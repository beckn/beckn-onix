package main

import (
	"context"
	"testing"
)

func TestDecrypterProviderSuccess(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		config      map[string]string
		wantDecrypt bool
		wantCleanup bool
	}{
		{
			name:        "Valid context with empty config",
			ctx:         context.Background(),
			config:      map[string]string{},
			wantDecrypt: true,
			wantCleanup: true,
		},
		{
			name:        "Valid context with non-empty config",
			ctx:         context.Background(),
			config:      map[string]string{"key": "value"},
			wantDecrypt: true,
			wantCleanup: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := DecrypterProvider{}
			decrypter, cleanup, err := provider.New(tt.ctx, tt.config)

			// Check error.
			if err != nil {
				t.Errorf("New() error = %v, want no error", err)
			}

			// Check decrypter.
			if tt.wantDecrypt && decrypter == nil {
				t.Error("New() decrypter is nil, want non-nil")
			}

			// Check cleanup function.
			if tt.wantCleanup && cleanup == nil {
				t.Error("New() cleanup is nil, want non-nil")
			}

			// Test cleanup function if it exists.
			if cleanup != nil {
				if err := cleanup(); err != nil {
					t.Errorf("cleanup() error = %v", err)
				}
			}
		})
	}
}

func TestDecrypterProviderFailure(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		config    map[string]string
		wantError string
	}{
		{
			name:      "Nil context",
			ctx:       nil,
			config:    map[string]string{},
			wantError: "context cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := DecrypterProvider{}
			decrypter, cleanup, err := provider.New(tt.ctx, tt.config)

			// Check error.
			if err == nil {
				t.Error("New() error = nil, want error")
			}

			if err.Error() != tt.wantError {
				t.Errorf("New() error = %v, want %v", err, tt.wantError)
			}

			// Check that decrypter and cleanup are nil on error.
			if decrypter != nil {
				t.Error("New() decrypter is not nil, want nil")
			}

			if cleanup != nil {
				t.Error("New() cleanup is not nil, want nil")
			}
		})
	}
}
