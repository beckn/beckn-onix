package decryption

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"encoding/base64"
	"fmt"

	"github.com/zenazn/pkcs7pad"
)

// Config holds the configuration for the decryption process.
type Config struct {
}

// Decrypter implements the Decrypter interface and handles the decryption process.
type Decrypter struct {
	config *Config
}

// New creates a new Decrypter instance with the given configuration.
func New(ctx context.Context, config *Config) (*Decrypter, func() error, error) {
	d := &Decrypter{config: config}
	return d, d.Close, nil
}

// Decrypt decrypts the given encryptedData using the provided privateKeyBase64.
func (d *Decrypter) Decrypt(ctx context.Context, encryptedData, privateKeyBase64, publicKeyBase64 string) (string, error) {
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}

	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return "", fmt.Errorf("invalid public key: %w", err)
	}

	// Decode the Base64 encoded encrypted data
	messageByte, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return "", fmt.Errorf("failed to decode encrypted data: %w", err)
	}

	aesCipher, err := createAESCipher(privateKeyBytes, publicKeyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	blocksize := aesCipher.BlockSize()
	if len(messageByte)%blocksize != 0 {
		return "", fmt.Errorf("ciphertext is not a multiple of the blocksize")
	}

	for i := 0; i < len(messageByte); i += aesCipher.BlockSize() {
		executionSlice := messageByte[i : i+aesCipher.BlockSize()]
		aesCipher.Decrypt(executionSlice, executionSlice)
	}

	messageByte, err = pkcs7pad.Unpad(messageByte)
	if err != nil {
		return "", fmt.Errorf("failed to unpad data: %w", err)
	}

	return string(messageByte), nil
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

// Close releases resources
func (d *Decrypter) Close() error {
	return nil
}
