package encryption

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
)

// Helper function to generate a test RSA key pair
func generateTestKeyPair(t *testing.T) (string, *rsa.PrivateKey) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	// Convert public key to PEM format
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("Failed to marshal public key: %v", err)
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	})

	// Encode to base64
	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyPEM)
	return publicKeyBase64, privateKey
}

// TestEncryptSuccess tests successful encryption scenarios
func TestEncryptSuccess(t *testing.T) {
	publicKey, privateKey := generateTestKeyPair(t)

	tests := []struct {
		name    string
		data    string
		pubKey  string
		wantErr bool
	}{
		{
			name:    "Valid short message",
			data:    "Hello, World!",
			pubKey:  publicKey,
			wantErr: false,
		},
		{
			name:    "Valid JSON message",
			data:    `{"key":"value"}`,
			pubKey:  publicKey,
			wantErr: false,
		},
		{
			name:    "Valid empty message",
			data:    "",
			pubKey:  publicKey,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter := &Encrypter{config: &Config{}}
			encrypted, err := encrypter.Encrypt(context.Background(), tt.data, tt.pubKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("Encrypt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Decode the base64 encrypted data
				encryptedBytes, err := base64.StdEncoding.DecodeString(encrypted)
				if err != nil {
					t.Errorf("Failed to decode encrypted data: %v", err)
					return
				}

				// Decrypt the message
				hash := sha256.New()
				decrypted, err := rsa.DecryptOAEP(hash, rand.Reader, privateKey, encryptedBytes, nil)
				if err != nil {
					t.Errorf("Failed to decrypt message: %v", err)
					return
				}

				if string(decrypted) != tt.data {
					t.Errorf("Decrypted data = %v, want %v", string(decrypted), tt.data)
				}
			}
		})
	}
}

// TestEncryptFailure tests encryption failure scenarios
func TestEncryptFailure(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		pubKey    string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "Invalid public key format",
			data:      "test data",
			pubKey:    "invalid-base64",
			wantErr:   true,
			errSubstr: "failed to decode public key",
		},
		{
			name:      "Invalid PEM format",
			data:      "test data",
			pubKey:    base64.StdEncoding.EncodeToString([]byte("not-a-pem-key")),
			wantErr:   true,
			errSubstr: "failed to decode PEM block",
		},
		{
			name:      "Empty public key",
			data:      "test data",
			pubKey:    "",
			wantErr:   true,
			errSubstr: "failed to decode PEM block",
		},
		{
			name:      "Non-RSA public key",
			data:      "test data",
			pubKey:    base64.StdEncoding.EncodeToString([]byte("-----BEGIN PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBAI0GUzz\n-----END PUBLIC KEY-----")),
			wantErr:   true,
			errSubstr: "failed to decode PEM block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter := &Encrypter{config: &Config{}}
			_, err := encrypter.Encrypt(context.Background(), tt.data, tt.pubKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("Encrypt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("Encrypt() error = %v, want error containing %q", err, tt.errSubstr)
			}
		})
	}
}

// TestNew tests the creation of new Encrypter instances
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name:    "Create with nil config",
			config:  nil,
			wantErr: false,
		},
		{
			name:    "Create with empty config",
			config:  &Config{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(context.Background(), tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got == nil {
				t.Error("New() returned nil, want non-nil Encrypter")
			}
		})
	}
}

// TestClose tests the Close method
func TestClose(t *testing.T) {
	encrypter := &Encrypter{config: &Config{}}
	if err := encrypter.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}
