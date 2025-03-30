package main

import (
	"context"
	"testing"
)

// TestVerifierProviderSuccess tests successful creation of a verifier.
func TestVerifierProviderSuccess(t *testing.T) {
	provider := provider{}

	tests := []struct {
		name   string
		ctx    context.Context
		config map[string]string
	}{
		{
			name:   "Successful creation",
			ctx:    context.Background(),
			config: map[string]string{},
		},
		{
			name:   "Nil context",
			ctx:    context.TODO(),
			config: map[string]string{},
		},
		{
			name:   "Empty config",
			ctx:    context.Background(),
			config: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier, close, err := provider.New(tt.ctx, tt.config)

			if err != nil {
				t.Fatalf("Expected no error, but got: %v", err)
			}
			if verifier == nil {
				t.Fatal("Expected verifier instance to be non-nil")
			}
			if close != nil {
				if err := close(); err != nil {
					t.Fatalf("Test %q failed: cleanup function returned an error: %v", tt.name, err)
				}
			}
		})
	}
}

// TestVerifierProviderFailure tests cases where verifier creation should fail.
func TestVerifierProviderFailure(t *testing.T) {
	provider := provider{}

	tests := []struct {
		name    string
		ctx     context.Context
		config  map[string]string
		wantErr bool
	}{
		{
			name:    "Nil context failure",
			ctx:     nil,
			config:  map[string]string{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifierInstance, close, err := provider.New(tt.ctx, tt.config)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Expected error: %v, but got: %v", tt.wantErr, err)
			}
			if verifierInstance != nil {
				t.Fatal("Expected verifier instance to be nil")
			}
			if close != nil {
				if err := close(); err != nil {
					t.Fatalf("Test %q failed: cleanup function returned an error: %v", tt.name, err)
				}
			}

		})
	}
}
