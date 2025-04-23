package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/beckn-onix/pkg/plugin"
)

var (
	manager *plugin.Manager
)

func main() {
	var err error
	// Load the configuration.
	config, err := plugin.LoadConfig("pkg/plugin/plugin.yaml")
	if err != nil {
		log.Fatalf("Failed to load plugins configuration: %v", err)
	}

	// Initialize the plugin manager.
	manager, err = plugin.NewManager(context.Background(), config)
	if err != nil {
		log.Fatalf("Failed to create PluginManager: %v", err)
	}

	// Get the validator.
	validator, _, defErr := manager.SchemaValidator(context.Background())
	if defErr != nil {
		log.Fatalf("Failed to get validators: %v", defErr)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		validateHandler(w, r, validator)
	})
	fmt.Println("Starting server on port 8084...")
	err = http.ListenAndServe(":8084", nil)
	if err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func validateHandler(w http.ResponseWriter, r *http.Request, validators definition.SchemaValidator) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// Extract endpoint from request URL.
	requestURL := r.RequestURI
	u, err := url.ParseRequestURI(requestURL)
	if err != nil {
		http.Error(w, "Failed to parse request URL", http.StatusBadRequest)
		return
	}

	payloadData, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read payload data", http.StatusInternalServerError)
		return
	}

	ctx := context.Background()
	// validationErr := validators.Validate(ctx, u, payloadData)
	// if validationErr != (definition.SchemaValError{}) {
	// 	http.Error(w, fmt.Sprintf("Document validation failed: %v", validationErr), http.StatusBadRequest)
	// } else if !valid {
	// 	http.Error(w, "Document validation failed", http.StatusBadRequest)
	// } else {
	// 	w.WriteHeader(http.StatusOK)
	// 	if _, err := w.Write([]byte("Document validation succeeded!")); err != nil {
	// 		log.Fatalf("Failed to write response: %v", err)
	// 	}
	// }
	validationErr := validators.Validate(ctx, u, payloadData)
	if validationErr != nil {
		// Handle other types of errors
		http.Error(w, fmt.Sprintf("Schema validation failed: %v", validationErr), http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Schema validation succeeded!")); err != nil {
			log.Fatalf("Failed to write response: %v", err)
		}
	}
}
