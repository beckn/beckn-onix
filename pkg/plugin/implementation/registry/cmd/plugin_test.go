package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestRegistryLookupProviderSuccess(t *testing.T) {
	ctx := context.Background()
	config := map[string]string{
		"registeryURL": "http://example.com",
		"lookupURL":    "http://example.com",
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
			config:      map[string]string{"registeryURL": "http://example.com", "lookupURL": "http://example.com"},
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

func TestParseConfigSuccess(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]string
		expectedCfg *Config
	}{
		{
			name: "Valid config with retryMax",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"lookupURL":    "http://example.com",
				"retryMax":     "5",
			},
			expectedCfg: &Config{
				RegistryURL:  "http://example.com",
				RetryMax:     5,
				LookupURL:    "http://example.com",
				RetryWaitMin: 1 * time.Second,
				RetryWaitMax: 5 * time.Second,
			},
		},
		{
			name: "Valid config with missing retryMax (defaults to 0)",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"lookupURL":    "http://example.com",
			},
			expectedCfg: &Config{
				RegistryURL:  "http://example.com",
				RetryMax:     3,
				LookupURL:    "http://example.com",
				RetryWaitMin: 1 * time.Second,
				RetryWaitMax: 5 * time.Second,
			},
		},
		{
			name: "Valid config with invalid retryMax (defaults to 0)",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"lookupURL":    "http://example.com",
				"retryMax":     "abc",
			},
			expectedCfg: &Config{
				RegistryURL:  "http://example.com",
				RetryMax:     0,
				LookupURL:    "http://example.com",
				RetryWaitMin: 1 * time.Second,
				RetryWaitMax: 5 * time.Second,
			},
		},
		{
			name: "Valid config with retryWaitMin and retryWaitMax",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"lookupURL":    "http://example.com",
				"retryWaitMin": "2s",
				"retryWaitMax": "4s",
			},
			expectedCfg: &Config{
				RegistryURL:  "http://example.com",
				RetryMax:     3,
				LookupURL:    "http://example.com",
				RetryWaitMin: 2 * time.Second,
				RetryWaitMax: 4 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseConfig(tt.input)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.expectedCfg, cfg); diff != "" {
				t.Errorf("Mismatch in config (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseConfigFailures(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]string
		expectedErr string
	}{
		{
			name: "missing registryURL",
			input: map[string]string{
				"lookupURL": "http://example.com",
			},
			expectedErr: "config must contain 'registeryURL'",
		},
		{
			name: "missing lookupURL",
			input: map[string]string{
				"registeryURL": "http://example.com",
			},
			expectedErr: "config must contain 'lookupURL'",
		},
		{
			name: "negative retryWaitMin",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"lookupURL":    "http://example.com",
				"retryWaitMin": "-1s",
			},
			expectedErr: "retryWaitMin must be a non-negative duration",
		},
		{
			name: "invalid retryWaitMin format",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"lookupURL":    "http://example.com",
				"retryWaitMin": "not-a-duration",
			},
			expectedErr: "retryWaitMin must be a non-negative duration",
		},
		{
			name: "invalid retryWaitMax format",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"lookupURL":    "http://example.com",
				"retryWaitMax": "not-a-duration",
			},
			expectedErr: "retryWaitMax must be a non-negative duration",
		},
		{
			name: "retryWaitMin > retryWaitMax",
			input: map[string]string{
				"registeryURL": "http://example.com",
				"lookupURL":    "http://example.com",
				"retryWaitMin": "10s",
				"retryWaitMax": "5s",
			},
			expectedErr: "retryWaitMin cannot be greater than retryWaitMax",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConfig(tt.input)
			if err == nil {
				t.Fatal("expected error but got none")
			}
			if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("expected error containing %q, got %q", tt.expectedErr, err.Error())
			}
		})
	}
}
