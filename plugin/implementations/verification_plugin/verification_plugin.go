package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	plugins "plugins/plugin"
	"strings"

	"golang.org/x/crypto/blake2b"
)

type Verification struct{}

func NewVerification() *Verification {
	return &Verification{}
}

func (v *Verification) Verify(ctx context.Context, body []byte, header []byte) (bool, error) {
	// headerString := string(header)

	// signatureHeader := extractHeaderValue("Authorization", headerString)
	// if signatureHeader == "" {
	// 	return false, fmt.Errorf("authorization header missing")
	// }

	// signatureParts := parseSignatureHeader(signatureHeader)

	// timestamp, err := strconv.ParseInt(signatureParts["created"], 10, 64)
	// if err != nil {
	// 	return false, fmt.Errorf("invalid created timestamp: %w", err)
	// }
	// createdTime := time.Unix(timestamp, 0)

	// expires, err := strconv.ParseInt(signatureParts["expires"], 10, 64)
	// if err != nil {
	// 	return false, fmt.Errorf("invalid expires timestamp: %w", err)
	// }
	// expiredTime := time.Unix(expires, 0)

	// currentTime := time.Now().Unix()
	// if timestamp > currentTime || currentTime > expires {
	// 	return false, fmt.Errorf("signature is expired or not yet valid")
	// }

	// signingString := createSigningString(body, createdTime.Unix(), expiredTime.Unix())

	signingString := createSigningString(body)

	publicKey := "Du8JGjBZPoR4BlWn3p2l1yhFgVqFhTCVV/BLURl1IXs="

	// isVerified := verifySignaturePK(signatureParts["signature"], signingString, publicKey)
	isVerified := verifySignaturePK(string(header), signingString, publicKey)
	return isVerified, nil
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

func parseSignatureHeader(header string) map[string]string {
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

	fmt.Println("Parsed Signature Map:", signatureMap)
	return signatureMap
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

// Creates a signing string for verification.
// func createSigningString(payload []byte, createdTimestamp, expiredTimestamp int64) string {
// 	hasher, _ := blake2b.New512(nil)
// 	hasher.Write(payload)
// 	hashSum := hasher.Sum(nil)
// 	digestB64 := base64.StdEncoding.EncodeToString(hashSum)

// 	return fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s",
// 		createdTimestamp, expiredTimestamp, digestB64)
// }

func createSigningString(payload []byte) string {
	hasher, _ := blake2b.New512(nil)
	hasher.Write(payload)
	hashSum := hasher.Sum(nil)
	digestB64 := base64.StdEncoding.EncodeToString(hashSum)

	return string(digestB64)
}

type VerifierProvider struct{}

func (vp *VerifierProvider) New(ctx context.Context, config map[string]string) (plugins.Validator, error) {
	return NewVerification(), nil
}

func GetPlugin() plugins.ValidatorProvider {
	return &VerifierProvider{}
}
