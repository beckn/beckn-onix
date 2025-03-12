package main

import (
	"context"
	"testing"
)

// TestSignerProviderSuccess verifies successful scenarios for SignerProvider.
func TestSignerProviderSuccess(t *testing.T) {
	provider := SignerProvider{}

	successTests := []struct {
		name   string
		ctx    context.Context
		config map[string]string
	}{
		{
			name:   "Valid Config",
			ctx:    context.Background(),
			config: map[string]string{},
		},
		{
			name:   "Unexpected Config Key",
			ctx:    context.Background(),
			config: map[string]string{"unexpected_key": "some_value"},
		},
		{
			name:   "Empty Config",
			ctx:    context.Background(),
			config: map[string]string{},
		},
		{
			name:   "Config with empty TTL",
			ctx:    context.Background(),
			config: map[string]string{"ttl": ""},
		},
		{
			name:   "Config with negative TTL",
			ctx:    context.Background(),
			config: map[string]string{"ttl": "-100"},
		},
		{
			name:   "Config with non-numeric TTL",
			ctx:    context.Background(),
			config: map[string]string{"ttl": "not_a_number"},
		},
	}

	for _, tt := range successTests {
		t.Run(tt.name, func(t *testing.T) {
			signer, close, err := provider.New(tt.ctx, tt.config)

			if err != nil {
				t.Fatalf("Test %q failed: expected no error, but got: %v", tt.name, err)
			}
			if signer == nil {
				t.Fatalf("Test %q failed: signer instance should not be nil", tt.name)
			}
			if close != nil {
				if err := close(); err != nil {
					t.Fatalf("Cleanup function returned an error: %v", err)
				}
			}
		})
	}
}

// TestSignerProviderFailure verifies failure scenarios for SignerProvider.
func TestSignerProviderFailure(t *testing.T) {
	provider := SignerProvider{}

	failureTests := []struct {
		name    string
		ctx     context.Context
		config  map[string]string
		wantErr bool
	}{
		{
			name:    "Nil Context",
			ctx:     nil,
			config:  map[string]string{},
			wantErr: true,
		},
	}

	for _, tt := range failureTests {
		t.Run(tt.name, func(t *testing.T) {
			signerInstance, close, err := provider.New(tt.ctx, tt.config)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Test %q failed: expected error: %v, got: %v", tt.name, tt.wantErr, err)
			}
			if signerInstance != nil {
				t.Fatalf("Test %q failed: expected signer instance to be nil", tt.name)
			}
			if close != nil {
				t.Fatalf("Test %q failed: expected cleanup function to be nil", tt.name)
			}
		})
	}
}
