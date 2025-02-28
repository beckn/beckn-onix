package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
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
appName: "networkSideHandler"
port: 8080
`,
			expectError: false,
			expected: &config{
				AppName: "networkSideHandler",
				Port:    8080,
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
			tempFilePath := createTempConfig(t, tc.configData)
			defer os.Remove(tempFilePath)

			ctx := context.Background()
			cfg, err := initConfig(ctx, tempFilePath)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Did not expect error, got %v", err)
				}
				if diff := cmp.Diff(tc.expected, cfg); diff != "" {
					t.Errorf("Config mismatch (-expected +got):\n%s", diff)
				}
			}
		})
	}
}

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
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Start the actual server
			server := httptest.NewServer(http.HandlerFunc(requestHandler))
			defer server.Close()

			req, err := http.NewRequest(tc.method, server.URL, strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Failed to send request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.expectedStatusCode {
				t.Errorf("Expected status code %d, got %d", tc.expectedStatusCode, resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("Failed to read response body: %v", err)
			}

			if string(body) != tc.expectedResponse {
				t.Errorf("Expected response body: %q, got: %q", tc.expectedResponse, string(body))
			}
		})
	}
}

func TestRunSuccess(t *testing.T) {
	tests := []struct {
		name       string
		configData string
		expectCode int // Expected HTTP status code for POST request
	}{
		{
			name: "Success - Valid Config",
			configData: `
appName: "TestApp"
port: 8082
`,
			expectCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := createTempConfig(t, tt.configData)
			defer os.Remove(configPath)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan struct{})

			go func() {
				if err := run(ctx, configPath); err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				close(done)
			}()

			select {
			case <-done:
				resp, err := http.Post("http://localhost:8082/", "application/json", nil)
				if err != nil {
					t.Fatalf("Failed to make POST request: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != tt.expectCode {
					t.Errorf("Expected status %d, got %d", tt.expectCode, resp.StatusCode)
				}
			case <-time.After(1 * time.Second):
				t.Fatal("Test timed out")
			}
		})
	}
}

func TestRunFailure(t *testing.T) {
	tests := []struct {
		name        string
		configData  string
		expectError bool
	}{
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

			if err := run(ctx, configPath); err == nil {
				t.Errorf("Expected error, got nil")
			}
		})
	}
}

func TestMainFunction(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "Default config path",
			args:    []string{"cmd"},
			wantErr: false,
		},
		{
			name:    "Invalid config path",
			args:    []string{"cmd", "-config=invalid/path.yaml"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Args = tt.args

			defer func() {
				if r := recover(); r != nil {
					if !tt.wantErr {
						t.Errorf("Unexpected panic (os.Exit simulation): %v", r)
					}
				}
			}()

			main()

			if tt.wantErr {
				t.Log("Expected an error scenario for main function.")
			}
		})
	}
}

func TestRequestHandler(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		expectCode int
	}{
		{
			name:       "Success - POST request",
			method:     http.MethodPost,
			expectCode: http.StatusOK,
		},
		{
			name:       "Fail - GET request",
			method:     http.MethodGet,
			expectCode: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/", nil)
			rr := httptest.NewRecorder()

			requestHandler(rr, req)

			if rr.Code != tt.expectCode {
				t.Errorf("Expected status code %d, got %d", tt.expectCode, rr.Code)
			}
		})
	}
}
