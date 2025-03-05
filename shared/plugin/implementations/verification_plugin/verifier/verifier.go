package verifier

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/blake2b"
)

// Config struct for Verifier.
type Config struct {
}

// Verifier implements the Validator interface.
type Verifier struct {
	config *Config
}

// New creates a new Verifier instance.
func New(ctx context.Context, config *Config) (*Verifier, error) {
	return &Verifier{config: config}, nil
}

// Verify checks the signature for the given payload and public key.
func (v *Verifier) Verify(ctx context.Context, body []byte, header []byte, publicKeyBase64 string) (bool, error) {
	createdTimestamp, expiredTimestamp, signature, err := parseAuthHeader(string(header))
	if err != nil {
		return false, fmt.Errorf("error parsing header: %w", err)
	}

	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false, fmt.Errorf("error decoding signature: %w", err)
	}

	currentTime := time.Now().Unix()
	if createdTimestamp > currentTime || currentTime > expiredTimestamp {
		return false, fmt.Errorf("signature is expired or not yet valid")
	}

	createdTime := time.Unix(createdTimestamp, 0)
	expiredTime := time.Unix(expiredTimestamp, 0)

	signingString := createSigningString(body, createdTime.Unix(), expiredTime.Unix())

	decodedPublicKey, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return false, fmt.Errorf("error decoding public key: %w", err)
	}

	if !ed25519.Verify(ed25519.PublicKey(decodedPublicKey), []byte(signingString), signatureBytes) {
		return false, fmt.Errorf("signature verification failed")
	}

	return true, nil
}

// parseAuthHeader extracts signature values from the Authorization header.
func parseAuthHeader(header string) (int64, int64, string, error) {
	header = strings.TrimPrefix(header, "Signature ")

	parts := strings.Split(header, ",")
	signatureMap := make(map[string]string)

	for _, part := range parts {
		keyValue := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(keyValue) == 2 {
			key := strings.TrimSpace(keyValue[0])
			value := strings.Trim(keyValue[1], "\"")
			signatureMap[key] = value
		}
	}

	createdTimestamp, err := strconv.ParseInt(signatureMap["created"], 10, 64)
	if err != nil {
		return 0, 0, "", fmt.Errorf("invalid created timestamp: %w", err)
	}

	expiredTimestamp, err := strconv.ParseInt(signatureMap["expires"], 10, 64)
	if err != nil {
		return 0, 0, "", fmt.Errorf("invalid expires timestamp: %w", err)
	}

	signature := signatureMap["signature"]
	if signature == "" {
		return 0, 0, "", fmt.Errorf("signature missing in header")
	}

	return createdTimestamp, expiredTimestamp, signature, nil
}

// createSigningString constructs a signing string for verification.
func createSigningString(payload []byte, createdTimestamp, expiredTimestamp int64) string {
	hasher, _ := blake2b.New512(nil)
	hasher.Write(payload)
	hashSum := hasher.Sum(nil)
	digestB64 := base64.StdEncoding.EncodeToString(hashSum)

	return fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s", createdTimestamp, expiredTimestamp, digestB64)
}

// Close releases resources (mock implementation returning nil).
func (v *Verifier) Close() error {
	return nil
}
