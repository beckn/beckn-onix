package encrypter

import (
        "context"
        "crypto/aes"
        "crypto/cipher"
        "crypto/ecdh"
        "encoding/base64"
        "fmt"

        "github.com/zenazn/pkcs7pad"
)

// Config holds the configuration for the encryption process.
type Config struct {
}

// Encrypter implements the Encrypter interface and handles the encryption process.
type Encrypter struct {
        config *Config
}

// New creates a new Encrypter instance with the given configuration.
func New(ctx context.Context, config *Config) (*Encrypter, func() error, error) {
        e := &Encrypter{config: config}
        return e, e.Close, nil
}

func (e *Encrypter) Encrypt(ctx context.Context, data string, privateKeyBase64, publicKeyBase64 string) (string, error) {
        privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyBase64)
        if err != nil {
                return "", fmt.Errorf("invalid private key: %w", err)
        }

        publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
        if err != nil {
                return "", fmt.Errorf("invalid public key: %w", err)
        }

        // Convert the input string to a byte slice
        dataByte := []byte(data)

        aesCipher, err := createAESCipher(privateKeyBytes, publicKeyBytes)
        if err != nil {
                return "", fmt.Errorf("failed to create AES cipher: %w", err)
        }

        dataByte = pkcs7pad.Pad(dataByte, aesCipher.BlockSize())

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

// Close releases resources
func (e *Encrypter) Close() error {
        return nil
}