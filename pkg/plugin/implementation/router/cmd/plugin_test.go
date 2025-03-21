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
	configDir, err := os.MkdirTemp("", "routingRules")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Define sample routing rules
	rulesContent := `
routingRules:
  - domain: "ONDC:TRV11"
    version: "2.0.0"
    routingType: "url"
    target:
      url: "https://services-backend/trv/v1"
    endpoints:
      - select
      - init
      - confirm
      - status

  - domain: "ONDC:TRV11"
    version: "2.0.0"
    routingType: "msgq"
    target:
      topic_id: "trv_topic_id1"
    endpoints:
      - search
`

	// Write the routing rules to a file
	rulesFilePath := filepath.Join(configDir, "routingRules.yaml")
	if err := os.WriteFile(rulesFilePath, []byte(rulesContent), 0644); err != nil {
		t.Fatalf("Failed to write routing rules file: %v", err)
	}

	return rulesFilePath
}

// TestRouterProviderSuccess tests the RouterProvider implementation for success cases.
func TestRouterProviderSuccess(t *testing.T) {
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
				"routingConfig": rulesFilePath,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := RouterProvider{}
			router, _, err := provider.New(tt.ctx, tt.config)

			// Ensure no error occurred
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Ensure the router and close function are not nil
			if router == nil {
				t.Error("expected a non-nil Router instance, got nil")
			}
		})
	}
}

// TestRouterProviderFailure tests the RouterProvider implementation for failure cases.
func TestRouterProviderFailure(t *testing.T) {
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
				"routingConfig": "",
			},
			expectedError: "failed to load routing rules: routingConfig path is empty",
		},
		{
			name:          "Missing routing config key",
			ctx:           context.Background(),
			config:        map[string]string{},
			expectedError: "routingConfig is required in the configuration",
		},
		{
			name:          "Nil context",
			ctx:           nil,
			config:        map[string]string{"routingConfig": rulesFilePath},
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
