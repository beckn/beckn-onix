package signvalidator

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"golang.org/x/crypto/blake2b"
)

// Config struct for Verifier.
type Config struct {
}

// validator implements the validator interface.
type validator struct {
	config *Config
}

// New creates a new Verifier instance.
func New(ctx context.Context, config *Config) (*validator, func() error, error) {
	v := &validator{config: config}

	return v, nil, nil
}

// Verify checks the signature for the given payload and public key.
func (v *validator) Validate(ctx context.Context, body []byte, header string, publicKeyBase64 string) error {
	createdTimestamp, expiredTimestamp, signature, err := parseAuthHeader(header)
	if err != nil {
		return model.NewSignValidationErr(fmt.Errorf("error parsing header: %w", err))
	}

	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("error decoding signature: %w", err)
	}

	currentTime := time.Now().Unix()
	if createdTimestamp > currentTime {
		return model.NewSignValidationErr(fmt.Errorf("signature not yet valid: created=%s, server_time=%s, delta=%ds",
			time.Unix(createdTimestamp, 0).UTC().Format(time.RFC3339),
			time.Unix(currentTime, 0).UTC().Format(time.RFC3339),
			createdTimestamp-currentTime,
		))
	}
	if currentTime > expiredTimestamp {
		return model.NewSignValidationErr(fmt.Errorf("signature expired: expires=%s, server_time=%s, expired_by=%ds",
			time.Unix(expiredTimestamp, 0).UTC().Format(time.RFC3339),
			time.Unix(currentTime, 0).UTC().Format(time.RFC3339),
			currentTime-expiredTimestamp,
		))
	}

	createdTime := time.Unix(createdTimestamp, 0)
	expiredTime := time.Unix(expiredTimestamp, 0)

	signingString := hash(body, createdTime.Unix(), expiredTime.Unix())

	decodedPublicKey, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return model.NewSignValidationErr(fmt.Errorf("error decoding public key: %w", err))
	}

	if !ed25519.Verify(ed25519.PublicKey(decodedPublicKey), []byte(signingString), signatureBytes) {
		return model.NewSignValidationErr(fmt.Errorf("signature verification failed"))
	}

	return nil
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

	if signatureMap["algorithm"] != "ed25519" {
		return 0, 0, "", model.NewSignValidationErr(fmt.Errorf("unsupported algorithm %q: only ed25519 is permitted", signatureMap["algorithm"]))
	}

	createdTimestamp, err := strconv.ParseInt(signatureMap["created"], 10, 64)
	if err != nil {
		// TODO: Return appropriate error code when Error Code Handling Module is ready
		return 0, 0, "", fmt.Errorf("invalid created timestamp: %w", err)
	}

	expiredTimestamp, err := strconv.ParseInt(signatureMap["expires"], 10, 64)
	if err != nil {
		return 0, 0, "", model.NewSignValidationErr(fmt.Errorf("invalid expires timestamp: %w", err))
	}

	signature := signatureMap["signature"]
	if signature == "" {
		// TODO: Return appropriate error code when Error Code Handling Module is ready
		return 0, 0, "", model.NewSignValidationErr(fmt.Errorf("signature missing in header"))
	}

	return createdTimestamp, expiredTimestamp, signature, nil
}

// ValidateAck verifies a Beckn v2.0.0 AckSignature per NFH-004 §3.4.
// The signing string is identical to regular request signing with one addition:
// if outboundAuthSignature is non-empty, a fourth line is appended:
//
//	request-signature: <outboundAuthSignature>
//
// This mirrors exactly what ackSigner builds in SignAck.
func (v *validator) ValidateAck(ctx context.Context, ackBody []byte, signatureHeader, outboundAuthSignature, publicKeyBase64 string) error {
	createdTimestamp, expiredTimestamp, signature, err := parseAuthHeader(signatureHeader)
	if err != nil {
		return model.NewSignValidationErr(fmt.Errorf("error parsing Signature header: %w", err))
	}

	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("error decoding signature: %w", err)
	}

	currentTime := time.Now().Unix()
	if createdTimestamp > currentTime {
		return model.NewSignValidationErr(fmt.Errorf("AckSignature not yet valid: created=%s, server_time=%s, delta=%ds",
			time.Unix(createdTimestamp, 0).UTC().Format(time.RFC3339),
			time.Unix(currentTime, 0).UTC().Format(time.RFC3339),
			createdTimestamp-currentTime,
		))
	}
	if currentTime > expiredTimestamp {
		return model.NewSignValidationErr(fmt.Errorf("AckSignature expired: expires=%s, server_time=%s, expired_by=%ds",
			time.Unix(expiredTimestamp, 0).UTC().Format(time.RFC3339),
			time.Unix(currentTime, 0).UTC().Format(time.RFC3339),
			currentTime-expiredTimestamp,
		))
	}

	signingString := hashAck(ackBody, createdTimestamp, expiredTimestamp, outboundAuthSignature)

	decodedPublicKey, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return model.NewSignValidationErr(fmt.Errorf("error decoding public key: %w", err))
	}

	if !ed25519.Verify(ed25519.PublicKey(decodedPublicKey), []byte(signingString), signatureBytes) {
		return model.NewSignValidationErr(fmt.Errorf("AckSignature verification failed"))
	}

	return nil
}

// hash constructs a signing string for regular request verification.
func hash(payload []byte, createdTimestamp, expiredTimestamp int64) string {
	hasher, _ := blake2b.New512(nil)
	hasher.Write(payload)
	hashSum := hasher.Sum(nil)
	digestB64 := base64.StdEncoding.EncodeToString(hashSum)

	return fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s", createdTimestamp, expiredTimestamp, digestB64)
}

// hashAck constructs the NFH-004 §3.4 four-line signing string for AckSignature
// verification. Mirrors ackSigner.SignAck signing-string construction exactly.
func hashAck(payload []byte, createdTimestamp, expiredTimestamp int64, requestSignature string) string {
	s := hash(payload, createdTimestamp, expiredTimestamp)
	if requestSignature != "" {
		s += "\nrequest-signature: " + requestSignature
	}
	return s
}
