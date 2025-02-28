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

func generateTestKeys() (string, string) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	return base64.StdEncoding.EncodeToString(privateKey), base64.StdEncoding.EncodeToString(publicKey)
}

func TestCreateSigningString_Success(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600
	body := []byte("test payload")

	signString, err := createSigningString(body, createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, "(created):")
	assert.Contains(t, signString, "(expires):")
	assert.Contains(t, signString, "digest: BLAKE-512=")
}

func TestCreateSigningString_HashError(t *testing.T) {
	_, err := createSigningString(nil, time.Now().Unix(), time.Now().Unix()+3600)
	assert.NoError(t, err)
}

func TestSignData_Success(t *testing.T) {
	privateKey, _ := generateTestKeys()
	signString := []byte("test signing string")
	signature, err := signData(signString, privateKey)

	assert.NoError(t, err)
	assert.NotEmpty(t, signature)
}

func TestSignData_InvalidPrivateKey(t *testing.T) {
	signString := []byte("test signing string")
	_, err := signData(signString, "invalid_key")
	assert.Error(t, err)
}

func TestSign_Success(t *testing.T) {
	privateKey, _ := generateTestKeys()
	config := SigningConfig{TTL: 3600}
	signer, _ := NewSigner(context.Background(), config)
	signature, err := signer.Sign(context.Background(), []byte("test payload"), privateKey)

	assert.NoError(t, err)
	assert.NotEmpty(t, signature)
}

func TestSign_InvalidPrivateKey(t *testing.T) {
	config := SigningConfig{TTL: 3600}
	signer, _ := NewSigner(context.Background(), config)
	_, err := signer.Sign(context.Background(), []byte("test payload"), "invalid_key")
	assert.Error(t, err)
}

func TestCreateSigningString_EmptyPayload(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600

	signString, err := createSigningString([]byte{}, createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, "(created):")
	assert.Contains(t, signString, "(expires):")
	assert.Contains(t, signString, "digest: BLAKE-512=")
}

func TestCreateSigningString_LargePayload(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600
	largePayload := make([]byte, 10*1024*1024) // 10MB payload

	signString, err := createSigningString(largePayload, createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, "(created):")
	assert.Contains(t, signString, "(expires):")
	assert.Contains(t, signString, "digest: BLAKE-512=")
}

func TestCreateSigningString_ZeroTimestamps(t *testing.T) {
	signString, err := createSigningString([]byte("test"), 0, 0)
	assert.NoError(t, err)
	assert.Contains(t, signString, "(created): 0")
	assert.Contains(t, signString, "(expires): 0")
	assert.Contains(t, signString, "digest: BLAKE-512=")
}

func TestCreateSigningString_ExpiresBeforeCreated(t *testing.T) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt - 3600 // Expiration before creation

	signString, err := createSigningString([]byte("test"), createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, fmt.Sprintf("(created): %d", createdAt))
	assert.Contains(t, signString, fmt.Sprintf("(expires): %d", expiresAt))
}

func TestCreateSigningString_MaxIntTimestamp(t *testing.T) {
	createdAt := int64(9223372036854775807) // Max int64
	expiresAt := createdAt

	signString, err := createSigningString([]byte("test"), createdAt, expiresAt)
	assert.NoError(t, err)
	assert.Contains(t, signString, fmt.Sprintf("(created): %d", createdAt))
	assert.Contains(t, signString, fmt.Sprintf("(expires): %d", expiresAt))
}

func TestSignData_InvalidPrivateKeyLength(t *testing.T) {
	signingString := []byte("test signing string")
	invalidPrivateKey := base64.StdEncoding.EncodeToString([]byte("short_key")) // Too short

	_, err := signData(signingString, invalidPrivateKey)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid private key length")
}
