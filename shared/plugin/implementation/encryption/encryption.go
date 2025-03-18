package encryption

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
)

// Config holds the configuration for the encryption process.
type Config struct {
}

// Encrypter implements the Encrypter interface and handles the encryption process.
type Encrypter struct {
	config *Config
}

// New creates a new Encrypter instance with the given configuration.
func New(ctx context.Context, config *Config) (*Encrypter, error) {
	return &Encrypter{config: config}, nil
}

// Encrypt encrypts the given body using the provided publicKeyBase64.
func (e *Encrypter) Encrypt(ctx context.Context, data string, publicKeyBase64 string) (string, error) {
	// Decode the base64-encoded public key
	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode public key: %w", err)
	}

	// Parse the public key
	block, _ := pem.Decode(publicKeyBytes)
	if block == nil || block.Type != "PUBLIC KEY" {
		return "", errors.New("failed to decode PEM block containing public key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse public key: %w", err)
	}

	// Type assert to *rsa.PublicKey
	rsaPublicKey, ok := pub.(*rsa.PublicKey)
	if !ok {
		return "", errors.New("not an RSA public key")
	}

	// Convert the input string to a byte slice
	dataBytes := []byte(data)

	// Hash the data before encrypting (RSA encryption typically requires a hash function)
	hash := sha256.New()
	encryptedData, err := rsa.EncryptOAEP(hash, rand.Reader, rsaPublicKey, dataBytes, nil)
	if err != nil {
		return "", err
	}

	// Base64 encode the encrypted data for easier transfer
	return base64.StdEncoding.EncodeToString(encryptedData), nil
}

// Close releases any resources held by the Encrypter.
func (e *Encrypter) Close() error {
	// Implement any necessary cleanup here
	return nil
}
