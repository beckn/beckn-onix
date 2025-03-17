package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"

	"gopkg.in/yaml.v2"
)

type config struct {
	BapURL string `yaml:"bap_url"`
	Port   string `yaml:"port"`
}

var (
	webhookData []map[string]interface{}
	msgID       string
	mu          sync.Mutex
)
var cfg config

// loadConfig reads configuration from config.yaml
func loadConfig(configPath string) error {
	file, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(file, &cfg); err != nil {
		return err
	}
	return nil
}

// webhookHandler handles incoming webhook responses
func webhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var data map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	mu.Lock()
	webhookData = append(webhookData, data)
	mu.Unlock()

	log.Printf("Received webhook response: %+v\n", data)
	w.WriteHeader(http.StatusOK)
}

// callBecknAPI calls a Beckn provider API
func CallBecknAPI(query string) {
	// request := &model.SearchRequest{
	// 	Criteria: model.SearchCriteria{
	// 		Domain: "local_retail",
	// 		Query:  query,
	// 	},
	// }

	// body, _ := json.Marshal(request)
	// client, err := idtoken.NewClient(context.Background(), cfg.BapURL)
	// if err != nil {
	// 	log.Fatalf("idtoken.NewClient: %w", err)
	// }
	// resp, err := client.Post(cfg.BapURL, "application/json", bytes.NewBuffer(body))
	// if err != nil {
	// 	msgID = ""
	// 	log.Fatalf("Error calling Beckn API: %v", err)
	// }
	// b, err := io.ReadAll(resp.Body)
	// if err != nil {
	// 	log.Fatal("Error reading search response")
	// }
	// msgID = string(b)
}

// uiHandler serves the UI page
func uiHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/index.html")
}

// callAPIHandler triggers the Beckn API call
func callAPIHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	go CallBecknAPI(query)
	w.Write([]byte("API Call Triggered"))
}

// webhookDataHandler returns webhook data
func webhookDataHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	mu.Lock()
	json.NewEncoder(w).Encode(webhookData)
	mu.Unlock()
}

// msgIdHandler returns API errors
func msgIdHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	mu.Lock()
	w.Write([]byte(msgID))
	mu.Unlock()
}

func main() {
	configPath := flag.String("config", "config.yaml", "../../config/byuerApp-config.yaml")
	flag.Parse()

	err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	http.HandleFunc("/webhook", webhookHandler)
	http.HandleFunc("/call-api", callAPIHandler)
	http.HandleFunc("/webhook-data", webhookDataHandler)
	http.HandleFunc("/msgId", msgIdHandler)
	http.HandleFunc("/", uiHandler)

	log.Printf("Server starting on port %s", cfg.Port)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, nil))
}
