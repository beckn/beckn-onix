package main

import (
	config "beckn-onix/cmd/clientSideReceiver/config"
	handlers "beckn-onix/cmd/clientSideReceiver/handler"
	utils "beckn-onix/shared/utils"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {

	// Define the command-line flag for the YAML file path
	configPath := flag.String("config", "config.yaml", "./config.yaml")
	flag.Parse()

	// Define the context (could be used for cancellation or timeouts)
	ctx := context.Background()

	// Load the configuration using InitConfig
	configuration, err := config.InitConfig(ctx, *configPath)
	if err != nil {
		log.Fatalf("Error initializing config: %v", err)
	}

	// Use the config to initialize the server
	fmt.Printf("App Name: %s\n", configuration.AppName)
	fmt.Printf("Server Port: %d\n", configuration.ServerPort)
	fmt.Printf("Database Host: %s\n", configuration.DBHost)
	fmt.Printf("Database Port: %d\n", configuration.DBPort)
	fmt.Printf("Database User: %s\n", configuration.DBUser)

	port := fmt.Sprintf(":%d", configuration.ServerPort)

	// Initialize router
	http.HandleFunc("/", handlers.HomeHandler)

	utils.Log.Info("Server started on", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		utils.Log.Error("Server started on", port)
	}
}
