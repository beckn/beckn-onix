package signvalidator

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"strconv"
	"testing"
	"time"
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

	signingString := hash(body, createdAt, expiresAt)
	signature := ed25519.Sign(privateKey, []byte(signingString))

	return base64.StdEncoding.EncodeToString(signature)
}

// TestVerifySuccessCases tests all valid signature verification cases.
func TestVerifySuccess(t *testing.T) {
	privateKeyBase64, publicKeyBase64 := generateTestKeyPair()

	tests := []struct {
		name      string
		body      []byte
		createdAt int64
		expiresAt int64
	}{
		{
			name:      "Valid Signature",
			body:      []byte("Test Payload"),
			createdAt: time.Now().Unix(),
			expiresAt: time.Now().Unix() + 3600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signature := signTestData(privateKeyBase64, tt.body, tt.createdAt, tt.expiresAt)
			header := `Signature created="` + strconv.FormatInt(tt.createdAt, 10) +
				`", expires="` + strconv.FormatInt(tt.expiresAt, 10) +
				`", signature="` + signature + `"`

			verifier, close, _ := New(context.Background(), &Config{})
			err := verifier.Validate(context.Background(), tt.body, header, publicKeyBase64)

			if err != nil {
				t.Fatalf("Expected no error, but got: %v", err)
			}
			if close != nil {
				if err := close(); err != nil {
					t.Fatalf("Test %q failed: cleanup function returned an error: %v", tt.name, err)
				}
			}
		})
	}
}

// TestVerifyFailureCases tests all invalid signature verification cases.
func TestVerifyFailure(t *testing.T) {
	privateKeyBase64, publicKeyBase64 := generateTestKeyPair()
	_, wrongPublicKeyBase64 := generateTestKeyPair()

	tests := []struct {
		name   string
		body   []byte
		header string
		pubKey string
	}{
		{
			name:   "Missing Authorization Header",
			body:   []byte("Test Payload"),
			header: "",
			pubKey: publicKeyBase64,
		},
		{
			name:   "Malformed Header",
			body:   []byte("Test Payload"),
			header: `InvalidSignature created="wrong"`,
			pubKey: publicKeyBase64,
		},
		{
			name: "Invalid Base64 Signature",
			body: []byte("Test Payload"),
			header: `Signature created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+3600, 10) +
				`", signature="!!INVALIDBASE64!!"`,
			pubKey: publicKeyBase64,
		},
		{
			name: "Expired Signature",
			body: []byte("Test Payload"),
			header: `Signature created="` + strconv.FormatInt(time.Now().Unix()-7200, 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()-3600, 10) +
				`", signature="` + signTestData(privateKeyBase64, []byte("Test Payload"), time.Now().Unix()-7200, time.Now().Unix()-3600) + `"`,
			pubKey: publicKeyBase64,
		},
		{
			name: "Invalid Public Key",
			body: []byte("Test Payload"),
			header: `Signature created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+3600, 10) +
				`", signature="` + signTestData(privateKeyBase64, []byte("Test Payload"), time.Now().Unix(), time.Now().Unix()+3600) + `"`,
			pubKey: wrongPublicKeyBase64,
		},
		{
			name: "Invalid Expires Timestamp",
			body: []byte("Test Payload"),
			header: `Signature created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="invalid_timestamp"`,
			pubKey: publicKeyBase64,
		},
		{
			name: "Signature Missing in Headers",
			body: []byte("Test Payload"),
			header: `Signature created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+3600, 10) + `"`,
			pubKey: publicKeyBase64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier, close, _ := New(context.Background(), &Config{})
			err := verifier.Validate(context.Background(), tt.body, tt.header, tt.pubKey)

			if err == nil {
				t.Fatal("Expected an error but got none")
			}
			if close != nil {
				if err := close(); err != nil {
					t.Fatalf("Test %q failed: cleanup function returned an error: %v", tt.name, err)
				}
			}
		})
	}
}
