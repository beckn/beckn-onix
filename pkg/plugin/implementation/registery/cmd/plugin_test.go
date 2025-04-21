package main

import (
	"context"
	"testing"
)

func TestRegistryLookupProviderSuccess(t *testing.T) {
	ctx := context.Background()
	config := map[string]string{
		"registeryURL": "http://example.com",
		"retryMax":     "5",
	}

	provider := registryLookupProvider{}
	client, cleanup, err := provider.New(ctx, config)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if client == nil {
		t.Errorf("Expected non-nil client but got nil")
	}
	if cleanup != nil {
		t.Errorf("Expected nil cleanup function but got non-nil")
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
				t.Fatal("Expected error but got none")
			}
			if err.Error() != tt.expectedErr {
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

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]string
		expectErr   bool
		expectedCfg *Config
	}{
		{
			name: "Valid config with retryMax",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"retryMax":     "5",
			},
			expectErr: false,
			expectedCfg: &Config{
				RegistryURL: "http://example.com",
				RetryMax:    5,
			},
		},
		{
			name: "Valid config with missing retryMax (defaults to 0)",
			input: map[string]string{
				"registeryURL": "http://example.com",
			},
			expectErr: false,
			expectedCfg: &Config{
				RegistryURL: "http://example.com",
				RetryMax:    0,
			},
		},
		{
			name: "Valid config with invalid retryMax (defaults to 0)",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"retryMax":     "abc",
			},
			expectErr: false,
			expectedCfg: &Config{
				RegistryURL: "http://example.com",
				RetryMax:    0,
			},
		},
		{
			name: "Missing registeryURL",
			input: map[string]string{
				"retryMax": "5",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseConfig(tt.input)

			if tt.expectErr {
				if err == nil {
					t.Fatal("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if cfg.RegistryURL != tt.expectedCfg.RegistryURL || cfg.RetryMax != tt.expectedCfg.RetryMax {
				t.Errorf("Expected config %+v, got %+v", tt.expectedCfg, cfg)
			}
		})
	}
}
