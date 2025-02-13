package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// LoadEnv loads environment variables from .env file
func LoadEnv() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: .env file not found, using system environment variables")
	}
}

// GetPort retrieves the server port from environment variables
func GetPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = ":8080" // Default port
	}
	return port
}


// GetProjectID retrieves the Google Cloud Project ID
func GetProjectID() string {
	return os.Getenv("GOOGLE_CLOUD_PROJECT")
}

// GetSubscriptionID retrieves the Pub/Sub subscription ID
func GetSubscriptionID() string {
	return os.Getenv("PUBSUB_SUBSCRIPTION_ID")

}


func GetTopicID() string {
	return os.Getenv("PUBSUB_TOPIC_ID")
}
