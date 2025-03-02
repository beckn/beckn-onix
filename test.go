package main

import (
	"beckn-onix/plugins"
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
	// Ensure endpoint trimming to avoid leading slashes
	endpoint := strings.Trim(u.Path, "/")
	schemaFileName := fmt.Sprintf("%s.json", endpoint)

	fmt.Println("Handling request for endpoint:", endpoint)

	pluginsConfig, err := plugins.LoadPluginsConfig("plugins/config.yaml")
	if err != nil {
		http.Error(w, "Failed to load plugins configuration", http.StatusInternalServerError)
		return
	}

	_, validators, err := plugins.NewValidatorProvider(pluginsConfig)
	if err != nil {
		http.Error(w, "Failed to create PluginManager", http.StatusInternalServerError)
		return
	}

	validator, exists := validators[schemaFileName]
	if !exists {
		http.Error(w, fmt.Sprintf("Validator not found for %s", schemaFileName), http.StatusNotFound)
		return
	}

	payloadData, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read payload data", http.StatusInternalServerError)
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
