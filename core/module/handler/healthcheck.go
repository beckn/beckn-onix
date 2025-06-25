package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// HealthCheckResponse defines the structure for our health check JSON response.
type healthCheckResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// healthHandler handles requests to the /health endpoint.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	// Ensure the request method is GET.
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	response := healthCheckResponse{
		Status:  "ok",
		Service: "beckn-adapter",
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		fmt.Printf("Error encoding health check response: %v\n", err)
		return
	}
}