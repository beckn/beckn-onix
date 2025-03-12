package main

import (
	logger "beckn-onix/shared/log"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidPublicKey = errors.New("invalid public key")
	ErrEmptyData        = errors.New("empty data to encrypt")
	ErrNilContext       = errors.New("context cannot be nil")
)

// Encrypter defines the methods for encryption.
type Encrypter interface {
	// Encrypt encrypts the given body using the provided publicKeyBase64.
	Encrypt(ctx context.Context, data string, publicKeyBase64 string) (string, error)

	// Close for releasing resources
	Close() error
}

// EncrypterProvider initializes a new encrypter instance with the given config.
type EncrypterProvider interface {
	// New creates a new encrypter instance based on the provided config.
	New(ctx context.Context, config map[string]string) (Encrypter, error)
}

// PublicKeyManager is the interface for key management plugin to fetch public keys.
type PublicKeyManager interface {
	// PublicKey retrieves the public key for encryption for the given subscriberID and keyID.
	PublicKey(ctx context.Context, subscriberID string, keyID string) (string, error)
}

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

// Encrypt encrypts the given data using the provided public key
func (e *Encrypter) Encrypt(ctx context.Context, data string, publicKeyBase64 string) (string, error) {
	if ctx == nil {
		return "", errors.New("context cannot be nil")
	}
	if data == "" {
		return "", errors.New("empty data to encrypt")
	}
	if publicKeyBase64 == "" {
		return "", errors.New("invalid public key")
	}

	// Decode public key
	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		logger.Log.Error("Failed to decode public key:", err)
		return "", fmt.Errorf("invalid public key format: %w", err)
	}

	// Parse public key
	publicKey, err := x509.ParsePKIXPublicKey(publicKeyBytes)
	if err != nil {
		logger.Log.Error("Failed to parse public key:", err)
		return "", fmt.Errorf("invalid public key: %w", err)
	}

	// Type assertion to *rsa.PublicKey
	rsaPublicKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		return "", errors.New("not an RSA public key")
	}

	// Encrypt data
	dataBytes := []byte(data)
	hash := sha256.New()
	encryptedData, err := rsa.EncryptOAEP(hash, rand.Reader, rsaPublicKey, dataBytes, nil)
	if err != nil {
		logger.Log.Error("Failed to encrypt data:", err)
		return "", fmt.Errorf("encryption failed: %w", err)
	}

	// Store encrypted message
	msg := EncryptedMessage{
		ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		Data:      base64.StdEncoding.EncodeToString(encryptedData),
		KeyID:     e.keyID,
		Algorithm: e.algorithm,
	}
	e.messages = append(e.messages, msg)

	return msg.Data, nil
}
