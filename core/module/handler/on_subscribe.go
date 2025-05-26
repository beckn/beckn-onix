package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

// OnSubscribeRequest represents the request payload for the on_subscribe handler.
type OnSubscribeRequest struct {
	Status    string `json:"status,omitempty"`
	MessageID string `json:"message_id"`
	Challenge string `json:"challenge"` // Encrypted
}

// OnSubscribeResponse represents the response payload for the on_subscribe handler.
type OnSubscribeResponse struct {
	Answer    string `json:"answer"`     // Decrypted
	MessageID string `json:"message_id"` // Same as request
}

type onSubscribeStep struct {
	km definition.KeyManager
}

func (s *onSubscribeStep) Run(ctx *model.StepContext) error {
	var req OnSubscribeRequest
	if err := json.Unmarshal(ctx.Body, &req); err != nil {
		return model.NewBadReqErr(fmt.Errorf("invalid request body: %w", err))
	}

	// Validate required fields
	if req.MessageID == "" || req.Challenge == "" {
		return model.NewBadReqErr(fmt.Errorf("message_id and challenge are required"))
	}

	// Fetch key set from Key Manager using message ID
	keySet, _, err := s.km.SigningPrivateKey(ctx, req.MessageID)
	if err != nil {
		return fmt.Errorf("failed to get keys for message_id %s: %w", req.MessageID, err)
	}

	// Decode and decrypt the challenge
	encBytes, err := base64.StdEncoding.DecodeString(req.Challenge)
	if err != nil {
		return fmt.Errorf("failed to decode challenge: %w", err)
	}

	// Decrypt using the encryption private key
	answerBytes, err := decryptWithPrivateKey(encBytes, keySet)
	if err != nil {
		return fmt.Errorf("failed to decrypt challenge: %w", err)
	}

	// Prepare the response
	resp := OnSubscribeResponse{
		Answer:    string(answerBytes),
		MessageID: req.MessageID,
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	ctx.Body = respJSON
	return nil
}

// Helper: decrypt challenge using PEM private key
func decryptWithPrivateKey(ciphertext []byte, privateKeyPEM string) ([]byte, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		return nil, errors.New("invalid PEM format or key type")
	}

	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return rsa.DecryptPKCS1v15(rand.Reader, privKey, ciphertext)
}
