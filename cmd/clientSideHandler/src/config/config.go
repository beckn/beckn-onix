package config

import (
	"beckn-onix/shared/utils"
	"os"

	"github.com/joho/godotenv"
)

// LoadEnv loads environment variables
func LoadEnv() {
	if err := godotenv.Load(); err != nil {
		utils.Log.Println("No .env file found, using system environment variables")
	}
}

// GetPort retrieves the server port from environment variables
func GetPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081" // Default port
	}
	return ":" + port
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
