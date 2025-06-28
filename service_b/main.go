package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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

type ViaCEPResponse struct {
	Localidade string `json:"localidade,omitempty"`
	Erro       bool   `json:"erro,omitempty"`
}

type WeatherAPIResponse struct {
	Current struct {
		TempC float64 `json:"temp_c"`
	} `json:"current"`
}

type IApiClient interface {
	getCityByCEP(cep string) (string, error)
	getTemperatureByCity(cep string) (float64, error)
}

type ApiClient struct {
	httpGet        func(url string) (resp *http.Response, err error)
	wheatherApiKey string
}

func NewClient(
	httpGet func(url string) (resp *http.Response, err error),
	wheatherApiKey string,
) *ApiClient {
	return &ApiClient{
		httpGet:        httpGet,
		wheatherApiKey: wheatherApiKey,
	}
}

type WeatherHandler struct {
	apiClient IApiClient
	tracer    trace.Tracer
}

func NewWeatherHandler(apiClient IApiClient, tracer trace.Tracer) *WeatherHandler {
	return &WeatherHandler{
		apiClient: apiClient,
		tracer:    tracer,
	}
}

// load env vars cfg
func init() {
	viper.AutomaticEnv()
}

func main() {

	apiKey := viper.GetString("WEATHERAPI_KEY")
	if apiKey == "" {
		log.Fatalf("weatherapi key not set")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	shutdown, err := common.InitProvider("service_b", viper.GetString("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Fatal("failed to shutdown TracerProvider: %w", err)
		}
	}()

	tracer := otel.Tracer("microservice-tracer")

	client := NewClient(http.Get, apiKey)
	wh := NewWeatherHandler(client, tracer)

	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Logger)
	router.Use(middleware.Timeout(60 * time.Second))
	router.HandleFunc("/weather", wh.weatherHandler)
	log.Printf("Listening on port 8080")
	log.Fatal(http.ListenAndServe(":8080", router))

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

func (wh *WeatherHandler) weatherHandler(w http.ResponseWriter, r *http.Request) {

	carrier := propagation.HeaderCarrier(r.Header)
	ctx := r.Context()
	ctx = otel.GetTextMapPropagator().Extract(ctx, carrier)

	ctx, span := wh.tracer.Start(ctx, "Validate inputs")

	cep := r.URL.Query().Get("cep")

	if !common.IsValidCEP(cep) { // retorna o erro 422
		http.Error(w, "invalid zipcode", http.StatusUnprocessableEntity)
		span.SetStatus(codes.Error, "invalid zipcode")
		span.End()
		return
	}

	span.End()

	ctx, span = wh.tracer.Start(ctx, "Get City from Zipcode")

	city, err := wh.apiClient.getCityByCEP(cep)
	if err != nil { // retorna o erro 404
		http.Error(w, "can not find zipcode", http.StatusNotFound)
		span.RecordError(err)
		span.SetStatus(codes.Error, "can not find zipcode")
		span.End()
		return
	}
	span.End()

	ctx, span = wh.tracer.Start(ctx, "Get City temperature")
	defer span.End()
	tempC, err := wh.apiClient.getTemperatureByCity(city)
	if err != nil { // retorna 404 caso a cidade do cep não seja encontrada
		http.Error(w, "can not find temperature", http.StatusNotFound)
		span.RecordError(err)
		span.SetStatus(codes.Error, "can not find temperature")
		return
	}

	resp := common.WeatherResponse{
		City:  city,
		TempC: tempC,
		TempF: tempC*1.8 + 32,
		TempK: tempC + 273,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (c *ApiClient) getCityByCEP(cep string) (string, error) {
	resp, err := c.httpGet(fmt.Sprintf("https://viacep.com.br/ws/%s/json/", cep))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var viaCEP ViaCEPResponse
	if err := json.Unmarshal(body, &viaCEP); err != nil {
		return "", err
	}
	if viaCEP.Erro || viaCEP.Localidade == "" {
		return "", fmt.Errorf("not found")
	}
	return viaCEP.Localidade, nil
}

func (c *ApiClient) getTemperatureByCity(city string) (float64, error) {
	url := fmt.Sprintf("https://api.weatherapi.com/v1/current.json?key=%s&q=%s", c.wheatherApiKey, url.QueryEscape(city))
	resp, err := c.httpGet(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var weather WeatherAPIResponse
	if err := json.Unmarshal(body, &weather); err != nil {
		return 0, err
	}
	return weather.Current.TempC, nil
}
