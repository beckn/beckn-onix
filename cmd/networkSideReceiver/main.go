package main

import (
	"fmt"
	"net/http"
	"os"
	"gopkg.in/yaml.v2"
	
	logger "beckn-onix/shared/utils"
)

type Config struct {
	AppName string `yaml:"appName"`
	Port    int    `yaml:"port"`
}

func main() {
	cfg := loadConfig()
	StartServer(cfg)
}

func StartServer(cfg *Config) {
	http.HandleFunc("/", CreatePostHandler) // Fixed: Removed "POST /"

	logger.Log.Info("Server is running on port: ", cfg.Port)
	logger.Log.Error(http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), nil))
}

func loadConfig() *Config {

	data, err := os.ReadFile("../../config/networkSideReceiver-config.yaml")
	if err != nil {
		logger.Log.Error("error reading config file:", err)
	}

	var config Config

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		logger.Log.Error("error unmarshaling config:", err)
	}

	return &config
}


func CreatePostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
}


