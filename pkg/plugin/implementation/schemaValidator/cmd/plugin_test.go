package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestSchema creates a temporary directory and writes a sample schema file.
func setupTestSchema(t *testing.T) string {
	t.Helper()

	// Create a temporary directory for the schema
	schemaDir, err := os.MkdirTemp("", "schemas")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Create the directory structure for the schema file
	schemaFilePath := filepath.Join(schemaDir, "example", "1.0", "test_schema.json")
	if err := os.MkdirAll(filepath.Dir(schemaFilePath), 0755); err != nil {
		t.Fatalf("Failed to create schema directory structure: %v", err)
	}

	// Define a sample schema
	schemaContent := `{
		"type": "object",
		"properties": {
			"context": {
				"type": "object",
				"properties": {
					"domain": {"type": "string"},
					"version": {"type": "string"}
				},
				"required": ["domain", "version"]
			}
		},
		"required": ["context"]
	}`

	// Write the schema to the file
	if err := os.WriteFile(schemaFilePath, []byte(schemaContent), 0644); err != nil {
		t.Fatalf("Failed to write schema file: %v", err)
	}

	return schemaDir
}

// TestValidatorProviderSuccess tests successful ValidatorProvider implementation.
func TestValidatorProviderSuccess(t *testing.T) {
	schemaDir := setupTestSchema(t)
	defer os.RemoveAll(schemaDir)

	// Define test cases.
	tests := []struct {
		name          string
		ctx           context.Context
		config        map[string]string
		expectedError string
	}{
		{
			name:          "Valid schema directory",
			ctx:           context.Background(), // Valid context
			config:        map[string]string{"schema_dir": schemaDir},
			expectedError: "",
		},
	}

	// Test using table-driven tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vp := schemaValidatorProvider{}
			schemaValidator, close, err := vp.New(tt.ctx, tt.config)

			// Ensure no error occurred
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Ensure the schemaValidator is not nil
			if schemaValidator == nil {
				t.Error("expected a non-nil schemaValidator, got nil")
			}

			// Ensure the close function is not nil
			if close == nil {
				t.Error("expected a non-nil close function, got nil")
			}

			// Test the close function
			if err := close(); err != nil {
				t.Errorf("close function returned an error: %v", err)
			}
		})
	}
}

// TestValidatorProviderSuccess tests cases where ValidatorProvider creation should fail.
func TestValidatorProviderFailure(t *testing.T) {
	schemaDir := setupTestSchema(t)
	defer os.RemoveAll(schemaDir)

	// Define test cases.
	tests := []struct {
		name          string
		ctx           context.Context
		config        map[string]string
		expectedError string
	}{
		{
			name:          "Config is empty",
			ctx:           context.Background(),
			config:        map[string]string{},
			expectedError: "config must contain 'schema_dir'",
		},
		{
			name:          "schema_dir is empty",
			ctx:           context.Background(),
			config:        map[string]string{"schema_dir": ""},
			expectedError: "config must contain 'schema_dir'",
		},
		{
			name:          "Invalid schema directory",
			ctx:           context.Background(), // Valid context
			config:        map[string]string{"schema_dir": "/invalid/dir"},
			expectedError: "failed to initialise schemaValidator: schema directory does not exist: /invalid/dir",
		},
		{
			name:          "Nil context",
			ctx:           nil, // Nil context
			config:        map[string]string{"schema_dir": schemaDir},
			expectedError: "context cannot be nil",
		},
	}

	// Test using table-driven tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vp := schemaValidatorProvider{}
			_, _, err := vp.New(tt.ctx, tt.config)

			// Check for expected error
			if tt.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error %q, got %v", tt.expectedError, err)
				}
				return
			}

			// Ensure no error occurred
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}
