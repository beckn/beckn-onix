package encryption

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// Helper function to generate a test X25519 key pair
func generateTestKeyPair(t *testing.T) (string, []byte) {
	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	publicKey := privateKey.PublicKey()
	publicKeyBytes := publicKey.Bytes()

	// Encode public key to base64
	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyBytes)
	return publicKeyBase64, privateKey.Bytes()
}

// TestEncryptSuccess tests successful encryption scenarios
func TestEncryptSuccess(t *testing.T) {
	publicKey, _ := generateTestKeyPair(t)

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
				// Verify the encrypted data is valid base64
				_, err := base64.StdEncoding.DecodeString(encrypted)
				if err != nil {
					t.Errorf("Encrypt() output is not valid base64: %v", err)
				}

				// Since we can't decrypt without the ephemeral private key,
				// we can only verify that encryption doesn't return empty data
				if encrypted == "" {
					t.Error("Encrypt() returned empty string")
				}

				// Verify the output is different from input (basic encryption check)
				if encrypted == tt.data {
					t.Error("Encrypt() output matches input, suggesting no encryption occurred")
				}
			}
		})
	}
}

// TestEncryptFailure tests encryption failure scenarios
func TestEncryptFailure(t *testing.T) {
	tests := []struct {
		name          string
		data          string
		publicKey     string
		wantErr       bool
		errorContains string
	}{
		{
			name:          "Invalid public key format",
			data:          "test data",
			publicKey:     "invalid-base64!@#$",
			wantErr:       true,
			errorContains: "invalid public key",
		},
		{
			name:          "Invalid key bytes",
			data:          "test data",
			publicKey:     base64.StdEncoding.EncodeToString([]byte("invalid-key-bytes")),
			wantErr:       true,
			errorContains: "failed to create public key",
		},
		{
			name:          "Empty public key",
			data:          "test data",
			publicKey:     "",
			wantErr:       true,
			errorContains: "invalid public key",
		},
		{
			name:          "Too short key",
			data:          "test data",
			publicKey:     base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}),
			wantErr:       true,
			errorContains: "failed to create public key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter := &Encrypter{config: &Config{}}
			_, err := encrypter.Encrypt(context.Background(), tt.data, tt.publicKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("Encrypt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && !strings.Contains(err.Error(), tt.errorContains) {
				t.Errorf("Encrypt() error = %v, want error containing %q", err, tt.errorContains)
			}
		})
	}
}

// TestNew tests the creation of new Encrypter instances
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		config  *Config
		wantErr bool
	}{
		{
			name:    "Valid config",
			ctx:     context.Background(),
			config:  &Config{},
			wantErr: false,
		},
		{
			name:    "Nil config",
			ctx:     context.Background(),
			config:  nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter, err := New(tt.ctx, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && encrypter == nil {
				t.Error("New() returned nil encrypter")
			}
		})
	}
}

// TestClose tests the Close method
func TestClose(t *testing.T) {
	encrypter := &Encrypter{}
	if err := encrypter.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestEncrypt(t *testing.T) {
	// Generate a test public key for encryption tests
	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}
	publicKey := base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes())

	tests := []struct {
		name          string
		data          string
		publicKey     string
		wantErr       bool
		errorContains string
	}{
		{
			name:      "Valid encryption",
			data:      "test data",
			publicKey: publicKey,
			wantErr:   false,
		},
		{
			name:      "Empty data",
			data:      "",
			publicKey: publicKey,
			wantErr:   false,
		},
		{
			name:          "Invalid public key",
			data:          "test data",
			publicKey:     "invalid-base64",
			wantErr:       true,
			errorContains: "invalid public key",
		},
		{
			name:          "Empty public key",
			data:          "test data",
			publicKey:     "",
			wantErr:       true,
			errorContains: "invalid public key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter, err := New(context.Background(), &Config{})
			if err != nil {
				t.Fatalf("Failed to create encrypter: %v", err)
			}

			result, err := encrypter.Encrypt(context.Background(), tt.data, tt.publicKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("Encrypt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Encrypt() error = %v, want error containing %q", err, tt.errorContains)
				}
				return
			}

			if result == "" {
				t.Error("Encrypt() returned empty result")
			}

			// Verify the result is valid base64
			decoded, err := base64.StdEncoding.DecodeString(result)
			if err != nil {
				t.Errorf("Encrypt() returned invalid base64: %v", err)
			}
			if len(decoded) == 0 {
				t.Error("Encrypt() returned empty decoded result")
			}
		})
	}
}

func TestPKCS7Padding(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		blockSize int
		wantErr   bool
	}{
		{
			name:      "Valid padding",
			data:      []byte("test"),
			blockSize: 16,
			wantErr:   false,
		},
		{
			name:      "Empty data",
			data:      []byte{},
			blockSize: 16,
			wantErr:   false,
		},
		{
			name:      "Invalid block size - too small",
			data:      []byte("test"),
			blockSize: 0,
			wantErr:   true,
		},
		{
			name:      "Invalid block size - too large",
			data:      []byte("test"),
			blockSize: 256,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			padded, err := pkcs7Pad(tt.data, tt.blockSize)
			if (err != nil) != tt.wantErr {
				t.Errorf("pkcs7Pad() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(padded)%tt.blockSize != 0 {
					t.Errorf("pkcs7Pad() result length %d is not multiple of blockSize %d", len(padded), tt.blockSize)
				}
			}
		})
	}
}
