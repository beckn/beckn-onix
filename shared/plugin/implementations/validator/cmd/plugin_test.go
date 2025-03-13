package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"testing"

	"beckn-onix/shared/plugin/definition"
)

// MockValidator is a mock implementation of the Validator interface for testing.
type MockValidator struct{}

func (m *MockValidator) Validate(ctx context.Context, u *url.URL, data []byte) (bool, definition.Error) {
	return true, definition.Error{}
}

// Mock New function for testing
func MockNew(ctx context.Context, config map[string]string) (map[string]definition.Validator, error) {
	// If the config has the error flag set to "true", return an error
	if config["error"] == "true" {
		return nil, errors.New("mock error")
	}

	// If schema_dir is set, print it out for debugging purposes
	if schemaDir, ok := config["schema_dir"]; ok {
		// You could add more logic to handle the schema_dir, for now we just print it
		fmt.Println("Using schema directory:", schemaDir)
	}

	// Return a map of mock validators
	return map[string]definition.Validator{
		"validator1": &MockValidator{},
		"validator2": &MockValidator{},
	}, nil
}

// New method for ValidatorProvider, uses MockNew for creating mock validators
func New(ctx context.Context, config map[string]string) (map[string]definition.Validator, definition.Error) {
	validators, err := MockNew(ctx, config)
	if err != nil {
		return nil, definition.Error{Message: err.Error()}
	}
	return validators, definition.Error{}
}

func TestValidatorProvider(t *testing.T) {
	// Create a temporary directory for the schema
	schemaDir, err := ioutil.TempDir("", "schemas")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(schemaDir)

	// Create a temporary JSON schema file
	schemaFile := fmt.Sprintf("%s/test_schema.json", schemaDir)
	schemaContent := `{"type": "object", "properties": {"name": {"type": "string"}}}`
	if err := ioutil.WriteFile(schemaFile, []byte(schemaContent), 0644); err != nil {
		t.Fatalf("Failed to write schema file: %v", err)
	}

	// Define test cases
	tests := []struct {
		name          string
		config        map[string]string
		expectedError string
		expectedCount int
	}{
		{
			name:          "Valid schema directory",
			config:        map[string]string{"schema_dir": schemaDir}, // Use schemaDir instead of tempDir
			expectedError: "",
			expectedCount: 2, // Expecting 2 mock validators
		},
		{
			name:          "Invalid schema directory",
			config:        map[string]string{"schema_dir": "/invalid/dir"},
			expectedError: "failed to initialise validators: {/invalid/dir schema directory does not exist}",
			expectedCount: 0,
		},
	}

	// Test using table-driven tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vp := ValidatorProvider{}
			validators, err := vp.New(context.Background(), tt.config)

			// Check for expected error
			if tt.expectedError != "" {
				if err == (definition.Error{}) || err.Message != tt.expectedError {
					t.Errorf("expected error %q, got %v", tt.expectedError, err)
				}
				return
			}

			// Check for expected number of validators
			if len(validators) != tt.expectedCount {
				t.Errorf("expected %d validators, got %d", tt.expectedCount, len(validators))
			}
		})
	}
}
