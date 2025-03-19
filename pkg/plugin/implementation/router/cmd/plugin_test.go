package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestConfig creates a temporary directory and writes a sample routing rules file.
func setupTestConfig(t *testing.T) string {
	t.Helper()

	// Create a temporary directory for the routing rules
	configDir, err := os.MkdirTemp("", "routing_rules")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Define sample routing rules
	rulesContent := `
routing_rules:
  - domain: "ONDC:TRV11"
    version: "2.0.0"
    routing_type: "url"
    target:
      url: "https://services-backend/trv/v1"
    endpoints:
      - select
      - init
      - confirm
      - status

  - domain: "ONDC:TRV11"
    version: "2.0.0"
    routing_type: "msgq"
    target:
      topic_id: "trv_topic_id1"
    endpoints:
      - search
`

	// Write the routing rules to a file
	rulesFilePath := filepath.Join(configDir, "routing_rules.yaml")
	if err := os.WriteFile(rulesFilePath, []byte(rulesContent), 0644); err != nil {
		t.Fatalf("Failed to write routing rules file: %v", err)
	}

	return rulesFilePath
}

// TestRouterProvider_Success tests the RouterProvider implementation for success cases.
func TestRouterProvider_Success(t *testing.T) {
	rulesFilePath := setupTestConfig(t)
	defer os.RemoveAll(filepath.Dir(rulesFilePath))

	// Define test cases
	tests := []struct {
		name   string
		ctx    context.Context
		config map[string]string
	}{
		{
			name: "Valid configuration",
			ctx:  context.Background(),
			config: map[string]string{
				"routing_config": rulesFilePath,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := RouterProvider{}
			router, closeFunc, err := provider.New(tt.ctx, tt.config)

			// Ensure no error occurred
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Ensure the router and close function are not nil
			if router == nil {
				t.Error("expected a non-nil Router instance, got nil")
			}
			if closeFunc == nil {
				t.Error("expected a non-nil close function, got nil")
			}

			// Test the close function
			if err := closeFunc(); err != nil {
				t.Errorf("close function returned an error: %v", err)
			}
		})
	}
}

// TestRouterProvider_Failure tests the RouterProvider implementation for failure cases.
func TestRouterProvider_Failure(t *testing.T) {
	rulesFilePath := setupTestConfig(t)
	defer os.RemoveAll(filepath.Dir(rulesFilePath))

	// Define test cases
	tests := []struct {
		name          string
		ctx           context.Context
		config        map[string]string
		expectedError string
	}{
		{
			name: "Empty routing config path",
			ctx:  context.Background(),
			config: map[string]string{
				"routing_config": "",
			},
			expectedError: "failed to load routing rules: routing_config path is empty",
		},
		{
			name:          "Missing routing config key",
			ctx:           context.Background(),
			config:        map[string]string{},
			expectedError: "routing_config is required in the configuration",
		},
		{
			name:          "Nil context",
			ctx:           nil,
			config:        map[string]string{"routing_config": rulesFilePath},
			expectedError: "context cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := RouterProvider{}
			_, _, err := provider.New(tt.ctx, tt.config)

			// Check for expected error
			if err == nil || !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("expected error %q, got %v", tt.expectedError, err)
			}
		})
	}
}
