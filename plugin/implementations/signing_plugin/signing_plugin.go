package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	plugins "plugins/plugin"
	"time"

	"golang.org/x/crypto/blake2b"
)

type Signing struct{}

func NewSigning() *Signing {
	return &Signing{}
}

func createSigningString(payload []byte, createdTimestamp, expiredTimestamp int64) string {
	hasher, _ := blake2b.New512(nil)
	hasher.Write(payload)
	hashSum := hasher.Sum(nil)
	digestB64 := base64.StdEncoding.EncodeToString(hashSum)

	// commenting for now as we are not sending timestamp for verification
	// return fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s",
	// 	createdTimestamp, expiredTimestamp, digestB64)
	return string(digestB64)
}

func SignerFunc(signingString []byte, privateKeyBase64 string) ([]byte, error) {
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("error decoding private key: %w", err)
	}
	privateKey := ed25519.PrivateKey(privateKeyBytes)

	signature := ed25519.Sign(privateKey, signingString)
	return signature, nil
}

func SignPayload(payload []byte, privateKey string, createdTimestamp, expiredTimestamp int64) (string, error) {
	signingString := createSigningString(payload, createdTimestamp, expiredTimestamp)
	signature, err := SignerFunc([]byte(signingString), privateKey)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func (s *Signing) Sign(ctx context.Context, body []byte, keyID string) (string, error) {
	privateKey := "XQTrvH1kZippFmizR25yvBD12+5bJCSfUkFw5nFHKnEO7wkaMFk+hHgGVafenaXXKEWBWoWFMJVX8EtRGXUhew=="
	createdTimestamp := time.Now().Unix()
	expiredTimestamp := createdTimestamp + (60 * 60)

	signature, err := SignPayload(body, privateKey, createdTimestamp, expiredTimestamp)
	if err != nil {
		return "", err
	}

	fmt.Println("Created timestamp from signing : ", createdTimestamp)
	fmt.Println("Expired timestamp from signing : ", expiredTimestamp)

	return signature, nil
}

type SignerProvider struct{}

func (sp *SignerProvider) New(ctx context.Context, config map[string]string) (plugins.Signer, error) {
	return NewSigning(), nil
}

func GetPlugin() plugins.SignerProvider {
	return &SignerProvider{}
}
