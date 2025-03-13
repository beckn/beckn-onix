package main

import (
	"beckn-onix/shared/plugin"
	"beckn-onix/shared/plugin/definition"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
)

var (
	manager    *plugin.Manager
	validators map[string]definition.Validator
)

// Payload represents the structure of the payload with context information.
type Payload struct {
	Context struct {
		Action   string `json:"action"`
		BapID    string `json:"bap_id"`
		BapURI   string `json:"bap_uri"`
		BppID    string `json:"bpp_id"`
		BppURI   string `json:"bpp_uri"`
		Domain   string `json:"domain"`
		Location struct {
			City struct {
				Code string `json:"code"`
			} `json:"city"`
			Country struct {
				Code string `json:"code"`
			} `json:"country"`
		} `json:"location"`
		MessageID     string `json:"message_id"`
		Timestamp     string `json:"timestamp"`
		TransactionID string `json:"transaction_id"`
		TTL           string `json:"ttl"`
		Version       string `json:"version"`
	} `json:"context"`
	Message struct {
		CancellationReasonID string `json:"cancellation_reason_id"`
		Descriptor           struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"descriptor"`
		OrderID string `json:"order_id"`
	} `json:"message"`
}

func main() {
	var err error
	// Load the configuration
	config, err := plugin.LoadConfig("shared/plugin/plugin.yaml")
	if err != nil {
		log.Fatalf("Failed to load plugins configuration: %v", err)
	}

	// Initialize the plugin manager
	manager, err = plugin.NewManager(context.Background(), config)
	if err != nil {
		log.Fatalf("Failed to create PluginManager: %v", err)
	}

	// Get the validators map
	validators, defErr := manager.Validators(context.Background())
	if defErr != (definition.Error{}) {
		log.Fatalf("Failed to get validators: %v", defErr)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		validateHandler(w, r, validators)
	})
	fmt.Println("Starting server on port 8084...")
	err = http.ListenAndServe(":8084", nil)
	if err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func validateHandler(w http.ResponseWriter, r *http.Request, validators map[string]definition.Validator) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// Extract endpoint from request URL
	requestURL := r.RequestURI
	u, err := url.ParseRequestURI(requestURL)
	if err != nil {
		http.Error(w, "Failed to parse request URL", http.StatusBadRequest)
		return
	}

	payloadData, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read payload data", http.StatusInternalServerError)
		return
	}
	var payload Payload
	err = json.Unmarshal(payloadData, &payload)
	if err != nil {
		log.Printf("Failed to parse JSON payload: %v", err)
		http.Error(w, fmt.Sprintf("Failed to parse JSON payload: %v", err), http.StatusBadRequest)
		return
	}

	// Validate that the domain and version fields are not empty
	if payload.Context.Domain == "" || payload.Context.Version == "" {
		http.Error(w, "Invalid payload: domain and version are required fields", http.StatusBadRequest)
		return
	}
	schemaFileName := "ondc_trv10_v2.0.0_cancel"

	validator, exists := validators[schemaFileName]
	if !exists {
		http.Error(w, fmt.Sprintf("Validator not found for %s", schemaFileName), http.StatusNotFound)
		return
	}

	ctx := context.Background()
	valid, validationErr := validator.Validate(ctx, u, payloadData)
	if validationErr != (definition.Error{}) {
		http.Error(w, fmt.Sprintf("Document validation failed: %v", validationErr), http.StatusBadRequest)
	} else if !valid {
		http.Error(w, "Document validation failed", http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Document validation succeeded!"))
	}
}
