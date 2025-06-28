package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mobenaus/fc-pos-go-labs-observabilidade/common"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Entrada struct {
	CEP string `json:"cep"`
}

type WebServer struct {
	Tracer trace.Tracer
}

// load env vars cfg
func init() {
	viper.AutomaticEnv()
}

func main() {

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	shutdown, err := common.InitProvider("service_a", viper.GetString("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Fatal("failed to shutdown TracerProvider: %w", err)
		}
	}()

	tracer := otel.Tracer("microservice-tracer")

	webserver := WebServer{
		Tracer: tracer,
	}

	router := getRouter(webserver)

	log.Println("Starting server on port", ":8000")
	if err := http.ListenAndServe(":8000", router); err != nil {
		log.Fatal(err)
	}

	select {
	case <-sigCh:
		log.Println("Shutting down gracefully, CTRL+C pressed...")
	case <-ctx.Done():
		log.Println("Shutting down due to other reason...")
	}

	// Create a timeout context for the graceful shutdown
	_, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
}

func getRouter(ws WebServer) *chi.Mux {
	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Logger)
	router.Use(middleware.Timeout(60 * time.Second))
	router.Post("/", ws.handleRequest)
	return router
}

func (ws *WebServer) handleRequest(w http.ResponseWriter, r *http.Request) {

	carrier := propagation.HeaderCarrier(r.Header)
	ctx := r.Context()
	ctx = otel.GetTextMapPropagator().Extract(ctx, carrier)

	ctx, spanValidation := ws.Tracer.Start(ctx, "Validate inputs")

	var entrada Entrada
	if err := json.NewDecoder(r.Body).Decode(&entrada); err != nil {
		http.Error(w, "payload inválido", http.StatusBadRequest)
		spanValidation.RecordError(err)
		spanValidation.SetStatus(codes.Error, "payload inválido")
		spanValidation.End()
		return
	}

	if !common.IsValidCEP(entrada.CEP) { // retorna o erro 422
		http.Error(w, "invalid zipcode", http.StatusUnprocessableEntity)
		spanValidation.SetStatus(codes.Error, "invalid zipcode")
		spanValidation.End()
		return
	}

	spanValidation.End()

	ctx, span := ws.Tracer.Start(ctx, "Call to service_b")
	defer span.End()

	response, err := ws.getTemperatura(ctx, entrada)
	if err != nil {

		http.Error(w, "Falha para recuperar os dados", http.StatusInternalServerError)
		span.RecordError(err)
		span.SetStatus(codes.Error, "Falha para recuperar os dados")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (ws *WebServer) getTemperatura(tracectx context.Context, entrada Entrada) (common.WeatherResponse, error) {

	ctx, cancel := context.WithTimeout(tracectx, 5000*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/weather?cep=%s", viper.GetString("WEATHER_SERVICE"), entrada.CEP), nil)
	if err != nil {
		return common.WeatherResponse{}, err
	}

	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return common.WeatherResponse{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return common.WeatherResponse{}, err
	}
	var response common.WeatherResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return common.WeatherResponse{}, err
	}
	return response, nil
}
