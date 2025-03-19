package encryption

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
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

// PKCS7 padding implementation
func pkcs7Pad(data []byte, blockSize int) ([]byte, error) {
	if blockSize < 1 || blockSize > 255 {
		return nil, errors.New("invalid block size")
	}
	padding := blockSize - (len(data) % blockSize)
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padText...), nil
}

// Encrypt encrypts the given data using AES with a shared secret
func (e *Encrypter) Encrypt(ctx context.Context, data string, publicKeyBase64 string) (string, error) {
	// Generate ephemeral private key
	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("failed to generate private key: %w", err)
	}

	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return "", fmt.Errorf("invalid public key: %w", err)
	}

	// Convert the input string to a byte slice
	dataByte := []byte(data)

	aesCipher, err := createAESCipher(privateKey.Bytes(), publicKeyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	dataByte, err = pkcs7Pad(dataByte, aesCipher.BlockSize())
	if err != nil {
		return "", fmt.Errorf("failed to pad data: %w", err)
	}

	for i := 0; i < len(dataByte); i += aesCipher.BlockSize() {
		aesCipher.Encrypt(dataByte[i:i+aesCipher.BlockSize()], dataByte[i:i+aesCipher.BlockSize()])
	}

	return base64.StdEncoding.EncodeToString(dataByte), nil
}

func createAESCipher(privateKey, publicKey []byte) (cipher.Block, error) {
	x25519Curve := ecdh.X25519()
	x25519PrivateKey, err := x25519Curve.NewPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create private key: %w", err)
	}
	x25519PublicKey, err := x25519Curve.NewPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create public key: %w", err)
	}
	sharedSecret, err := x25519PrivateKey.ECDH(x25519PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to derive shared secret: %w", err)
	}

	aesCipher, err := aes.NewCipher(sharedSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	return aesCipher, nil
}

// Close releases any resources held by the Encrypter.
func (e *Encrypter) Close() error {
	// Implement any necessary cleanup here
	return nil
}
