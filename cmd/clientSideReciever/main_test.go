package main

import (
	"context"
	"flag"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *config
		wantErr string
	}{
		{"Nil Config", nil, "config is nil"},
		{"Missing AppName", &config{"", 8080}, "missing required field: AppName"},
		{"Missing Port", &config{"MyApp", 0}, "missing required field: Port"},
		{"Both AppName & Port Missing", &config{"", 0}, "missing required field: AppName"},
		{"Valid config", &config{"MyApp", 8080}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.config)
			if err != nil {
				if err.Error() != tt.wantErr {
					t.Errorf("expected error %q, got %q", tt.wantErr, err.Error())
				}
			} else if tt.wantErr != "" {
				t.Errorf("expected error %q, got nil", tt.wantErr)
			}
		})
	}
}

func TestInitConfigSuccess(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		expectError bool
		expected    *config
	}{
		{
			name:        "Success - Valid Config",
			file:        "valid_config.yaml",
			expectError: false,
			expected: &config{
				AppName: "clientSideReciever",
				Port:    9091,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join("testdata", tc.file)
			ctx := context.Background()
			cfg, err := initConfig(ctx, path)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Did not expect an error but got: %v", err)
				}
				if cfg.AppName != tc.expected.AppName {
					t.Errorf("Expected AppName %s, got %s", tc.expected.AppName, cfg.AppName)
				}
				if cfg.Port != tc.expected.Port {
					t.Errorf("Expected Port %d, got %d", tc.expected.Port, cfg.Port)
				}
			}
		})
	}
}

func TestInitConfigFailure(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		expectError bool
	}{
		{"Error - Invalid YAML Format", "invalid_yaml.yaml", true},
		{"Error - Missing Required Fields", "missing_required_fields.yaml", true},
		{"Error - Nonexistent File", "nonexistent.yaml", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join("testdata", tc.file)
			ctx := context.Background()
			_, err := initConfig(ctx, path)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error but got nil")
				}
			} else {
				t.Errorf("Did not expect an error but got one")
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
			expectedResponse:   "",
		},
		{
			name:               "Error - Invalid Method (GET)",
			method:             "GET",
			body:               "",
			expectedStatusCode: http.StatusMethodNotAllowed,
			expectedResponse:   "Method not allowed\n",
		}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			requestHandler(rec, req)

			if rec.Code != tc.expectedStatusCode {
				t.Errorf("Expected status code %d, got %d", tc.expectedStatusCode, rec.Code)
			}

			if rec.Body.String() != tc.expectedResponse {
				t.Errorf("Expected response body: %q, got: %q", tc.expectedResponse, rec.Body.String())
			}
		})
	}
}

func TestRunSuccess(t *testing.T) {
	tests := []struct {
		name       string
		configData string
		expectCode int
	}{
		{
			name: "Success - Valid Config",
			configData: `
appName: "TestApp"
port: 8083
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

			serverReady := make(chan struct{})
			serverError := make(chan error, 1)

			go func() {
				if _, err := run(ctx, configPath); err != nil {
					serverError <- err
				}
			}()

			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					default:
						conn, err := net.Dial("tcp", "localhost:8083")
						if err == nil {
							conn.Close()
							close(serverReady)
							return
						}
					}
				}
			}()

			select {
			case <-serverReady:

				resp, err := http.Post("http://localhost:8083/", "application/json", nil)
				if err != nil {
					t.Fatalf("Failed to make POST request: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != tt.expectCode {
					t.Errorf("Expected status %d, got %d", tt.expectCode, resp.StatusCode)
				}
			case err := <-serverError:
				t.Fatalf("Unexpected server error: %v", err)
			}
		})
	}
}

func TestRunFailure(t *testing.T) {
	tests := []struct {
		name       string
		configData string
	}{
		{
			name:       "Error - Invalid YAML Format",
			configData: `invalid_yaml: :::`,
		},
		{
			name: "Error - Missing Required Fields",
			configData: `
appName: "TestApp"
`,
		},
		{
			name: "Error - Invalid Port",
			configData: `
appName: "TestApp"
port: "invalid_port"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := createTempConfig(t, tt.configData)
			defer os.Remove(configPath)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if _, err := run(ctx, configPath); err == nil {
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
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			err := execute()
			if (err != nil) != tt.wantErr {
				t.Errorf("Unexpected error state: got %v, wantErr %v", err, tt.wantErr)
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
