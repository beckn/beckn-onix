package main

import (
	"context"
	"strings"
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
			// Create provider and encrypter
			provider := EncrypterProvider{}
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

func TestEncrypterProviderFailure(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		config    map[string]string
		errSubstr string
	}{
		{
			name:      "Nil context",
			ctx:       nil,
			config:    map[string]string{},
			errSubstr: "context cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := EncrypterProvider{}
			encrypter, cleanup, err := provider.New(tt.ctx, tt.config)
			if err == nil {
				t.Error("EncrypterProvider.New() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("EncrypterProvider.New() error = %v, want error containing %q", err, tt.errSubstr)
			}
			if encrypter != nil {
				t.Error("EncrypterProvider.New() expected nil encrypter when error")
			}
			if cleanup != nil {
				if err := cleanup(); err != nil {
					t.Errorf("Cleanup() error = %v", err)
				}
			}
		})
	}
}
