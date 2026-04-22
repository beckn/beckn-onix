package artifactverifier

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
)

// VerifyDetachedArtifact parses the signature and public key payloads, then
// verifies the detached signature against the supplied content bytes.
func VerifyDetachedArtifact(content, signaturePayload, publicKeyPayload []byte) error {
	signature, err := ParseSignature(signaturePayload)
	if err != nil {
		return err
	}
	publicKey, err := ParsePublicKeyResponse(publicKeyPayload)
	if err != nil {
		return err
	}
	return VerifyDetached(content, signature, publicKey)
}

// ParseSignature accepts either a raw detached signature body or a base64-encoded
// signature, and also supports JSON payloads that expose a "signature" field.
func ParseSignature(body []byte) ([]byte, error) {
	if value, ok := extractStringField(body, []string{"signature"}); ok {
		return decodeBase64String(value)
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("empty signature body")
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	return body, nil
}

// ParsePublicKeyResponse supports DeDi public-key lookup JSON, legacy JSON
// wrappers that expose raw key strings, and direct PEM/DER responses.
func ParsePublicKeyResponse(body []byte) (any, error) {
	type dediResponse struct {
		Data struct {
			Details struct {
				PublicKey string `json:"publicKey"`
				KeyType   string `json:"keyType"`
				KeyFormat string `json:"keyFormat"`
			} `json:"details"`
		} `json:"data"`
	}

	var response dediResponse
	if err := json.Unmarshal(body, &response); err == nil && response.Data.Details.PublicKey != "" {
		return parsePublicKeyValue(response.Data.Details.PublicKey, response.Data.Details.KeyFormat)
	}
	if value, ok := extractStringField(body, []string{"signing_public_key", "public_key", "publicKey"}); ok {
		return parsePublicKeyValue(value, "base64")
	}
	if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
		if key, err := parsePublicKeyValue(trimmed, "base64"); err == nil {
			return key, nil
		}
	}
	return parsePublicKey(body)
}

// VerifyDetached verifies a detached signature over content using the parsed
// public key. RSA and ECDSA use SHA-256; Ed25519 verifies the raw content.
func VerifyDetached(content, signature []byte, key any) error {
	sum := sha256.Sum256(content)

	switch pub := key.(type) {
	case *rsa.PublicKey:
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], signature)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(pub, sum[:], signature) {
			return fmt.Errorf("ECDSA signature verification failed")
		}
		return nil
	case ed25519.PublicKey:
		if !ed25519.Verify(pub, content, signature) {
			return fmt.Errorf("Ed25519 signature verification failed")
		}
		return nil
	default:
		return fmt.Errorf("unsupported public key type %T", key)
	}
}

func parsePublicKeyValue(value, format string) (any, error) {
	clean := strings.Join(strings.Fields(value), "")
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "pem", "x.509", "x509":
		return parsePublicKey([]byte(value))
	case "base64":
		// DeDi keyFormat=base64 currently expects standard padded base64; URL-safe or
		// alternate encodings are not supported by this decode path.
		decoded, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 public key: %w", err)
		}
		if key, err := parsePublicKey(decoded); err == nil {
			return key, nil
		}
		if len(decoded) == ed25519.PublicKeySize {
			return ed25519.PublicKey(decoded), nil
		}
		return nil, fmt.Errorf("failed to parse public key")
	default:
		return nil, fmt.Errorf("unsupported public key format %q", format)
	}
}

func parsePublicKey(data []byte) (any, error) {
	block, _ := pem.Decode(data)
	if block != nil {
		switch block.Type {
		case "PUBLIC KEY":
			key, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("failed to parse PKIX public key: %w", err)
			}
			return key, nil
		case "RSA PUBLIC KEY":
			key, err := x509.ParsePKCS1PublicKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("failed to parse RSA public key: %w", err)
			}
			return key, nil
		case "CERTIFICATE":
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("failed to parse certificate: %w", err)
			}
			return cert.PublicKey, nil
		default:
			return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
		}
	}

	key, err := x509.ParsePKIXPublicKey(data)
	if err == nil {
		return key, nil
	}

	cert, err := x509.ParseCertificate(data)
	if err == nil {
		return cert.PublicKey, nil
	}

	return nil, fmt.Errorf("failed to parse public key")
}

func decodeBase64String(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}
	return decoded, nil
}

func extractStringField(body []byte, keys []string) (string, bool) {
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return "", false
	}
	return findStringField(data, keys)
}

func findStringField(value any, keys []string) (string, bool) {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if raw, ok := v[key]; ok {
				if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
					return s, true
				}
			}
		}
		for _, child := range v {
			if s, ok := findStringField(child, keys); ok {
				return s, true
			}
		}
	case []any:
		for _, child := range v {
			if s, ok := findStringField(child, keys); ok {
				return s, true
			}
		}
	}
	return "", false
}
