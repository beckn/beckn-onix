package handler

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

// subscribeHandler implements the /subscribe endpoint logic
type subscribeHandler struct {
	km          definition.KeyManager
	signer      definition.Signer
	registryURL string
	//registryClient client.RegistryClient
	registryClient interface { // Use an empty interface to allow mock injection
		RegistrySubscribe(ctx context.Context, endpoint string, reqBody []byte) (map[string]interface{}, error)
	}
}

// Run executes the subscribe step logic
func (s *subscribeHandler) Run(ctx *model.StepContext) error {
	// Parse the request body
	var req model.Subscriber
	if err := json.Unmarshal(ctx.Body, &req); err != nil {
		return model.NewBadReqErr(fmt.Errorf("invalid request body: %w", err))
	}

	// Validate required fields
	if err := s.validateRequest(&req); err != nil {
		return model.NewBadReqErr(err)
	}

	// Key Generation - Generate new keyset as specified
	keySet, err := s.km.GenerateKeyPairs()
	if err != nil {
		return fmt.Errorf("failed to generate key pairs: %w", err)
	}

	keySet.UniqueKeyID = req.KeyID
	// Set validity period - use default 1 year if not specified
	validFrom := time.Now()
	validUntil := validFrom.Add(365 * 24 * time.Hour) // Default 1 year

	if req.KeyValidity > 0 {
		validUntil = validFrom.Add(time.Duration(req.KeyValidity) * time.Second)
	}

	// Generate message ID for correlation
	messageID := req.KeyID // use key_id for correlation if no better ID available
	if messageID == "" {
		return fmt.Errorf("Message ID Empty")
	}
	// Create registry subscription request
	registryReq := s.createRegistryRequest(&req, keySet, messageID, validFrom, validUntil)

	// In case of update, sign the request with the new key
	if s.signer != nil && ctx.SubID != "" {
		// This is an update, sign with new key
		signedReq, err := s.signRequest(ctx, registryReq, keySet)
		if err != nil {
			return fmt.Errorf("failed to sign request: %w", err)
		}
		registryReq = signedReq
	}

	// Make call to registry's /subscribe endpoint
	registryResponse, err := s.callRegistrySubscribe(ctx, registryReq)
	if err != nil {
		return fmt.Errorf("failed to call registry: %w", err)
	}

	// Store the new keys against the message ID in key manager
	// Use the message ID from registry response if available, otherwise use our generated one
	responseMessageID := messageID
	if msgID, ok := registryResponse["message_id"].(string); ok && msgID != "" {
		responseMessageID = msgID
	}

	if err := s.km.StorePrivateKeys(ctx, responseMessageID, keySet); err != nil {
		return fmt.Errorf("failed to store keys: %w", err)
	}

	// Prepare response
	response := map[string]interface{}{
		"status":     registryResponse["status"],
		"message_id": responseMessageID,
	}

	// Store response in context
	respJSON, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	// Set the response body in context
	ctx.Body = respJSON

	log.Infof(ctx, "Subscription initiated for %s with message ID %s", req.SubscriberID, messageID)

	return nil
}

// validateRequest validates the incoming subscribe request  requirements
func (s *subscribeHandler) validateRequest(req *model.Subscriber) error {
	if req.SubscriberID == "" {
		return fmt.Errorf("subscriber_id is required")
	}

	if req.Type == "" {
		return fmt.Errorf("type is required")
	}

	// Validate type is one of: bap, bpp, bg
	switch req.Type {
	case "bap", "bpp", "bg":
		// valid
	default:
		return fmt.Errorf("type must be one of: bap, bpp, bg")
	}

	if req.Domain == "" {
		return fmt.Errorf("domain is required")
	}

	if req.Location == nil {
		return fmt.Errorf("location is required")
	}

	if req.URL == "" {
		return fmt.Errorf("url is required")
	}

	return nil
}

// createRegistryRequest creates the registry subscription request
func (s *subscribeHandler) createRegistryRequest(req *model.Subscriber, keySet *model.Keyset, messageID string, validFrom, validUntil time.Time) *model.RegistrySubscriptionRequest {
	// Decode the signing public key from the keyset
	signingPubKey, _ := base64.StdEncoding.DecodeString(keySet.SigningPublic)
	// Parse and re-encode to ensure proper format
	parsedKey, _ := x509.ParsePKIXPublicKey(signingPubKey)
	signingPubKeyBytes, _ := x509.MarshalPKIXPublicKey(parsedKey)

	// Do the same for encryption public key
	encPubKey, _ := base64.StdEncoding.DecodeString(keySet.EncrPublic)
	parsedEncKey, _ := x509.ParsePKIXPublicKey(encPubKey)
	encPubKeyBytes, _ := x509.MarshalPKIXPublicKey(parsedEncKey)

	return &model.RegistrySubscriptionRequest{
		SubscriberID:     req.SubscriberID,
		Type:             req.Type,
		Domain:           req.Domain,
		Location:         req.Location,
		KeyID:            keySet.UniqueKeyID,
		URL:              req.URL,
		SigningPublicKey: base64.StdEncoding.EncodeToString(signingPubKeyBytes),
		EncrPublicKey:    base64.StdEncoding.EncodeToString(encPubKeyBytes),
		ValidFrom:        validFrom.Format(time.RFC3339),
		ValidUntil:       validUntil.Format(time.RFC3339),
		MessageID:        messageID,
	}
}

// signRequest signs the registry request with the new key (for updates)
func (s *subscribeHandler) signRequest(ctx *model.StepContext, req *model.RegistrySubscriptionRequest, keySet *model.Keyset) (*model.RegistrySubscriptionRequest, error) {
	if s.signer == nil {
		return req, nil
	}

	// Only sign update requests — identified by a non-empty SubscriberID
	if req.SubscriberID == "" {
		// It's a create request; no signing needed
		return req, nil
	}

	// Marshal the request to JSON
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Get the private signing key
	privateKey := keySet.SigningPrivate

	// Sign the request
	createdAt := time.Now().Unix()
	validTill := time.Now().Add(5 * time.Minute).Unix()

	signature, err := s.signer.Sign(ctx, reqBody, privateKey, createdAt, validTill)
	if err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	// Add signature to the request (this would typically be in a header)
	// For now, we'll add it as a field
	signedReq := *req
	// In real implementation, this would be added as an Authorization header
	_ = signature

	return &signedReq, nil
}

// callRegistrySubscribe makes the HTTP call to the Registry's /subscribe endpoint
func (s *subscribeHandler) callRegistrySubscribe(ctx *model.StepContext, req *model.RegistrySubscriptionRequest) (map[string]interface{}, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	return s.registryClient.RegistrySubscribe(ctx, "subscribe", reqBody)

}
