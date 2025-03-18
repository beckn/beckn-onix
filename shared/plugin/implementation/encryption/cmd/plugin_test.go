package main

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func TestEncrypterProviderSuccess(t *testing.T) {
	// Generate a test key pair first to use across all tests
	publicKey := generateTestPublicKey(t)

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
			encrypter, err := provider.New(tt.ctx, tt.config)
			if err != nil {
				t.Fatalf("EncrypterProvider.New() error = %v", err)
			}
			if encrypter == nil {
				t.Fatal("EncrypterProvider.New() returned nil encrypter")
			}

			// Test basic encryption
			testData := "test message"
			encrypted, err := encrypter.Encrypt(tt.ctx, testData, publicKey)
			if err != nil {
				t.Errorf("Encrypt() error = %v", err)
			}
			if encrypted == "" {
				t.Error("Encrypt() returned empty string")
			}

			if err := encrypter.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
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
			encrypter, err := provider.New(tt.ctx, tt.config)
			if err == nil {
				t.Error("EncrypterProvider.New() expected error, got nil")
				return
			}
			if !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("EncrypterProvider.New() error = %v, want error containing %q", err, tt.errSubstr)
			}
			if encrypter != nil {
				t.Error("EncrypterProvider.New() expected nil encrypter when error")
			}
		})
	}
}

func TestProviderImplementation(t *testing.T) {
	if Provider == nil {
		t.Fatal("Provider is nil")
	}
}

func TestEncrypterIntegration(t *testing.T) {
	provider := EncrypterProvider{}
	ctx := context.Background()

	// Generate test key pair first
	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}
	publicKey := base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes())

	// Create encrypter
	encrypter, err := provider.New(ctx, map[string]string{})
	if err != nil {
		t.Fatalf("Failed to create encrypter: %v", err)
	}

	tests := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{
			name:    "Encrypt small message",
			data:    "Hello, World!",
			wantErr: false,
		},
		{
			name:    "Encrypt empty message",
			data:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := encrypter.Encrypt(ctx, tt.data, publicKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("Encrypt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result == "" {
				t.Error("Encrypt() returned empty string")
			}
		})
	}
}

// Helper function to generate a test public key
func generateTestPublicKey(t *testing.T) string {
	t.Helper()
	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes())
}
