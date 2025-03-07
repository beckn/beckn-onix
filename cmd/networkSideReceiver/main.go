package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"beckn-onix/shared/log"
	"gopkg.in/yaml.v2"
	"strings"
)

type config struct {
	AppName string `yaml:"appName"`
	Port    int    `yaml:"port"`
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.Log.Info("Received request:", r.Method, r.URL.Path, r.Header)
	w.WriteHeader(http.StatusOK)
}

func run(ctx context.Context, configPath string) error {
	log.Log.Debug("Path: ", configPath)
	configuration, err := initConfig(ctx, configPath)
	if err != nil {
		log.Log.Error("Error initializing config: ", err)
		return err
	}
	log.Log.Debug("Config: ", configuration)

	port := fmt.Sprintf(":%d", configuration.Port)
	http.HandleFunc("/", handler)

	server := &http.Server{Addr: port}

	// Run server in a goroutine.
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Log.Error("Server failed:", err)
		}
	}()

	<-ctx.Done()
	log.Log.Info("Shutting down server...")
	return server.Shutdown(context.Background())
}

func (c *config) validate() error {
    if len(strings.TrimSpace(c.AppName)) == 0 {
        return fmt.Errorf("appName is required but was empty")
    }
    
    if c.Port == 0 {
        return fmt.Errorf("port is required but was not set")
    }
    
    if c.Port < 1024 || c.Port > 65535 {
        return fmt.Errorf("port must be between 1024 and 65535, got %d", c.Port)
    }

    return nil
}

func initConfig(ctx context.Context, path string) (*config, error) {
	file, err := os.Open(path)
	if err != nil {
		log.Log.Error("Could not open config file: ", err)
		return nil, err
	}
	defer file.Close()

	var config config
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		log.Log.Error("Could not unmarshal config data: ", err)
		return nil, err
	}
	if err := config.validate(); err != nil {
        log.Log.Error("Config validation failed: ", err)
        return nil, err
    }

	return &config, nil
}

var configPath string

func main() {
	flag.StringVar(&configPath, "config", "../../config/networkSideHandler-config.yaml", "../../config/networkSideHandler-config.yaml")
	flag.Parse()

	if err := run(context.Background(), configPath); err != nil {
		log.Log.Fatalln("Application failed:", err)
	}
}