package signer

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"
)

// generateTestKeys generates a test private and public key pair in base64 encoding.
func generateTestKeys() (string, string) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	return base64.StdEncoding.EncodeToString(privateKey), base64.StdEncoding.EncodeToString(publicKey)
}

// TestSignSuccess tests the Sign method with valid inputs to ensure it produces a valid signature.
func TestSignSuccess(t *testing.T) {
	privateKey, _ := generateTestKeys()
	config := Config{}
	signer, _ := New(context.Background(), &config)

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
		})
	}
}

// TestSignFailure tests the Sign method with invalid inputs to ensure proper error handling.
func TestSignFailure(t *testing.T) {
	config := Config{}
	signer, _ := New(context.Background(), &config)

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
			expectErrString: "invalid private key length",
		},
	}

	for _, tt := range failureTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := signer.Sign(context.Background(), tt.payload, tt.privateKey, tt.createdAt, tt.expiresAt)
			if err == nil {
				t.Errorf("expected error but got none")
			} else if !contains(err.Error(), tt.expectErrString) {
				t.Errorf("expected error message to contain %q, got %v", tt.expectErrString, err)
			}
		})
	}
}

// TestSignerClose verifies that the Close method does not return an error.
func TestSignerClose(t *testing.T) {
	s := &Signer{}
	err := s.Close()
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// Helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

// Alternative to strings.Contains (avoiding import).
func stringContains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
