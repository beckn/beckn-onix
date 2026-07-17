package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/version"
)

// buildInfo carries build-time identity fields for the running binary.
type buildInfo struct {
	Version      string `json:"version"`
	GitCommit    string `json:"gitCommit"`
	GitTreeState string `json:"gitTreeState"`
	BuildDate    string `json:"buildDate"`
}

// HealthCheckResponse defines the structure for our health check JSON response.
type healthCheckResponse struct {
	Status  string    `json:"status"`
	Service string    `json:"service"`
	Build   buildInfo `json:"build"`
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
		Build: buildInfo{
			Version:      version.Version,
			GitCommit:    version.GitCommit,
			GitTreeState: version.GitTreeState,
			BuildDate:    version.BuildDate,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		fmt.Printf("Error encoding health check response: %v\n", err)
		return
	}
}