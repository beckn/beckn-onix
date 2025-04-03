package signer

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// generateTestKeys generates a test private and public key pair in base64 encoding.
func generateTestKeys() (string, string) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	return base64.StdEncoding.EncodeToString(privateKey.Seed()), base64.StdEncoding.EncodeToString(publicKey)
}

// TestSignSuccess tests the Sign method with valid inputs to ensure it produces a valid signature.
func TestSignSuccess(t *testing.T) {
	privateKey, _ := generateTestKeys()
	config := Config{}
	signer, close, _ := New(context.Background(), &config)

	successTests := []struct {
		name       string
		payload    []byte
		privateKey string
		createdAt  int64
		expiresAt  int64
	}{
		{
			name:       "Valid Signing",
			payload:    []byte("test payload"),
			privateKey: privateKey,
			createdAt:  time.Now().Unix(),
			expiresAt:  time.Now().Unix() + 3600,
		},
	}

	for _, tt := range successTests {
		t.Run(tt.name, func(t *testing.T) {
			signature, err := signer.Sign(context.Background(), tt.payload, tt.privateKey, tt.createdAt, tt.expiresAt)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if len(signature) == 0 {
				t.Errorf("expected a non-empty signature, but got empty")
			}
			if close != nil {
				if err := close(); err != nil {
					t.Fatalf("Cleanup function returned an error: %v", err)
				}
			}
		})
	}
}

// TestSignFailure tests the Sign method with invalid inputs to ensure proper error handling.
func TestSignFailure(t *testing.T) {
	config := Config{}
	signer, close, _ := New(context.Background(), &config)

	failureTests := []struct {
		name            string
		payload         []byte
		privateKey      string
		createdAt       int64
		expiresAt       int64
		expectErrString string
	}{
		{
			name:            "Invalid Private Key",
			payload:         []byte("test payload"),
			privateKey:      "invalid_key",
			createdAt:       time.Now().Unix(),
			expiresAt:       time.Now().Unix() + 3600,
			expectErrString: "error decoding private key",
		},
		{
			name:            "Short Private Key",
			payload:         []byte("test payload"),
			privateKey:      base64.StdEncoding.EncodeToString([]byte("short_key")),
			createdAt:       time.Now().Unix(),
			expiresAt:       time.Now().Unix() + 3600,
			expectErrString: "invalid seed length",
		},
	}

	for _, tt := range failureTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := signer.Sign(context.Background(), tt.payload, tt.privateKey, tt.createdAt, tt.expiresAt)
			if err == nil {
				t.Errorf("expected error but got none")
			} else if !strings.Contains(err.Error(), tt.expectErrString) {
				t.Errorf("expected error message to contain %q, got %v", tt.expectErrString, err)
			}
			if close != nil {
				if err := close(); err != nil {
					t.Fatalf("Cleanup function returned an error: %v", err)
				}
			}
		})
	}
}
