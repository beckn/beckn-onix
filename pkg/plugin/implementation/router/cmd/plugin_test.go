package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setupTestConfig creates a temporary directory and writes a sample routing rules file.
func setupTestConfig(t *testing.T) string {
	t.Helper()

	// Get project root (assuming testData is in project root)
	_, filename, _, _ := runtime.Caller(0)              // Path to plugin_test.go
	projectRoot := filepath.Dir(filepath.Dir(filename)) // Move up from cmd/
	yamlPath := filepath.Join(projectRoot, "testData", "bap_receiver.yaml")

	// Copy to temp file (to test file loading logic)
	tempDir := t.TempDir()
	tempPath := filepath.Join(tempDir, "routingRules.yaml")
	content, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}
	if err := os.WriteFile(tempPath, content, 0644); err != nil {
		t.Fatalf("Failed to create temp config: %v", err)
	}

	return tempPath
}

// TestRouterProviderSuccess tests successful router creation.
func TestRouterProviderSuccess(t *testing.T) {
	rulesFilePath := setupTestConfig(t)
	defer os.RemoveAll(filepath.Dir(rulesFilePath))

	provider := RouterProvider{}
	router, _, err := provider.New(context.Background(), map[string]string{
		"routingConfig": rulesFilePath,
	})

	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	if router == nil {
		t.Error("New() returned nil router, want non-nil")
	}
}

// TestRouterProviderFailure tests the RouterProvider implementation for failure cases.
func TestRouterProviderFailure(t *testing.T) {
	rulesFilePath := setupTestConfig(t)
	defer os.RemoveAll(filepath.Dir(rulesFilePath))

	// Define test cases
	tests := []struct {
		name    string
		ctx     context.Context
		config  map[string]string
		wantErr string
	}{
		{
			name: "Empty routing config path",
			ctx:  context.Background(),
			config: map[string]string{
				"routingConfig": "",
			},
			wantErr: "failed to load routing rules: routingConfig path is empty",
		},
		{
			name:    "Missing routing config key",
			ctx:     context.Background(),
			config:  map[string]string{},
			wantErr: "routingConfig is required in the configuration",
		},
		{
			name:    "Nil context",
			ctx:     nil,
			config:  map[string]string{"routingConfig": rulesFilePath},
			wantErr: "context cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := RouterProvider{}
			_, _, err := provider.New(tt.ctx, tt.config)

			// Check for expected error
			if err == nil {
				t.Fatalf("New(%v, %v) = nil error, want error containing %q", tt.ctx, tt.config, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New(%v, %v) = %v, want error containing %q", tt.ctx, tt.config, err, tt.wantErr)
			}
		})
	}
}
