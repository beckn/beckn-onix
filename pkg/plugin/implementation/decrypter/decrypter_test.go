package decryption

import (
	"context"
	"crypto/aes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/zenazn/pkcs7pad"
)

// Helper function to generate valid test keys.
func generateTestKeys(t *testing.T) (privateKeyB64, publicKeyB64 string, sharedSecret []byte) {
	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	publicKey := privateKey.PublicKey()
	privateKeyB64 = base64.StdEncoding.EncodeToString(privateKey.Bytes())
	publicKeyB64 = base64.StdEncoding.EncodeToString(publicKey.Bytes())

	// Generate shared secret for encryption.
	secret, err := privateKey.ECDH(publicKey)
	if err != nil {
		t.Fatalf("Failed to generate shared secret: %v", err)
	}

	return privateKeyB64, publicKeyB64, secret
}

// Helper function to encrypt test data.
func encryptTestData(t *testing.T, data []byte, sharedSecret []byte) string {
	// Create AES cipher.
	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		t.Fatalf("Failed to create AES cipher: %v", err)
	}

	// Pad the data.
	paddedData := pkcs7pad.Pad(data, block.BlockSize())

	// Encrypt the data.
	ciphertext := make([]byte, len(paddedData))
	for i := 0; i < len(paddedData); i += block.BlockSize() {
		block.Encrypt(ciphertext[i:i+block.BlockSize()], paddedData[i:i+block.BlockSize()])
	}

	return base64.StdEncoding.EncodeToString(ciphertext)
}

// TestDecrypterSuccess tests successful decryption scenarios.
func TestDecrypterSuccess(t *testing.T) {
	privateKeyB64, publicKeyB64, sharedSecret := generateTestKeys(t)

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "Valid decryption with small data",
			data: []byte("test"),
		},
		{
			name: "Valid decryption with medium data",
			data: []byte("medium length test data that spans multiple blocks"),
		},
		{
			name: "Valid decryption with empty data",
			data: []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encrypt the test data.
			encryptedData := encryptTestData(t, tt.data, sharedSecret)

			decrypter, _, err := New(context.Background())
			if err != nil {
				t.Fatalf("Failed to create decrypter: %v", err)
			}

			result, err := decrypter.Decrypt(context.Background(), encryptedData, privateKeyB64, publicKeyB64)
			if err != nil {
				t.Errorf("Decrypt() error = %v", err)
			}

			if err == nil {
				if result != string(tt.data) {
					t.Errorf("Decrypt() = %v, want %v", result, string(tt.data))
				}
			}
		})
	}
}

// TestDecrypterFailure tests various failure scenarios.
func TestDecrypterFailure(t *testing.T) {
	privateKeyB64, publicKeyB64, _ := generateTestKeys(t)

	tests := []struct {
		name          string
		encryptedData string
		privateKey    string
		publicKey     string
		expectedErr   string
	}{
		{
			name:          "Invalid private key format",
			encryptedData: base64.StdEncoding.EncodeToString(make([]byte, 32)),
			privateKey:    "invalid-base64!@#$",
			publicKey:     publicKeyB64,
			expectedErr:   "invalid private key",
		},
		{
			name:          "Invalid public key format",
			encryptedData: base64.StdEncoding.EncodeToString(make([]byte, 32)),
			privateKey:    privateKeyB64,
			publicKey:     "invalid-base64!@#$",
			expectedErr:   "invalid public key",
		},
		{
			name:          "Invalid encrypted data format",
			encryptedData: "invalid-base64!@#$",
			privateKey:    privateKeyB64,
			publicKey:     publicKeyB64,
			expectedErr:   "failed to decode encrypted data",
		},
		{
			name:          "Empty private key",
			encryptedData: base64.StdEncoding.EncodeToString(make([]byte, 32)),
			privateKey:    "",
			publicKey:     publicKeyB64,
			expectedErr:   "invalid private key",
		},
		{
			name:          "Empty public key",
			encryptedData: base64.StdEncoding.EncodeToString(make([]byte, 32)),
			privateKey:    privateKeyB64,
			publicKey:     "",
			expectedErr:   "invalid public key",
		},
		{
			name:          "Invalid base64 data",
			encryptedData: "=invalid-base64", // Invalid encrypted data.
			privateKey:    privateKeyB64,
			publicKey:     publicKeyB64,
			expectedErr:   "failed to decode encrypted data",
		},
		{
			name:          "Invalid private key size",
			encryptedData: base64.StdEncoding.EncodeToString(make([]byte, 32)),
			privateKey:    base64.StdEncoding.EncodeToString([]byte("short")),
			publicKey:     publicKeyB64,
			expectedErr:   "failed to create private key",
		},
		{
			name:          "Invalid public key size",
			encryptedData: base64.StdEncoding.EncodeToString(make([]byte, 32)),
			privateKey:    privateKeyB64,
			publicKey:     base64.StdEncoding.EncodeToString([]byte("short")),
			expectedErr:   "failed to create public key",
		},
		{
			name:          "Invalid block size",
			encryptedData: base64.StdEncoding.EncodeToString([]byte("not-block-size")),
			privateKey:    privateKeyB64,
			publicKey:     publicKeyB64,
			expectedErr:   "ciphertext is not a multiple of the blocksize",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decrypter, _, err := New(context.Background())
			if err != nil {
				t.Fatalf("Failed to create decrypter: %v", err)
			}

			_, err = decrypter.Decrypt(context.Background(), tt.encryptedData, tt.privateKey, tt.publicKey)
			if err == nil {
				t.Error("Expected error but got none")
			}

			if err != nil {
				if !strings.Contains(err.Error(), tt.expectedErr) {
					t.Errorf("Expected error containing %q, got %q", tt.expectedErr, err.Error())
				}
			}
		})
	}
}

// TestNewDecrypter tests the creation of new Decrypter instances.
func TestNewDecrypter(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{
			name: "Valid context",
			ctx:  context.Background(),
		},
		{
			name: "Nil context",
			ctx:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decrypter, _, err := New(tt.ctx)
			if err != nil {
				t.Errorf("New() error = %v", err)
			}

			if err == nil {
				if decrypter == nil {
					t.Error("Expected non-nil decrypter")
				}
			}
		})
	}
}
