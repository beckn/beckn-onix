package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	plugindefinitions "beckn_signing.go/plugin_definitions"
	"golang.org/x/crypto/blake2b"
)

type Signing struct {
	PublicKeyPath  string
	PrivateKeyPath string
}

var _ plugindefinitions.SignatureAndValidation = (*Signing)(nil)

func NewSigning(publicKeyPath, privateKeyPath string) plugindefinitions.SignatureAndValidation {
	return &Signing{PublicKeyPath: publicKeyPath, PrivateKeyPath: privateKeyPath}
}

func (s *Signing) PublicKey() ([]byte, error) {
	data, err := os.ReadFile(s.PublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("error reading public key file: %w", err)
	}
	return data, nil
}

func (s *Signing) PrivateKey() ([]byte, error) {
	data, err := os.ReadFile(s.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("error reading private key file: %w", err)
	}
	return data, nil
}

func createSigningString(payload []byte, createdTimestamp, expiredTimestamp int64) string {
	// digest := blake2b.Sum512(payload)
	hasher, _ := blake2b.New512(nil)
	hasher.Write(payload)
	hashSum := hasher.Sum(nil)
	// digestB64 := base64.StdEncoding.EncodeToString(digest[:])
	digestB64 := base64.StdEncoding.EncodeToString(hashSum)
	return fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s",
		createdTimestamp, expiredTimestamp, digestB64)
}

func Signer(signingString []byte, privateKeyBase64 string) ([]byte, error) {
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("error decoding private key: %w", err)
	}
	generatedPrivateKey := ed25519.PrivateKey(privateKeyBytes)

	signature := ed25519.Sign(generatedPrivateKey, signingString)

	return signature, nil
}

func SignPayload(payload []byte, privateKey string, createdTimestamp, expiredTimestamp int64) (string, error) {
	signingString := createSigningString(payload, createdTimestamp, expiredTimestamp)
	signature, err := Signer([]byte(signingString), privateKey)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

func (s *Signing) Sign(body []byte, subscriberID string, keyID string) (string, error) {
	createdTimestamp := time.Now().Unix()
	expiredTimestamp := createdTimestamp + (60 * 60)
	privateKey, err := s.PrivateKey()
	if err != nil {
		return "", fmt.Errorf("error loading private key: %w", err)
	}

	signature, err := SignPayload(body, string(privateKey), createdTimestamp, expiredTimestamp)
	if err != nil {
		return "", err
	}

	authSignature := fmt.Sprintf(
		`Signature keyId="%s|%s|ed25519",algorithm="ed25519",created="%d",expires="%d",headers="(created) (expires) digest",signature="%s"`,
		subscriberID, keyID, createdTimestamp, expiredTimestamp, signature,
	)
	return authSignature, nil
}

func extractHeaderValue(key, headers string) string {
	lines := strings.Split(headers, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	return ""
}

func parseSignatureHeader(header string) map[string]string {
	parts := strings.Split(header, ", ")
	signatureMap := make(map[string]string)

	for _, part := range parts {
		keyValue := strings.SplitN(part, "=", 2)
		if len(keyValue) == 2 {
			key := strings.TrimSpace(keyValue[0])
			value := strings.Trim(keyValue[1], "\"")
			signatureMap[key] = value
		}
	}

	return signatureMap
}

func verifySignaturePK(signatureBase64, signingString, publicKeyBase64 string) bool {
	signature, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil {
		fmt.Println("Error decoding signature:", err)
		return false
	}

	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		fmt.Println("Error decoding public key:", err)
		return false
	}

	signingStringBytes := []byte(signingString)

	publicKey := ed25519.PublicKey(publicKeyBytes)
	return ed25519.Verify(publicKey, signingStringBytes, signature)
}

func (s *Signing) Verify(body []byte, header []byte) (bool, error) {
	headerString := string(header)

	signatureHeader := extractHeaderValue("Authorization", headerString)
	if signatureHeader == "" {
		return false, fmt.Errorf("authorization header missing")
	}
	signatureParts := parseSignatureHeader(signatureHeader)
	timestamp, err := strconv.ParseInt(signatureParts["created"], 10, 64)
	cTime := time.Unix(timestamp, 0)
	if err != nil {
		return false, fmt.Errorf("invalid created timestamp: %w", err)
	}

	expires, err := strconv.ParseInt(signatureParts["expires"], 10, 64)
	eTime := time.Unix(expires, 0)
	fmt.Println("Printing the expiry time :", expires)
	fmt.Println("Printing updated expiry time :", eTime.Unix())
	if err != nil {
		return false, fmt.Errorf("invalid expires timestamp: %w", err)
	}

	// Check if the signature is within the valid time window
	currentTime := time.Now().Unix()
	if timestamp > currentTime || currentTime > expires {
		return false, fmt.Errorf("signature is expired or not yet valid")
	}

	blakeValue := createSigningString(body, cTime.Unix(), eTime.Unix())
	publicKey, err := s.PublicKey()
	if err != nil {
		return false, fmt.Errorf("error loading private key: %w", err)
	}

	// Verify the signature
	isVerified := verifySignaturePK(signatureParts["signature"], blakeValue, string(publicKey))
	return isVerified, nil
}
