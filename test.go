package main

import (
	"beckn-onix/plugins"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// Payload represents the structure of the data payload with context information.
type Payload struct {
	Context struct {
		Domain  string `json:"domain"`
		Version string `json:"version"`
	} `json:"context"`
}

func main() {
	http.HandleFunc("/", validateHandler)
	fmt.Println("Starting server on port 8084...")
	err := http.ListenAndServe(":8084", nil)
	if err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func validateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// Extract endpoint from request URL
	requestURL := r.RequestURI
	u, err := url.Parse(requestURL)
	if err != nil {
		http.Error(w, "Failed to parse request URL", http.StatusBadRequest)
		return
	}

	payloadData, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read payload data", http.StatusInternalServerError)
		return
	}

	// Initialize an instance of Payload struct
	var payload Payload
	err1 := json.Unmarshal(payloadData, &payload)
	if err1 != nil {
		http.Error(w, "Failed to parse JSON payload", http.StatusBadRequest)
		return
	}

	// Extract the domain, version, and endpoint from the payload and URL
	domain := payload.Context.Domain
	version := payload.Context.Version
	version = fmt.Sprintf("v%s", version)

	endpoint := strings.Trim(u.Path, "/")
	fmt.Println("Handling request for endpoint:", endpoint)
	domain = strings.ToLower(domain)
	domain = strings.ReplaceAll(domain, ":", "_")

	schemaFileName := fmt.Sprintf("%s/%s/%s.json", domain, version, endpoint)

	pluginsConfig, err := plugins.LoadPluginsConfig("plugins/config.yaml")
	if err != nil {
		http.Error(w, "Failed to load plugins configuration", http.StatusInternalServerError)
		return
	}
	debug := true

	_, validators, err := plugins.NewValidatorProvider(pluginsConfig, debug)
	if err != nil {
		http.Error(w, "Failed to create PluginManager", http.StatusInternalServerError)
		return
	}

	validator, exists := validators[schemaFileName]
	if !exists {
		http.Error(w, fmt.Sprintf("Validator not found for %s", schemaFileName), http.StatusNotFound)
		return
	}

	ctx := r.Context()
	err = validator.Validate(ctx, payloadData)
	if err != nil {
		http.Error(w, fmt.Sprintf("Document validation failed: %v", err), http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Document validation succeeded!"))
	}
}
