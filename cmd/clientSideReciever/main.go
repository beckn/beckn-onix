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

	logpackage "beckn-onix/log"

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

	logpackage.Info(context.Background(), fmt.Sprintf("Received request: %s %s Headers: %v", r.Method, r.URL.Path, r.Header))
	w.WriteHeader(http.StatusOK)
}

func run(ctx context.Context, configPath string) (*http.Server, error) {
	cfg, err := initConfig(ctx, configPath)
	if err != nil {
		logpackage.Error(context.Background(), err, "error initializing config")
		return nil, err
	}

	port := fmt.Sprintf(":%d", cfg.Port)
	mux := http.NewServeMux()
	mux.HandleFunc("/", requestHandler)

	server := &http.Server{Addr: port, Handler: mux}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logpackage.Error(context.Background(), err, "Server failed")
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
		logpackage.Error(context.Background(), err, "Could not open config file")
		return nil, err
	}
	defer file.Close()

	var config config
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		logpackage.Error(context.Background(), err, "Could not open config file")
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
		logpackage.Error(context.Background(), err, "Application failed")
		return err 
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop

	logpackage.Info(context.Background(), "Shutting down server...")
	if err := server.Shutdown(context.Background()); err != nil {
		logpackage.Error(context.Background(), err, "Server shutdown failed:")
		return err
	}

	return nil
}

func main() {
	logpackage.InitLogger()
	if err := execute(); err != nil {
		logpackage.Error(context.Background(), err, "Application terminated with error")
		os.Exit(1)
	}
}
