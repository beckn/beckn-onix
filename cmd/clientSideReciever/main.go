package main

import (
	shared "beckn-onix/shared"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"gopkg.in/yaml.v2" // For unmarshaling YAML
	"log"
	"net/http"
	"os"
)

type Config struct {
	AppName    string `yaml:"app_name"`
	ServerPort int    `yaml:"port"`
}

func InitConfig(ctx context.Context, path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open config file: %v", err)
	}
	defer file.Close()

	var config Config
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal config data: %v", err)
	}

	return &config, nil
}

func main() {
	configPath := flag.String("config", "config.yaml", "./config.yaml")
	flag.Parse()

	ctx := context.Background()

	configuration, err := InitConfig(ctx, *configPath)
	if err != nil {
		log.Fatalf("Error initializing config: %v", err)
	}

	fmt.Printf("App Name: %s\n", configuration.AppName)
	fmt.Printf("Server Port: %d\n", configuration.ServerPort)

	port := fmt.Sprintf(":%d", configuration.ServerPort)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var req any
			// Parse JSON body
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "Message recieved successfully")
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	},
	)

	shared.Log.Info("Server started on", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		shared.Log.Error("Server started on", port)
	}
}
