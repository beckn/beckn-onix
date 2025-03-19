package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/beckn-onix/pkg/plugin"
)

var (
	manager *plugin.Manager
)

// Payload represents the structure of the payload with context information.
// type Payload struct {
// 	Context struct {
// 		Action   string `json:"action"`
// 		BapID    string `json:"bap_id"`
// 		BapURI   string `json:"bap_uri"`
// 		BppID    string `json:"bpp_id"`
// 		BppURI   string `json:"bpp_uri"`
// 		Domain   string `json:"domain"`
// 		Location struct {
// 			City struct {
// 				Code string `json:"code"`
// 			} `json:"city"`
// 			Country struct {
// 				Code string `json:"code"`
// 			} `json:"country"`
// 		} `json:"location"`
// 		MessageID     string `json:"message_id"`
// 		Timestamp     string `json:"timestamp"`
// 		TransactionID string `json:"transaction_id"`
// 		TTL           string `json:"ttl"`
// 		Version       string `json:"version"`
// 	} `json:"context"`
// 	Message struct {
// 		CancellationReasonID string `json:"cancellation_reason_id"`
// 		Descriptor           struct {
// 			Code string `json:"code"`
// 			Name string `json:"name"`
// 		} `json:"descriptor"`
// 		OrderID string `json:"order_id"`
// 	} `json:"message"`
// }

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
	validator, _, defErr := manager.Validator(context.Background())
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
		// Check if the error is of type SchemaValidationErr
		if schemaErr, ok := validationErr.(*definition.SchemaValidationErr); ok {
			// Handle schema validation errors
			var errorMessages []string
			for _, err := range schemaErr.Errors {
				errorMessages = append(errorMessages, fmt.Sprintf("Path: %s, Message: %s", err.Path, err.Message))
			}
			errorMessage := fmt.Sprintf("Schema validation failed: %s", strings.Join(errorMessages, "; "))
			http.Error(w, errorMessage, http.StatusBadRequest)
		} else {
			// Handle other types of errors
			http.Error(w, fmt.Sprintf("Schema validation failed: %v", validationErr), http.StatusBadRequest)
		}
	} else {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Schema validation succeeded!")); err != nil {
			log.Fatalf("Failed to write response: %v", err)
		}
	}
}
