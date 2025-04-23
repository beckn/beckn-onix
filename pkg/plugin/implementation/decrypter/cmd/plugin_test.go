package main

import (
	"context"
	"testing"
)

func TestDecrypterProviderSuccess(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		config map[string]string
	}{
		{
			name:   "Valid context with empty config",
			ctx:    context.Background(),
			config: map[string]string{},
		},
		{
			name:   "Valid context with non-empty config",
			ctx:    context.Background(),
			config: map[string]string{"key": "value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := decrypterProvider{}
			decrypter, cleanup, err := provider.New(tt.ctx, tt.config)

			// Check error.
			if err != nil {
				t.Errorf("New() error = %v, want no error", err)
			}

			// Check decrypter.
			if decrypter == nil {
				t.Error("New() decrypter is nil, want non-nil")
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
