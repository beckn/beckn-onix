package config

import (
	"log"
	"os"

	"context"
	"fmt"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v2" // For unmarshaling YAML
)

// Config struct captures all the necessary configuration
type Config struct {
	AppName    string `yaml:"app_name"`
	ServerPort int    `yaml:"server_port"`
	DBHost     string `yaml:"db_host"`
	DBPort     int    `yaml:"db_port"`
	DBUser     string `yaml:"db_user"`
	DBPassword string `yaml:"db_password"`
}

func InitConfig(ctx context.Context, path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open config file: %v", err)
	}
	defer file.Close()

	// Unmarshal the YAML data
	var config Config
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal config data: %v", err)
	}

	return &config, nil
}

func LoadEnv() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: .env file not found, using system environment variables")
	}
}

func GetPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = ":8080" // Default port
	}
	return port
}
