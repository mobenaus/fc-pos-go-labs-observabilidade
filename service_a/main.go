package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Entrada struct {
	CEP string `json:"cep"`
}

type WeatherResponse struct {
	City  string  `json:"city"`
	TempC float64 `json:"temp_C"`
	TempF float64 `json:"temp_F"`
	TempK float64 `json:"temp_K"`
}

func main() {
	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Logger)
	router.Use(middleware.Timeout(60 * time.Second))
	// promhttp
	//router.Handle("/metrics", promhttp.Handler())
	router.Post("/", handleRequest)

	log.Println("Starting server on port", ":8000")
	if err := http.ListenAndServe(":8000", router); err != nil {
		log.Fatal(err)
	}
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	var entrada Entrada
	if err := json.NewDecoder(r.Body).Decode(&entrada); err != nil {
		http.Error(w, "payload inválido", http.StatusBadRequest)
		return
	}
	response, err := getCotacao(entrada)
	if err != nil {
		http.Error(w, "Falha para recuperar os dados", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getCotacao(entrada Entrada) (WeatherResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5000*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://localhost:8080/weather?cep=%s", entrada.CEP), nil)
	if err != nil {
		return WeatherResponse{}, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return WeatherResponse{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return WeatherResponse{}, err
	}
	var response WeatherResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return WeatherResponse{}, err
	}
	return response, nil
}
