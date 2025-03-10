package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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


func TestInitConfigSuccess(t *testing.T) {
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
port: 9091
`,
			expectError: false,
			expected: &config{
				AppName: "networkSideHandler",
				Port:    9091,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempFilePath := "../../config/networkSideHandler-config.yaml"
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
				
				if diff := cmp.Diff(tc.expected, cfg); diff != "" {
					t.Errorf("Config mismatch (-expected +actual):\n%s", diff)
				}
			}
		})
	}
}

func TestInitConfigFailure(t *testing.T) {
	tests := []struct {
		name        string
		configData  string
		expectError bool
		expected    *config
	}{
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
			tempFilePath := "../../config/networkSideHandler-config.yaml"
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

				if diff := cmp.Diff(tc.expected, cfg); diff != "" {
					t.Errorf("Config mismatch (-expected +actual):\n%s", diff)
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
                if err := run(ctx, configPath); err != nil {
                    serverError <- err
                }
            }()

            // Check when server is available
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

            // Wait for server to be ready or error
            select {
            case <-serverReady:
                // Server is ready, make request
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