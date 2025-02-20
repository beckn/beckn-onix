package main

import (
	// Standard library packages
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	// BecknOnix packages
	log "beckn-onix/log"

	// Third-party packages
	"gopkg.in/yaml.v2" // For unmarshaling YAML
)

type config struct {
	AppName string `yaml:"appName"`
	Port    int    `yaml:"port"`
}


func run(ctx context.Context, configPath string) error {
	configuration, err := initConfig(ctx, configPath)
	if err != nil {
		return fmt.Errorf("error initializing config: %w", err)
	}

	fmt.Printf("App Name: %s\n", configuration.AppName)
	fmt.Printf("Server Port: %d\n", configuration.Port)

	port := fmt.Sprintf(":%d", configuration.Port)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		log.Log.Info("Received request:", r.Method, r.URL.Path, r.Header)

		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Message received successfully")
	},
	)

	log.Log.Info("Server started on", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		return fmt.Errorf("server failed to start: %w", err)
	}
	return nil
}

func initConfig(ctx context.Context, path string) (*config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open config file: %v", err)
	}
	defer file.Close()

	var config config
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal config data: %v", err)
	}
	if config.AppName == "" || config.Port == 0 {
		return nil, fmt.Errorf("missing required fields in config")
	}

	return &config, nil
}

var configPath string

func main() {
	flag.StringVar(&configPath, "config", "../../config/clientSideReciever-config.yaml", "../../config/clientSideReciever-config.yaml")
	flag.Parse()

	if err := run(context.Background(), configPath); err != nil {
		log.Log.Error("Application failed:", err)
	}
}
