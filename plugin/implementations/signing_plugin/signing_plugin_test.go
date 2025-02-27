package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"
)

func TestCreateSigningString(t *testing.T) {
	payload := []byte("test payload")
	created := time.Now().Unix()
	expired := created + 3600

	signingString := createSigningString(payload, created, expired)
	if signingString == "" {
		t.Errorf("Signing string should not be empty")
	}
}

func TestSignerFunc(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	privateKeyBase64 := base64.StdEncoding.EncodeToString(privateKey)
	signingString := []byte("test signing string")

	signature, err := SignerFunc(signingString, privateKeyBase64)
	if err != nil {
		t.Errorf("SignerFunc failed: %v", err)
	}

	if len(signature) == 0 {
		t.Errorf("Signature should not be empty")
	}
}

func TestSignerFunc_InvalidPrivateKey(t *testing.T) {
	_, err := SignerFunc([]byte("test"), "invalid-key")
	if err == nil {
		t.Errorf("Expected error for invalid private key but got nil")
	}
}

func TestSignPayload(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	privateKeyBase64 := base64.StdEncoding.EncodeToString(privateKey)
	payload := []byte("test payload")
	created := time.Now().Unix()
	expired := created + 3600

	signature, err := SignPayload(payload, privateKeyBase64, created, expired)
	if err != nil {
		t.Errorf("SignPayload failed: %v", err)
	}

	if len(signature) == 0 {
		t.Errorf("Signature should not be empty")
	}
}

func TestSignPayload_EdgeCases(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	privateKeyBase64 := base64.StdEncoding.EncodeToString(privateKey)

	cases := []struct {
		name       string
		payload    []byte
		privateKey string
		expectErr  bool
	}{
		{"Valid Payload", []byte("test payload"), privateKeyBase64, false},
		{"Empty Payload", []byte(""), privateKeyBase64, false},
		{"Invalid Key", []byte("test payload"), "invalid-key", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := SignPayload(c.payload, c.privateKey, time.Now().Unix(), time.Now().Unix()+3600)
			if (err != nil) != c.expectErr {
				t.Errorf("Expected error: %v, got: %v", c.expectErr, err)
			}
		})
	}
}

func TestSigning_Sign(t *testing.T) {
	s := NewSigning()
	ctx := context.Background()
	payload := []byte("test payload")
	keyID := "test-key"

	signature, err := s.Sign(ctx, payload, keyID)
	if err != nil {
		t.Errorf("Sign function failed: %v", err)
	}

	if len(signature) == 0 {
		t.Errorf("Signature should not be empty")
	}
}

func TestSigning_Sign_ErrorCase(t *testing.T) {
	s := NewSigning()
	ctx := context.Background()
	payload := []byte("test payload")
	_, err := s.Sign(ctx, payload, "invalid-key")
	if err == nil {
		t.Errorf("Expected error but got nil")
	}
}

func TestSignerProvider_New(t *testing.T) {
	sp := &SignerProvider{}
	ctx := context.Background()
	config := make(map[string]string)

	signer, err := sp.New(ctx, config)
	if err != nil {
		t.Errorf("SignerProvider.New failed: %v", err)
	}

	if signer == nil {
		t.Errorf("Signer should not be nil")
	}
}
