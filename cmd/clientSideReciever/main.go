package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"beckn-onix/log"

	"gopkg.in/yaml.v2"
)

type config struct {
	AppName string `yaml:"appName"`
	Port    int    `yaml:"port"`
}

func requestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.Log.Info("Received request:", r.Method, r.URL.Path, r.Header)
	w.WriteHeader(http.StatusOK)
}

func run(ctx context.Context, configPath string) (*http.Server, error) {
	cfg, err := initConfig(ctx, configPath)
	if err != nil {
		log.Log.Error("error initializing config: ", err)
		return nil, err
	}

	port := fmt.Sprintf(":%d", cfg.Port)
	mux := http.NewServeMux()
	mux.HandleFunc("/", requestHandler)

	server := &http.Server{Addr: port, Handler: mux}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Log.Error("Server failed:", err)
		}
	}()

	return server, nil
}

func validateConfig(config *config) error {
	if config == nil {
		return errors.New("config is nil")
	}

	if len(strings.TrimSpace(config.AppName)) == 0 {
		return errors.New("missing required field: AppName")
	}

	if config.Port == 0 {
		return errors.New("missing required field: Port")
	}

	return nil
}

func initConfig(ctx context.Context, path string) (*config, error) {
	file, err := os.Open(path)
	if err != nil {
		log.Log.Error("could not open config file: ", err)
		return nil, err
	}
	defer file.Close()

	var config config
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		log.Log.Error("could not unmarshal config data: ", err)
		return nil, err
	}
	err = validateConfig(&config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

var configPath string

func getConfigPath() string {
	var configPath string
	flag.StringVar(&configPath, "config", "../../config/clientSideReciever-config.yaml", "Config file path")
	flag.Parse()
	return configPath
}

func execute() error {
	configPath := getConfigPath()
	server, err := run(context.Background(), configPath)
	if err != nil {
		log.Log.Error("Application failed:", err)
		return err // Return error instead of exiting
	}

	// Ensure the server shuts down gracefully on termination
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop

	log.Log.Info("Shutting down server...")
	if err := server.Shutdown(context.Background()); err != nil {
		log.Log.Error("Server shutdown failed:", err)
		return err
	}

	return nil
}

func main() {
	if err := execute(); err != nil {
		log.Log.Error("Application terminated with error:", err)
		os.Exit(1)
	}
}
