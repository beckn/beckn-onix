package main

import (
	"net/http"

	"beckn-onix/cmd/clientSideHandler/src/config"
	"beckn-onix/cmd/clientSideHandler/src/routes"
	utils "beckn-onix/shared/utils"
)

func main() {
	// Load environment variables
	config.LoadEnv()

	// Get server port
	port := config.GetPort()

	// Initialize router
	mux := routes.InitializeRoutes()

	// Start Pub/Sub Listener in a goroutine
	// go handlers.SubscribeToMessages(config.GetSubscriptionID())

	utils.Log.Println("Server started on", port)

	// Start the HTTP server
	if err := http.ListenAndServe(port, mux); err != nil {
		utils.Log.Fatalln("Failed to start server on", port, err)
	}
}
