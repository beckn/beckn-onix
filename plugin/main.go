package main

import (
	"beckn-onix/plugin/pubsub"
	logger "beckn-onix/shared/utils"
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.Log.Info("Panic recovered:", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func main() {
	manager := pubsub.NewPluginManager("plugins", "../config/config.yaml")

	

	err := manager.LoadPlugin("publisher")
	if err != nil {
		logger.Log.Error("Error loading publisher plugin: ", err)
	}

	// Create server mux
	mux := http.NewServeMux()

	// Add routes
	mux.HandleFunc("/publish", func(w http.ResponseWriter, r *http.Request) {
		orderMsg := "New message received: #12345"
		manager.PublishMessage("order", orderMsg)

		// Also publish to GCP Pub/Sub if publisher is available
		manager.PublishMessage("publisher", orderMsg)

	})

	// Create server with recovery middleware
	server := &http.Server{
		Addr:    "0.0.0.0:8080",
		Handler: recoveryMiddleware(mux),
	}

	// Start server in goroutine
	go func() {
		logger.Log.Info("Server starting on ", server.Addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Setup graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Log.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Log.Error("Server shutdown error: ", err)
	}
	logger.Log.Info("Server stopped gracefully")
}
