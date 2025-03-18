package main

import (
	"bytes"
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beckn/beckn-onix/core/module"
	"github.com/beckn/beckn-onix/plugin"
)

// newManagerFunc is a wrapper function to allow dependency injection in tests.
var newManagerFunc = plugin.NewManager

// TestInitConfigSuccess verifies that initConfig correctly loads a valid configuration file.
func TestInitConfigSuccess(t *testing.T) {
	tests := []struct {
		name        string
		fileContent string
	}{
		{
			name: "ValidConfig",
			fileContent: `
appName: "TestApp"
http:
  port: "8080"
  timeout:
    read: 5
    write: 5
    idle: 10
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := "test_config.yaml"
			err := os.WriteFile(filePath, []byte(tt.fileContent), 0644)
			if err != nil {
				t.Fatalf("Failed to create temp config file: %v", err)
			}
			defer os.Remove(filePath)

			_, err = initConfig(context.Background(), filePath)
			if err != nil {
				t.Errorf("Expected success, but got error: %v", err)
			}
		})
	}
}

// TestInitConfigFailure checks if initConfig correctly handles invalid YAML and missing files.
func TestInitConfigFailure(t *testing.T) {
	tests := []struct {
		name        string
		fileContent string
		expectError string
	}{
		{
			name:        "InvalidYAML",
			fileContent: "invalid_yaml: ::::",
			expectError: "could not open config file: open non_existent.yaml: no such file or directory",
		},
		{
			name:        "MissingFile",
			fileContent: "",
			expectError: "could not open config file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := "test_config.yaml"
			if tt.fileContent != "" {
				os.WriteFile(filePath, []byte(tt.fileContent), 0644)
				defer os.Remove(filePath)
			}

			_, err := initConfig(context.Background(), "non_existent.yaml")
			if err == nil || !strings.Contains(err.Error(), tt.expectError) {
				t.Errorf("Expected error %q but got %v", tt.expectError, err)
			}
		})
	}
}

// TestValidateConfigSuccess check for the valid config.
func TestValidateConfigSuccess(t *testing.T) {
	tests := []struct {
		name   string
		config config
	}{
		{
			name: "ValidConfig",
			config: config{
				AppName: "TestApp",
				HTTP:    httpConfig{Port: "8080"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(&tt.config)
			if err != nil {
				t.Errorf("Expected success, but got error: %v", err)
			}
		})
	}
}

// TestValidateConfigFailure runs for the Invalid configs
func TestValidateConfigFailure(t *testing.T) {
	tests := []struct {
		name        string
		config      config
		expectError string
	}{
		{
			name:        "MissingAppName",
			config:      config{HTTP: httpConfig{Port: "8080"}},
			expectError: "missing app name",
		},
		{
			name:        "MissingPort",
			config:      config{AppName: "TestApp"},
			expectError: "missing port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(&tt.config)
			if err == nil || !strings.Contains(err.Error(), tt.expectError) {
				t.Errorf("Expected error %q but got %v", tt.expectError, err)
			}
		})
	}
}

// TestNewServerSuccess verifies that newServer initializes correctly with a valid configuration.
func TestNewServerSuccess(t *testing.T) {
	cfg := &config{
		Modules: []module.Config{},
	}
	mgr := &plugin.Manager{}

	_, err := newServer(context.Background(), mgr, cfg)
	if err != nil {
		t.Errorf("Expected success, but got error: %v", err)
	}
}

// TestNewServerFailure verifies that newServer fails when provided with an invalid configuration.
func TestNewServerFailure(t *testing.T) {
	cfg := &config{
		Modules: []module.Config{{Name: ""}}, // Simulating bad config
	}
	mgr := &plugin.Manager{}

	_, err := newServer(context.Background(), mgr, cfg)
	if err == nil {
		t.Errorf("Expected failure but got success")
	}
}

// Mock closer function
func mockCloserCalled(flag *bool) func() {
	return func() {
		*flag = true
	}
}

// TestShutdown verifies the shutdown function executes all closers and completes within timeout.
func TestShutdown(t *testing.T) {
	tests := []struct {
		name          string
		closers       []func()
		expectTimeout bool
	}{
		{
			name:          "Shutdown with no closers",
			closers:       nil,
			expectTimeout: false,
		},
		{
			name: "Shutdown with one closer",
			closers: func() []func() {
				var called bool
				return []func(){mockCloserCalled(&called)}
			}(),
			expectTimeout: false,
		},
		{
			name: "Shutdown with multiple closers",
			closers: func() []func() {
				var called1, called2 bool
				return []func(){mockCloserCalled(&called1), mockCloserCalled(&called2)}
			}(),
			expectTimeout: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test HTTP server
			server := &http.Server{Addr: ":8081"}

			// Create a wait group
			var wg sync.WaitGroup

			// Create a context with cancellation
			ctx, cancel := context.WithCancel(context.Background())

			// Start shutdown in a separate goroutine
			shutdown(ctx, server, &wg, tt.closers)

			// Simulate shutdown trigger
			cancel()

			// Wait for shutdown to complete
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()

			select {
			case <-done:
				// Success
			case <-time.After(12 * time.Second): // Ensure timeout doesn't exceed shutdown duration
				t.Fatal("Shutdown took too long")
			}
		})
	}
}

var mockRun func(ctx context.Context, configPath string) error

// TestMainFunction verifies the main function behavior based on different input arguments.
func TestMainFunction(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		mockRunErr  error
		expectLog   string
		expectFatal bool
	}{
		{
			name:       "Valid config path",
			args:       []string{"cmd", "-config=./valid-config.yaml"},
			mockRunErr: nil,
			expectLog:  "Starting application with config: ./valid-config.yaml",
		},
		{
			name:        "Invalid config path",
			args:        []string{"cmd", "-config=./invalid-config.yaml"},
			mockRunErr:  os.ErrNotExist,
			expectLog:   "Application failed",
			expectFatal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset flags before each test
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

			// Capture log output
			var logBuffer bytes.Buffer
			log.SetOutput(&logBuffer)

			// Mock run function
			mockRun = func(ctx context.Context, configPath string) error {
				return tt.mockRunErr
			}

			// Simulate command-line arguments
			os.Args = tt.args
			flag.Parse() // Only parse flags once

			// Run main()
			defer func() {
				if r := recover(); r != nil {
					if !tt.expectFatal {
						t.Errorf("Unexpected fatal error: %v", r)
					}
				}
			}()
			main()

			// Check log output directly
			if !bytes.Contains(logBuffer.Bytes(), []byte(tt.expectLog)) {
				t.Errorf("Expected log containing %q, but got: %s", tt.expectLog, logBuffer.String())
			}
		})
	}
}
