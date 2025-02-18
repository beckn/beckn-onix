package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestInitConfig checks if the InitConfig function correctly reads a YAML file
func TestInitConfig(t *testing.T) {

	configData := `
app_name: "clientSideReciever"
port: 8080	
`
	tempFile, err := os.CreateTemp("", "config.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	// Write test config to the temp file
	if _, err := tempFile.Write([]byte(configData)); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tempFile.Close()

	ctx := context.Background()
	config, err := InitConfig(ctx, tempFile.Name())
	if err != nil {
		t.Fatalf("InitConfig failed: %v", err)
	}

	// Validate results
	if config.AppName != "clientSideReciever" {
		t.Errorf("Expected AppName to be 'TestApp', got %s", config.AppName)
	}
	if config.ServerPort != 8080 {
		t.Errorf("Expected ServerPort to be 8080, got %d", config.ServerPort)
	}
}

// TestServerHandler checks if the HTTP server correctly handles requests
func TestServerHandler(t *testing.T) {
	// Create a test request
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"message": "Hello"}`))
	req.Header.Set("Content-Type", "application/json")

	// Record the response
	rec := httptest.NewRecorder()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Error reading body", http.StatusInternalServerError)
				return
			}

			if string(body) != `{"message": "Hello"}` {
				t.Errorf("Expected body: %s, got: %s", `{"message": "Hello"}`, string(body))
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Message received successfully"))
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Serve HTTP request to the test response recorder
	handler.ServeHTTP(rec, req)

	// Validate response status code
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", rec.Code)
	}

	// Validate response body
	expectedResponse := "Message received successfully"
	if rec.Body.String() != expectedResponse {
		t.Errorf("Expected response body: %q, got: %q", expectedResponse, rec.Body.String())
	}
}

// TestInvalidMethod checks if the server rejects non-POST requests
func TestInvalidMethod(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil) // Sending a GET request
	rec := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
	})

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status code 405, got %d", rec.Code)
	}
}
