package signer

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

// generateTestKeys generates a test private and public key pair in base64 encoding.
func generateTestKeys() (string, string) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	return base64.StdEncoding.EncodeToString(privateKey), base64.StdEncoding.EncodeToString(publicKey)
}

// TestCreateSigningStringSuccess tests the creation of a signing string with valid inputs.
func TestCreateSigningStringSuccess(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600
	body := []byte("Test Payload")

	signString, err := hash(body, createdAt, expiresAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(signString, "(created):") {
		t.Errorf("expected signing string to contain '(created):', got %s", signString)
	}
	if !strings.Contains(signString, "(expires):") {
		t.Errorf("expected signing string to contain '(expires):', got %s", signString)
	}
	if !strings.Contains(signString, "digest: BLAKE-512=") {
		t.Errorf("expected signing string to contain 'digest: BLAKE-512=', got %s", signString)
	}

}

// TestCreateSigningStringHashError tests the creation of a signing string with a nil body.
func TestCreateSigningStringHashError(t *testing.T) {
	_, _ = hash(nil, time.Now().Unix(), time.Now().Unix()+3600)
}

// TestSignDataSuccess tests the signing of data with a valid private key.
func TestSignDataSuccess(t *testing.T) {
	privateKey, _ := generateTestKeys()
	signString := []byte("test signing string")
	signature, err := generateSignature(signString, privateKey)

	if err != nil {
		t.Fatalf("unexpected error: %v", err) // Fails the test immediately if an error occurs
	}
	if len(signature) == 0 {
		t.Errorf("expected a non-empty signature, but got empty")
	}
}

// TestSignDataInvalidPrivateKey tests signing data with an invalid private key.
func TestSignDataInvalidPrivateKey(t *testing.T) {
	signString := []byte("test signing string")
	_, err := generateSignature(signString, "invalid_key")
	if err == nil {
		t.Errorf("expected an error, but got nil")
	}

}

// TestSignSuccess tests the Sign function using a valid private key.
func TestSignSuccess(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := time.Now().Unix() + 3600
	privateKey, _ := generateTestKeys()
	config := Config{}
	signer, err := New(context.Background(), &config)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	signature, err := signer.Sign(context.Background(), []byte("test payload"), privateKey, createdAt, expiresAt)
	if err != nil {
		t.Fatalf("unexpected error during signing: %v", err)
	}
	if len(signature) == 0 {
		t.Errorf("expected a non-empty signature, but got empty")
	}
}

// TestSignInvalidPrivateKey tests the Sign function with an invalid private key.
func TestSignInvalidPrivateKey(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := time.Now().Unix() + 3600
	config := Config{}
	signer, err := New(context.Background(), &config)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	_, err = signer.Sign(context.Background(), []byte("test payload"), "invalid_key", createdAt, expiresAt)
	if err == nil {
		t.Errorf("expected an error due to invalid private key, but got nil")
	}
}

// TestCreateSigningStringEmptyPayload tests creating a signing string with an empty payload.
func TestCreateSigningStringEmptyPayload(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600

	signString, err := hash([]byte{}, createdAt, expiresAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(signString, "(created):") {
		t.Errorf("expected signing string to contain '(created):', got %s", signString)
	}
	if !strings.Contains(signString, "(expires):") {
		t.Errorf("expected signing string to contain '(expires):', got %s", signString)
	}
	if !strings.Contains(signString, "digest: BLAKE-512=") {
		t.Errorf("expected signing string to contain 'digest: BLAKE-512=', got %s", signString)
	}
}

// TestCreateSigningStringLargePayload tests creating a signing string with a large payload.
func TestCreateSigningStringLargePayload(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600
	largePayload := make([]byte, 10*1024*1024) // 10MB payload

	signString, err := hash(largePayload, createdAt, expiresAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(signString, "(created):") {
		t.Errorf("expected signing string to contain '(created):', got %s", signString)
	}
	if !strings.Contains(signString, "(expires):") {
		t.Errorf("expected signing string to contain '(expires):', got %s", signString)
	}
	if !strings.Contains(signString, "digest: BLAKE-512=") {
		t.Errorf("expected signing string to contain 'digest: BLAKE-512=', got %s", signString)
	}
}

// TestCreateSigningStringZeroTimestamps tests creating a signing string with zero timestamps.
func TestCreateSigningStringZeroTimestamps(t *testing.T) {
	signString, err := hash([]byte("test"), 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(signString, "(created): 0") {
		t.Errorf("expected signing string to contain '(created): 0', got: %s", signString)
	}

	if !strings.Contains(signString, "(expires): 0") {
		t.Errorf("expected signing string to contain '(expires): 0', got: %s", signString)
	}

	if !strings.Contains(signString, "digest: BLAKE-512=") {
		t.Errorf("expected signing string to contain 'digest: BLAKE-512=', got: %s", signString)
	}
}

// TestCreateSigningStringExpiresBeforeCreated tests a signing string where expiration is before creation.
func TestCreateSigningStringExpiresBeforeCreated(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt - 3600 // Expiration before creation

	signString, err := hash([]byte("test"), createdAt, expiresAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(signString, fmt.Sprintf("(created): %d", createdAt)) {
		t.Errorf("expected signing string to contain '(created): %d', got %s", createdAt, signString)
	}
	if !strings.Contains(signString, fmt.Sprintf("(expires): %d", expiresAt)) {
		t.Errorf("expected signing string to contain '(expires): %d', got %s", expiresAt, signString)
	}
}

// TestCreateSigningStringMaxIntTimestamp tests a signing string with the maximum int64 timestamp.
func TestCreateSigningStringMaxIntTimestamp(t *testing.T) {
	createdAt := int64(9223372036854775807) // Max int64
	expiresAt := createdAt

	signString, err := hash([]byte("test"), createdAt, expiresAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(signString, fmt.Sprintf("(created): %d", createdAt)) {
		t.Errorf("expected signing string to contain '(created): %d', got %s", createdAt, signString)
	}
	if !strings.Contains(signString, fmt.Sprintf("(expires): %d", expiresAt)) {
		t.Errorf("expected signing string to contain '(expires): %d', got %s", expiresAt, signString)
	}
}

// TestSignDataInvalidPrivateKeyLength tests signing data with a private key that is too short.
func TestSignDataInvalidPrivateKeyLength(t *testing.T) {
	signingString := []byte("test signing string")
	invalidPrivateKey := base64.StdEncoding.EncodeToString([]byte("short_key")) // Too short

	_, err := generateSignature(signingString, invalidPrivateKey)

	if err == nil {
		t.Fatal("expected an error, but got nil")
	}
	if !strings.Contains(err.Error(), "invalid private key length") {
		t.Errorf("expected error message to contain 'invalid private key length', got %v", err)
	}
}

// TestClose verifies that the Close method of Impl returns nil without errors.
func TestClose(t *testing.T) {
	s := &Signer{}

	err := s.Close()

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}
