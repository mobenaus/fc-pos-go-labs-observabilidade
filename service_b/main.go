package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
)

type ViaCEPResponse struct {
	Localidade string `json:"localidade"`
	Erro       bool   `json:"erro,omitempty"`
}

type WeatherAPIResponse struct {
	Current struct {
		TempC float64 `json:"temp_c"`
	} `json:"current"`
}

type TempResponse struct {
	City  string  `json:"city"`
	TempC float64 `json:"temp_C"`
	TempF float64 `json:"temp_F"`
	TempK float64 `json:"temp_K"`
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
}

func NewWeatherHandler(apiClient IApiClient) *WeatherHandler {
	return &WeatherHandler{
		apiClient: apiClient,
	}
}

func main() {

	apiKey := os.Getenv("WEATHERAPI_KEY")
	if apiKey == "" {
		log.Fatalf("weatherapi key not set")
	}
	client := NewClient(http.Get, apiKey)
	wh := NewWeatherHandler(client)

	http.HandleFunc("/weather", wh.weatherHandler)
	log.Printf("Listening on port 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func (wh *WeatherHandler) weatherHandler(w http.ResponseWriter, r *http.Request) {
	cep := r.URL.Query().Get("cep")

	if !isValidCEP(cep) { // retorna o erro 422
		http.Error(w, "invalid zipcode", http.StatusUnprocessableEntity)
		return
	}

	city, err := wh.apiClient.getCityByCEP(cep)
	if err != nil { // retorna o erro 404
		http.Error(w, "can not find zipcode", http.StatusNotFound)
		return
	}

	tempC, err := wh.apiClient.getTemperatureByCity(city)
	if err != nil { // retorna 404 caso a cidade do cep não seja encontrada
		println(err.Error())
		http.Error(w, "can not find temperature", http.StatusNotFound)
		return
	}

	resp := TempResponse{
		City:  city,
		TempC: tempC,
		TempF: tempC*1.8 + 32,
		TempK: tempC + 273,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func isValidCEP(cep string) bool {
	re := regexp.MustCompile(`^\d{8}$`)
	return re.MatchString(cep)
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
