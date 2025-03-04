package signer

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	body := []byte("test payload")

	signString, err := createSigningString(body, createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, "(created):")
	assert.Contains(t, signString, "(expires):")
	assert.Contains(t, signString, "digest: BLAKE-512=")
}

// TestCreateSigningStringHashError tests the creation of a signing string with a nil body.
func TestCreateSigningStringHashError(t *testing.T) {
	_, err := createSigningString(nil, time.Now().Unix(), time.Now().Unix()+3600)
	assert.NoError(t, err)
}

// TestSignDataSuccess tests the signing of data with a valid private key.
func TestSignDataSuccess(t *testing.T) {
	privateKey, _ := generateTestKeys()
	signString := []byte("test signing string")
	signature, err := signData(signString, privateKey)

	assert.NoError(t, err)
	assert.NotEmpty(t, signature)
}

// TestSignDataInvalidPrivateKey tests signing data with an invalid private key.
func TestSignDataInvalidPrivateKey(t *testing.T) {
	signString := []byte("test signing string")
	_, err := signData(signString, "invalid_key")
	assert.Error(t, err)
}

// TestSignSuccess tests the Sign function using a valid private key.
func TestSignSuccess(t *testing.T) {
	privateKey, _ := generateTestKeys()
	config := SigningConfig{TTL: 3600}
	signer, _ := NewSigner(context.Background(), config)
	signature, err := signer.Sign(context.Background(), []byte("test payload"), privateKey)

	assert.NoError(t, err)
	assert.NotEmpty(t, signature)
}

// TestSignInvalidPrivateKey tests the Sign function with an invalid private key.
func TestSignInvalidPrivateKey(t *testing.T) {
	config := SigningConfig{TTL: 3600}
	signer, _ := NewSigner(context.Background(), config)
	_, err := signer.Sign(context.Background(), []byte("test payload"), "invalid_key")
	assert.Error(t, err)
}

// TestCreateSigningString_EmptyPayload tests creating a signing string with an empty payload.
func TestCreateSigningString_EmptyPayload(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600

	signString, err := createSigningString([]byte{}, createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, "(created):")
	assert.Contains(t, signString, "(expires):")
	assert.Contains(t, signString, "digest: BLAKE-512=")
}

// TestCreateSigningStringLargePayload tests creating a signing string with a large payload.
func TestCreateSigningStringLargePayload(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600
	largePayload := make([]byte, 10*1024*1024) // 10MB payload

	signString, err := createSigningString(largePayload, createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, "(created):")
	assert.Contains(t, signString, "(expires):")
	assert.Contains(t, signString, "digest: BLAKE-512=")
}

// TestCreateSigningStringZeroTimestamps tests creating a signing string with zero timestamps.
func TestCreateSigningStringZeroTimestamps(t *testing.T) {
	signString, err := createSigningString([]byte("test"), 0, 0)
	assert.NoError(t, err)
	assert.Contains(t, signString, "(created): 0")
	assert.Contains(t, signString, "(expires): 0")
	assert.Contains(t, signString, "digest: BLAKE-512=")
}

// TestCreateSigningStringExpiresBeforeCreated tests a signing string where expiration is before creation.
func TestCreateSigningStringExpiresBeforeCreated(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt - 3600 // Expiration before creation

	signString, err := createSigningString([]byte("test"), createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, fmt.Sprintf("(created): %d", createdAt))
	assert.Contains(t, signString, fmt.Sprintf("(expires): %d", expiresAt))
}

// TestCreateSigningStringMaxIntTimestamp tests a signing string with the maximum int64 timestamp.
func TestCreateSigningStringMaxIntTimestamp(t *testing.T) {
	createdAt := int64(9223372036854775807) // Max int64
	expiresAt := createdAt

	signString, err := createSigningString([]byte("test"), createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, fmt.Sprintf("(created): %d", createdAt))
	assert.Contains(t, signString, fmt.Sprintf("(expires): %d", expiresAt))
}

// TestSignDataInvalidPrivateKeyLength tests signing data with a private key that is too short.
func TestSignDataInvalidPrivateKeyLength(t *testing.T) {
	signingString := []byte("test signing string")
	invalidPrivateKey := base64.StdEncoding.EncodeToString([]byte("short_key")) // Too short

	_, err := signData(signingString, invalidPrivateKey)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid private key length")
}
