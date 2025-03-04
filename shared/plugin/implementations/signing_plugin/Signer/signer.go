package signer

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/blake2b"
)

// SigningConfig holds the configuration for the signing process.
type SigningConfig struct {
	TTL int64
}

// Impl implements the Signer interface and handles the signing process.
type Impl struct {
	config SigningConfig
}

// NewSigner creates a new SignerImpl instance with the given configuration.
func NewSigner(ctx context.Context, config SigningConfig) (*Impl, error) {
	return &Impl{config: config}, nil
}

// createSigningString generates a signing string using BLAKE-512 hashing.
func createSigningString(payload []byte, createdAt, expiresAt int64) (string, error) {
	hasher, _ := blake2b.New512(nil)

	_, err := hasher.Write(payload)
	if err != nil {
		return "", fmt.Errorf("failed to hash payload: %w", err)
	}

	hashSum := hasher.Sum(nil)
	digestB64 := base64.StdEncoding.EncodeToString(hashSum)

	return fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s", createdAt, expiresAt, digestB64), nil
}

// signData signs the given signing string using the provided private key.
func signData(signingString []byte, privateKeyBase64 string) ([]byte, error) {
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("error decoding private key: %w", err)
	}

	if len(privateKeyBytes) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid private key length")
	}

	privateKey := ed25519.PrivateKey(privateKeyBytes)
	return ed25519.Sign(privateKey, signingString), nil
}

// Sign generates a digital signature for the provided payload.
func (s *Impl) Sign(ctx context.Context, body []byte, privateKeyBase64 string) (string, error) {
	createdAt := time.Now().Unix()
	expiresAt := createdAt + s.config.TTL

	signingString, err := createSigningString(body, createdAt, expiresAt)
	if err != nil {
		return "", err
	}

	signature, err := signData([]byte(signingString), privateKeyBase64)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

// Close releases resources (mock implementation returning nil).
func (s *Impl) Close() error {
	return nil
}
