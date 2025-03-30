package main

import (
	"context"
	"testing"
)

func TestEncrypterProviderSuccess(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		config map[string]string
	}{
		{
			name:   "Valid empty config",
			ctx:    context.Background(),
			config: map[string]string{},
		},
		{
			name: "Valid config with algorithm",
			ctx:  context.Background(),
			config: map[string]string{
				"algorithm": "AES",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create provider and encrypter.
			provider := encrypterProvider{}
			encrypter, cleanup, err := provider.New(tt.ctx, tt.config)
			if err != nil {
				t.Fatalf("EncrypterProvider.New() error = %v", err)
			}
			if encrypter == nil {
				t.Fatal("EncrypterProvider.New() returned nil encrypter")
			}
			defer func() {
				if cleanup != nil {
					if err := cleanup(); err != nil {
						t.Errorf("Cleanup() error = %v", err)
					}
				}
			}()

		})
	}
}
