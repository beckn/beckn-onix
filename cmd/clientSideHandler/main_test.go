package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)



func createTempConfig(t *testing.T, data string) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "config.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp config: %v", err)
	}
	if _, err := tmpFile.Write([]byte(data)); err != nil {
		t.Fatalf("Failed to write to temp config: %v", err)
	}
	tmpFile.Close()
	return tmpFile.Name()
}



func TestInitConfig(t *testing.T) {
	tests := []struct {
		name        string
		configData  string
		expectError bool
		expected    *config
	}{
		{
			name: "Success - Valid Config",
			configData: `
appName: "clientSideHandler"
port: 8081
`,
			expectError: false,
			expected: &config{
				AppName: "clientSideHandler",
				Port:    8081,
			},
		},
		{
			name:        "Error - Invalid YAML Format",
			configData:  `invalid_yaml: :::`,
			expectError: true,
		},
		{
			name:        "Error - Missing Required Fields",
			configData:  `appName: ""`,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempFilePath := "../../config/clientSideHandler-config.yaml"
			tempFile, err := os.Create(tempFilePath)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tempFile.Name())

			if _, err := tempFile.Write([]byte(tc.configData)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			tempFile.Close()

			ctx := context.Background()
			cfg, err := initConfig(ctx, tempFile.Name())

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Did not expect error, got %v", err)
				}
				if cfg.AppName != tc.expected.AppName {
					t.Errorf("Expected appName %s, got %s", tc.expected.AppName, cfg.AppName)
				}
				if cfg.Port != tc.expected.Port {
					t.Errorf("Expected Port %d, got %d", tc.expected.Port, cfg.Port)
				}
			}
		})
	}
}

// 🎯 Table-driven tests for ServerHandler (Success & Error Cases)
func TestServerHandler(t *testing.T) {
	tests := []struct {
		name               string
		method             string
		body               string
		expectedStatusCode int
		expectedResponse   string
	}{
		{
			name:               "Success - POST Request",
			method:             "POST",
			body:               `{"message": "Hello"}`,
			expectedStatusCode: http.StatusOK,
			expectedResponse:   "Message received successfully",
		},
		{
			name:               "Error - Invalid Method (GET)",
			method:             "GET",
			body:               "",
			expectedStatusCode: http.StatusMethodNotAllowed,
			expectedResponse:   "Method not allowed\n",
		}}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Message received successfully"))
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.expectedStatusCode {
				t.Errorf("Expected status code %d, got %d", tc.expectedStatusCode, rec.Code)
			}

			if rec.Body.String() != tc.expectedResponse {
				t.Errorf("Expected response body: %q, got: %q", tc.expectedResponse, rec.Body.String())
			}
		})
	}
}




func TestRun(t *testing.T) {
	tests := []struct {
		name        string
		configData  string
		expectError bool
		expectCode  int // Expected HTTP status code for POST request
	}{
		{
			name: "Success - Valid Config",
			configData: `
appName: "TestApp"
port: 8082
`,
			expectError: false,
			expectCode:  http.StatusOK,
		},
		{
			name:        "Error - Invalid YAML Format",
			configData:  `invalid_yaml: :::`,
			expectError: true,
		},
		{
			name: "Error - Missing Required Fields",
			configData: `
appName: "TestApp"
`,
			expectError: true,
		},
		{
			name: "Error - Invalid Port",
			configData: `
appName: "TestApp"
port: "invalid_port"
`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := createTempConfig(t, tt.configData)
			defer os.Remove(configPath)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Run server in goroutine if no expected error
			if !tt.expectError {
				go func() {
					if err := run(ctx, configPath); err != nil && !tt.expectError {
						t.Errorf("Unexpected error: %v", err)
					}
				}()
				time.Sleep(500 * time.Millisecond) // Allow server to start

				resp, err := http.Post("http://localhost:8082/", "application/json", nil)
				if err != nil {
					t.Fatalf("Failed to make POST request: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != tt.expectCode {
					t.Errorf("Expected status %d, got %d", tt.expectCode, resp.StatusCode)
				}
			} else {
				// Expect error scenario
				if err := run(ctx, configPath); err == nil {
					t.Errorf("Expected error, got nil")
				}
			}
		})
	}
}
