package main

import (
	"context"
	"testing"
)

func TestRegistryLookupProviderSuccess(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		config map[string]string
	}{
		{
			name:   "Valid configuration",
			ctx:    context.Background(),
			config: map[string]string{"registeryURL": "http://example.com", "retryMax": "5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := registryLookupProvider{}
			client, cleanup, err := provider.New(tt.ctx, tt.config)

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if client == nil {
				t.Errorf("Expected non-nil client but got nil")
			}
			if cleanup != nil {
				t.Errorf("Expected non-nil cleanup function but got nil")
			}
		})
	}
}

func TestRegistryLookupProviderFailure(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		config      map[string]string
		expectedErr string
	}{
		{
			name:        "Nil context",
			ctx:         nil,
			config:      map[string]string{"registeryURL": "http://example.com"},
			expectedErr: "context cannot be nil",
		},
		{
			name:        "Missing registeryURL",
			ctx:         context.Background(),
			config:      map[string]string{},
			expectedErr: "config must contain 'registeryURL'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := registryLookupProvider{}
			client, cleanup, err := provider.New(tt.ctx, tt.config)

			// Check for expected error
			if err == nil {
				t.Errorf("Expected error but got none")
			} else if err.Error() != tt.expectedErr {
				t.Fatalf("Expected error '%s', got '%s'", tt.expectedErr, err.Error())
			}
			if client != nil {
				t.Fatal("Expected client to be nil but got a non-nil client")
			}
			if cleanup != nil {
				t.Fatal("Expected cleanup function to be nil but got a non-nil function")
			}
		})
	}
}
