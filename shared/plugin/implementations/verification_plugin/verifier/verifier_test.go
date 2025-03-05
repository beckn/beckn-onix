package verifier

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// generateTestKeyPair generates a new ED25519 key pair for testing.
func generateTestKeyPair() (string, string) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	return base64.StdEncoding.EncodeToString(privateKey), base64.StdEncoding.EncodeToString(publicKey)
}

// signTestData creates a valid signature for test cases.
func signTestData(privateKeyBase64 string, body []byte, createdAt, expiresAt int64) string {
	privateKeyBytes, _ := base64.StdEncoding.DecodeString(privateKeyBase64)
	privateKey := ed25519.PrivateKey(privateKeyBytes)

	signingString := createSigningString(body, createdAt, expiresAt)
	signature := ed25519.Sign(privateKey, []byte(signingString))

	return base64.StdEncoding.EncodeToString(signature)
}

// TestVerifySuccess ensures the verification works with a valid payload.
func TestVerifySuccess(t *testing.T) {
	privateKeyBase64, publicKeyBase64 := generateTestKeyPair()

	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600

	body := []byte("test payload")
	signature := signTestData(privateKeyBase64, body, createdAt, expiresAt)

	header := `Signature created="` +
		strconv.FormatInt(createdAt, 10) +
		`", expires="` + strconv.FormatInt(expiresAt, 10) +
		`", signature="` + signature + `"`

	verifier, _ := New(context.Background(), &Config{})

	valid, err := verifier.Verify(context.Background(), body, []byte(header), publicKeyBase64)

	assert.NoError(t, err, "Expected no error in verification")
	assert.True(t, valid, "Expected signature verification to succeed")
}

// TestVerify_MissingAuthHeader checks if missing Authorization header causes an error.
func TestVerifyMissingAuthHeader(t *testing.T) {
	verifier, _ := New(context.Background(), &Config{})

	valid, err := verifier.Verify(context.Background(), []byte("test payload"), []byte(""), "dummyPublicKey")
	assert.Error(t, err, "Expected error due to missing authorization header")
	assert.False(t, valid)
}

// TestVerify_MalformedHeader ensures that malformed headers return an error.
func TestVerifyMalformedHeader(t *testing.T) {
	verifier, _ := New(context.Background(), &Config{})

	header := `InvalidSignature created="wrong"`
	valid, err := verifier.Verify(context.Background(), []byte("test payload"), []byte(header), "dummyPublicKey")

	assert.Error(t, err, "Expected error due to malformed header")
	assert.False(t, valid)
}

// TestVerify_InvalidBase64Signature ensures that an invalid base64 signature is handled correctly.
func TestVerifyInvalidBase64Signature(t *testing.T) {
	_, publicKeyBase64 := generateTestKeyPair()

	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600

	header := `Signature created="` +
		strconv.FormatInt(createdAt, 10) +
		`", expires="` + strconv.FormatInt(expiresAt, 10) +
		`", signature="!!INVALIDBASE64!!"`

	verifier, _ := New(context.Background(), &Config{})

	valid, err := verifier.Verify(context.Background(), []byte("test payload"), []byte(header), publicKeyBase64)
	assert.Error(t, err, "Expected error due to invalid base64 signature")
	assert.False(t, valid)
}

// TestVerify_ExpiredSignature ensures expired timestamps are rejected.
func TestVerifyExpiredSignature(t *testing.T) {
	privateKeyBase64, publicKeyBase64 := generateTestKeyPair()

	createdAt := time.Now().Unix() - 7200 // 2 hours ago
	expiresAt := createdAt + 3600         // Expired an hour ago

	body := []byte("test payload")
	signature := signTestData(privateKeyBase64, body, createdAt, expiresAt)

	header := `Signature created="` +
		strconv.FormatInt(createdAt, 10) +
		`", expires="` + strconv.FormatInt(expiresAt, 10) +
		`", signature="` + signature + `"`

	verifier, _ := New(context.Background(), &Config{})

	valid, err := verifier.Verify(context.Background(), body, []byte(header), publicKeyBase64)
	assert.Error(t, err, "Expected error due to expired signature")
	assert.False(t, valid)
}

// TestVerify_FutureSignature ensures future timestamps are rejected.
func TestVerifyFutureSignature(t *testing.T) {
	privateKeyBase64, publicKeyBase64 := generateTestKeyPair()

	createdAt := time.Now().Unix() + 3600 // 1 hour in the future
	expiresAt := createdAt + 7200

	body := []byte("test payload")
	signature := signTestData(privateKeyBase64, body, createdAt, expiresAt)

	header := `Signature created="` +
		strconv.FormatInt(createdAt, 10) +
		`", expires="` + strconv.FormatInt(expiresAt, 10) +
		`", signature="` + signature + `"`

	verifier, _ := New(context.Background(), &Config{})

	valid, err := verifier.Verify(context.Background(), body, []byte(header), publicKeyBase64)
	assert.Error(t, err, "Expected error due to signature not being valid yet")
	assert.False(t, valid)
}

// TestVerify_InvalidPublicKey ensures that a wrong public key results in verification failure.
func TestVerifyInvalidPublicKey(t *testing.T) {
	privateKeyBase64, _ := generateTestKeyPair()
	_, wrongPublicKeyBase64 := generateTestKeyPair() // Generate a different key

	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600

	body := []byte("test payload")
	signature := signTestData(privateKeyBase64, body, createdAt, expiresAt)

	header := `Signature created="` +
		strconv.FormatInt(createdAt, 10) +
		`", expires="` + strconv.FormatInt(expiresAt, 10) +
		`", signature="` + signature + `"`

	verifier, _ := New(context.Background(), &Config{})

	valid, err := verifier.Verify(context.Background(), body, []byte(header), wrongPublicKeyBase64)
	assert.Error(t, err, "Expected error due to invalid public key")
	assert.False(t, valid)
}

// TestVerify_EmptyBody ensures empty payloads do not crash the verification.
func TestVerifyEmptyBody(t *testing.T) {
	privateKeyBase64, publicKeyBase64 := generateTestKeyPair()

	createdAt := time.Now().Unix()
	expiresAt := createdAt + 3600

	body := []byte("")
	signature := signTestData(privateKeyBase64, body, createdAt, expiresAt)

	header := `Signature created="` +
		strconv.FormatInt(createdAt, 10) +
		`", expires="` + strconv.FormatInt(expiresAt, 10) +
		`", signature="` + signature + `"`

	verifier, _ := New(context.Background(), &Config{})

	valid, err := verifier.Verify(context.Background(), body, []byte(header), publicKeyBase64)
	assert.NoError(t, err, "Expected no error for empty body verification")
	assert.True(t, valid, "Expected empty body verification to succeed")
}

// TestClose verifies that the Close method of Verifier returns nil without errors.
func TestClose(t *testing.T) {
	v := &Verifier{}
	err := v.Close()
	assert.NoError(t, err, "Expected Close method to return nil without errors")
}
