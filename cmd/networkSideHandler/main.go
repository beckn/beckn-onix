package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	logger "beckn-onix/shared/utils"

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
}

func run(ctx context.Context, configPath string) error {
	configuration, err := initConfig(ctx, configPath)
	if err != nil {
		logger.Log.Error("error initializing config: ", err)
		return err
	}
	port := configuration.Port
	http.HandleFunc("/", requestHandler)

	server := &http.Server{Addr: fmt.Sprintf(":%d", port)}
	logger.Log.Info("Server starting on port:", port)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("Server failed:", err)
		}
	}()

	<-ctx.Done()
	logger.Log.Info("Shutting down server...")
	return server.Shutdown(context.Background())
}

func initConfig(_ctx context.Context, path string) (*config, error) {
	file, err := os.Open(path)
	if err != nil {
		 logger.Log.Error("could not open config file: ", err)
		return nil, err
	}
	defer file.Close()

	var config config
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		 logger.Log.Error("could not unmarshal config data: ", err)
		 return nil, err
	}
	if config.AppName == "" || config.Port == 0 {
		return nil, fmt.Errorf("missing required fields in config")
	}

	return &config, nil
}

var configPath string

func main() {
	flag.StringVar(&configPath, "config", "../../config/networkSideHandler-config.yaml", "../../config/networkSideHandler-config.yaml")
	flag.Parse()

	if err := run(context.Background(), configPath); err != nil {
		logger.Log.Fatalln("Application failed:", err)
	}
}