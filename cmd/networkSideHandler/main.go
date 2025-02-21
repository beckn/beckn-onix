package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"gopkg.in/yaml.v2"
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

	log.Printf("Server %s is running on port: %d\n", cfg.AppName, cfg.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), nil))
}

func loadConfig() *Config {

	data, err := os.ReadFile("../../config/networkSideHandler-config.yaml")
	if err != nil {
		log.Fatalf("error reading config file: %v", err)
	}

	var config Config

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("error unmarshaling config: %v", err)
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


