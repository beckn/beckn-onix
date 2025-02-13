package main

import (
	"beckn-onix/cmd/clientSideReciever/src/config"
	"beckn-onix/cmd/clientSideReciever/src/routes"
	utils "beckn-onix/shared/utils"
	"net/http"
)

func main() {
	// Load configuration
	config.LoadEnv()
	port := config.GetPort()

	// Initialize router
	mux := routes.InitializeRoutes()

	utils.Logger.Println("Server started on", port)
	if err := http.ListenAndServe(port, mux); err != nil {
		utils.Logger.Fatal("Server started on", port)
	}
}
