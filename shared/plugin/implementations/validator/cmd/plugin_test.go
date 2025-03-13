package main

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"beckn-onix/shared/plugin/definition"
)

// MockValidator is a mock implementation of the Validator interface for testing.
type MockValidator struct{}

func (m *MockValidator) Validate(ctx context.Context, u *url.URL, data []byte) (bool, definition.Error) {
	return true, (definition.Error{})
}

// Mock New function for testing
func MockNew(ctx context.Context, config map[string]string) (map[string]definition.Validator, error) {
	if config["error"] == "true" {
		return nil, errors.New("mock error")
	}

	return map[string]definition.Validator{
		"validator1": &MockValidator{},
		"validator2": &MockValidator{},
	}, nil
}

func TestValidatorProvider_New(t *testing.T) {
	tests := []struct {
		name          string
		config        map[string]string
		expectedError string
		expectedCount int
	}{
		{
			name:          "Successful initialization",
			config:        map[string]string{"some_key": "some_value"},
			expectedError: "",
			expectedCount: 2, // Expecting 2 mock validators
		},
		{
			name:          "Error during initialization (mock error)",
			config:        map[string]string{"error": "true"},
			expectedError: "mock error",
			expectedCount: 0,
		},
		{
			name:          "Empty config map",
			config:        map[string]string{},
			expectedError: "",
			expectedCount: 2, // Expecting 2 mock validators
		},
		{
			name:          "Non-empty config with invalid key",
			config:        map[string]string{"invalid_key": "invalid_value"},
			expectedError: "",
			expectedCount: 2, // Expecting 2 mock validators
		},
	}

	// Using the mock New function directly for testing
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a ValidatorProvider with the mock New function
			vp := &ValidatorProvider{}
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
