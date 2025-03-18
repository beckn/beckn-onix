package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
)

func TestEncrypterProviderSuccess(t *testing.T) {
	// Generate a test key pair first
	publicKey, _, err := generateTestKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate test key pair: %v", err)
	}

	publicKeyPEM := exportPublicKeyToPEM(publicKey)
	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyPEM)

	tests := []struct {
		name   string
		ctx    context.Context
		config map[string]string
	}{
		{
			name: "Valid configuration with context",
			ctx:  context.Background(),
			config: map[string]string{
				"publicKey": publicKeyBase64,
				"algorithm": "RSA",
			},
		},
		{
			name: "Minimal configuration with context",
			ctx:  context.Background(),
			config: map[string]string{
				"publicKey": publicKeyBase64,
			},
		},
	}

	provider := EncrypterProvider{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter, err := provider.New(tt.ctx, tt.config)
			if err != nil {
				t.Errorf("EncrypterProvider.New() error = %v", err)
				return
			}
			if encrypter == nil {
				t.Error("EncrypterProvider.New() returned nil encrypter")
			}

			testData := "test message"
			encrypted, err := encrypter.Encrypt(context.Background(), testData, publicKeyBase64)
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
		config    map[string]string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "Invalid public key",
			config:    map[string]string{}, // Missing publicKey
			wantErr:   true,
			errSubstr: "publicKey is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := EncrypterProvider{}
			_, err := provider.New(context.Background(), tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("EncrypterProvider.New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("EncrypterProvider.New() error = %v, want error containing %q", err, tt.errSubstr)
			}
		})
	}
}

// Helper function to generate RSA key pair for testing
func generateTestKeyPair() (*rsa.PublicKey, *rsa.PrivateKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	return &privateKey.PublicKey, privateKey, nil
}

// Helper function to export public key to PEM format
func exportPublicKeyToPEM(publicKey *rsa.PublicKey) []byte {
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	})

	return publicKeyPEM
}

// TestEncrypterIntegration tests the full encryption flow
func TestEncrypterIntegration(t *testing.T) {
	provider := EncrypterProvider{}
	ctx := context.Background()

	// Generate test key pair
	publicKey, _, err := generateTestKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate test key pair: %v", err)
	}

	// Export and encode public key
	publicKeyPEM := exportPublicKeyToPEM(publicKey)
	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyPEM)

	// Create encrypter with valid config
	config := map[string]string{
		"publicKey": publicKeyBase64,
	}

	encrypter, err := provider.New(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create encrypter: %v", err)
	}

	// Test cases
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
		{
			name:    "Encrypt JSON message",
			data:    `{"key": "value"}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := encrypter.Encrypt(ctx, tt.data, publicKeyBase64)
			if (err != nil) != tt.wantErr {
				t.Errorf("Encrypt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && encrypted == "" {
				t.Error("Encrypt() returned empty string")
			}
		})
	}

	// Test cleanup
	if err := encrypter.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
