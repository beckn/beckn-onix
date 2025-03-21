package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// setupMockServer initializes a mock HTTP server for testing API calls.
func setupMockServer() *httptest.Server {
	mockHandler := http.NewServeMux()
	mockHandler.HandleFunc("/beckn-api", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "success"}`))
	})
	return httptest.NewServer(mockHandler)
}

// TestLoadConfig verifies that the configuration file is correctly loaded.
func TestLoadConfig(t *testing.T) {
	tempConfig := "test_config.yaml"
	configContent := "bap_url: http://localhost:8080\nport: \"8081\""
	os.WriteFile(tempConfig, []byte(configContent), 0644)
	defer os.Remove(tempConfig)

	err := loadConfig(tempConfig)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.BapURL != "http://localhost:8080" || cfg.Port != "8081" {
		t.Errorf("Unexpected config values: %+v", cfg)
	}
}

// TestWebhookHandler checks if the webhook endpoint correctly handles requests.
func TestWebhookHandler(t *testing.T) {
	tests := []struct {
		name         string
		requestBody  string
		expectStatus int
	}{
		{
			"Valid Webhook Request",
			"{\"event\":\"order_placed\"}",
			http.StatusOK,
		},
		{
			"Invalid Webhook Request",
			"invalid-json",
			http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			webhookHandler(w, req)
			if w.Code != tc.expectStatus {
				t.Errorf("Expected status %d, got %d", tc.expectStatus, w.Code)
			}
		})
	}
}

// TestCallAPIHandler verifies API call handling with valid and invalid query parameters.
func TestCallAPIHandler(t *testing.T) {
	mockServer := setupMockServer()
	defer mockServer.Close()

	cfg.BapURL = mockServer.URL + "/beckn-api"
	tests := []struct {
		name         string
		queryParam   string
		expectStatus int
	}{
		{"Valid Query Param", "?query=test", http.StatusOK},
		{"Missing Query Param", "", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/call-api"+tc.queryParam, nil)
			w := httptest.NewRecorder()
			callAPIHandler(w, req)
			if w.Code != tc.expectStatus {
				t.Errorf("Expected status %d, got %d", tc.expectStatus, w.Code)
			}
		})
	}
}

// TestWebhookDataHandler ensures the webhook data retrieval endpoint functions correctly.
func TestWebhookDataHandler(t *testing.T) {
	mu.Lock()
	webhookData = []map[string]interface{}{
		{"event": "test_event"},
	}
	mu.Unlock()

	req := httptest.NewRequest("GET", "/webhook-data", nil)
	w := httptest.NewRecorder()
	webhookDataHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}
}

// TestMsgIdHandler validates the message ID retrieval endpoint.
func TestMsgIdHandler(t *testing.T) {
	mu.Lock()
	msgID = "12345"
	mu.Unlock()

	req := httptest.NewRequest("GET", "/msgId", nil)
	w := httptest.NewRecorder()
	msgIdHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}
}

// TestMainFunction checks if the main application starts correctly and serves requests.
func TestMainFunction(t *testing.T) {
	tempConfig := "test_config.yaml"
	configContent := "bap_url: http://localhost:8080\nport: \"8081\""
	os.WriteFile(tempConfig, []byte(configContent), 0644)
	defer os.Remove(tempConfig)

	os.Args = []string{"cmd", "-config", tempConfig}

	// Start the server in a goroutine
	go func() {
		err := run()
		if err != nil {
			t.Errorf("Run function failed: %v", err)
		}
	}()

	// Allow time for the server to start
	time.Sleep(1 * time.Second)

	// Perform a simple health check on the root endpoint
	resp, err := http.Get("http://localhost:8081/")
	if err != nil {
		t.Fatalf("Server did not start: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}
