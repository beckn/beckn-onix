package encrypter

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// Helper function to generate a test X25519 key pair.
func generateTestKeyPair(t *testing.T) (string, string) {
	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	publicKeyBytes := privateKey.PublicKey().Bytes()
	// Encode public and private key to base64.
	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyBytes)
	privateKeyBase64 := base64.StdEncoding.EncodeToString(privateKey.Bytes())

	return publicKeyBase64, privateKeyBase64
}

// TestEncryptSuccess tests successful encryption scenarios.
func TestEncryptSuccess(t *testing.T) {
	publicKey, privateKey := generateTestKeyPair(t)

	tests := []struct {
		name    string
		data    string
		pubKey  string
		privKey string
	}{
		{
			name:    "Valid short message",
			data:    "Hello, World!",
			pubKey:  publicKey,
			privKey: privateKey,
		},
		{
			name:    "Valid JSON message",
			data:    `{"key":"value"}`,
			pubKey:  publicKey,
			privKey: privateKey,
		},
		{
			name:    "Valid empty message",
			data:    "",
			pubKey:  publicKey,
			privKey: privateKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter := &Encrypter{}
			encrypted, err := encrypter.Encrypt(context.Background(), tt.data, tt.privKey, tt.pubKey)
			if err != nil {
				t.Errorf("Encrypt() expected no error, but got: %v", err)
			}

			// Verify the encrypted data is valid base64.
			_, err = base64.StdEncoding.DecodeString(encrypted)
			if err != nil {
				t.Errorf("Encrypt() output is not valid base64: %v", err)
			}

			// Since we can't decrypt without the ephemeral private key,
			// we can only verify that encryption doesn't return empty data.
			if encrypted == "" {
				t.Error("Encrypt() returned empty string")
			}

			// Verify the output is different from input (basic encryption check).
			if encrypted == tt.data {
				t.Error("Encrypt() output matches input, suggesting no encryption occurred")
			}

		})
	}
}

// TestEncryptFailure tests encryption failure scenarios.
func TestEncryptFailure(t *testing.T) {
	// Generate a valid key pair for testing.
	publicKey, privateKey := generateTestKeyPair(t)

	tests := []struct {
		name          string
		data          string
		publicKey     string
		privKey       string
		wantErr       bool
		errorContains string
	}{
		{
			name:          "Invalid public key format",
			data:          "test data",
			publicKey:     "invalid-base64!@#$",
			privKey:       privateKey,
			wantErr:       true,
			errorContains: "invalid public key",
		},
		{
			name:          "Invalid key bytes(public key)",
			data:          "test data",
			publicKey:     base64.StdEncoding.EncodeToString([]byte("invalid-key-bytes")),
			privKey:       privateKey,
			wantErr:       true,
			errorContains: "failed to create public key",
		},
		{
			name:          "Invalid key bytes(private key)",
			data:          "test data",
			publicKey:     publicKey,
			privKey:       base64.StdEncoding.EncodeToString([]byte("invalid-key-bytes")),
			wantErr:       true,
			errorContains: "failed to create private key",
		},
		{
			name:          "Empty public key",
			data:          "test data",
			publicKey:     "",
			privKey:       privateKey,
			wantErr:       true,
			errorContains: "invalid public key",
		},
		{
			name:          "Too short key",
			data:          "test data",
			publicKey:     base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}),
			privKey:       privateKey,
			wantErr:       true,
			errorContains: "failed to create public key",
		},
		{
			name:          "Invalid private key",
			data:          "test data",
			publicKey:     publicKey,
			privKey:       "invalid-base64!@#$",
			wantErr:       true,
			errorContains: "invalid private key",
		},
		{
			name:          "Empty private key",
			data:          "test data",
			publicKey:     publicKey,
			privKey:       "",
			wantErr:       true,
			errorContains: "invalid private key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter := &Encrypter{}
			_, err := encrypter.Encrypt(context.Background(), tt.data, tt.privKey, tt.publicKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("Encrypt() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.errorContains) {
				t.Errorf("Encrypt() error = %v, want error containing %q", err, tt.errorContains)
			}
		})
	}
}

// TestNew tests the creation of new Encrypter instances.
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		config  *Config
		wantErr bool
	}{
		{
			name:    "Valid config",
			ctx:     context.Background(),
			config:  &Config{},
			wantErr: false,
		},
		{
			name:    "Nil config",
			ctx:     context.Background(),
			config:  nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter, _, err := New(tt.ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && encrypter == nil {
				t.Error("New() returned nil encrypter")
			}
		})
	}
}
