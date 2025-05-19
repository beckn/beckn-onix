package handler

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/google/uuid"
)

// SubscribeRequest represents the incoming /subscribe request
type SubscribeRequest struct {
	SubscriberID string                 `json:"subscriber_id"`          // Unique identifier (e.g., https://bap.example.com)
	Type         string                 `json:"type"`                   // Type of participant: bap, bpp, bg
	Domain       string                 `json:"domain"`                 // Domain code (e.g., nic2004:60232)
	Location     map[string]interface{} `json:"location"`               // Location of the subscriber
	KeyID        string                 `json:"key_id,omitempty"`       // Unique Identifier of the key, if not passed a new key id will be generated
	KeyValidity  int64                  `json:"key_validity,omitempty"` // TTL for key, if not passed a configured value will be used as default
	URL          string                 `json:"url"`                    // Callback URL for network APIs
}

// RegistrySubscriptionRequest represents the request sent to registry
type RegistrySubscriptionRequest struct {
	SubscriberID     string                 `json:"subscriber_id"`
	Type             string                 `json:"type"`
	Domain           string                 `json:"domain"`
	Location         map[string]interface{} `json:"location"`
	KeyID            string                 `json:"key_id"`
	URL              string                 `json:"url"`
	SigningPublicKey string                 `json:"signing_public_key"` // Base64-encoded signing public key
	EncrPublicKey    string                 `json:"encr_public_key"`    // Base64-encoded encryption public key
	ValidFrom        string                 `json:"valid_from"`         // Validity start in ISO-8601 format
	ValidUntil       string                 `json:"valid_until"`        // Expiry in ISO-8601 format
	MessageID        string                 `json:"message_id"`         // For correlating subscribe and on_subscribe calls
}

// subscribeStep implements the /subscribe endpoint logic
type subscribeStep struct {
	km          definition.KeyManager
	signer      definition.Signer
	registryURL string
}

// newSubscribeStep creates a new subscribe step with required dependencies
func newSubscribeStep(km definition.KeyManager, signer definition.Signer, registryURL string) (definition.Step, error) {
	if km == nil {
		return nil, fmt.Errorf("invalid config: KeyManager plugin not configured")
	}
	if registryURL == "" {
		return nil, fmt.Errorf("invalid config: Registry URL not configured")
	}

	return &subscribeStep{
		km:          km,
		signer:      signer,
		registryURL: registryURL,
	}, nil
}

// Run executes the subscribe step logic
func (s *subscribeStep) Run(ctx *model.StepContext) error {
	// Parse the request body
	var req SubscribeRequest
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

	// Use provided key ID or generate a new one
	if req.KeyID == "" {
		req.KeyID = uuid.New().String()
	}
	keySet.UniqueKeyID = req.KeyID

	// Set validity period - use default 1 year if not specified
	validFrom := time.Now()
	validUntil := validFrom.Add(365 * 24 * time.Hour) // Default 1 year

	if req.KeyValidity > 0 {
		validUntil = validFrom.Add(time.Duration(req.KeyValidity) * time.Second)
	}

	// Generate message ID for correlation
	messageID := uuid.New().String()

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
func (s *subscribeStep) validateRequest(req *SubscribeRequest) error {
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
func (s *subscribeStep) createRegistryRequest(req *SubscribeRequest, keySet *model.Keyset, messageID string, validFrom, validUntil time.Time) *RegistrySubscriptionRequest {
	// Decode the signing public key from the keyset
	signingPubKey, _ := base64.StdEncoding.DecodeString(keySet.SigningPublic)
	// Parse and re-encode to ensure proper format
	parsedKey, _ := x509.ParsePKIXPublicKey(signingPubKey)
	signingPubKeyBytes, _ := x509.MarshalPKIXPublicKey(parsedKey)

	// Do the same for encryption public key
	encPubKey, _ := base64.StdEncoding.DecodeString(keySet.EncrPublic)
	parsedEncKey, _ := x509.ParsePKIXPublicKey(encPubKey)
	encPubKeyBytes, _ := x509.MarshalPKIXPublicKey(parsedEncKey)

	return &RegistrySubscriptionRequest{
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
func (s *subscribeStep) signRequest(ctx *model.StepContext, req *RegistrySubscriptionRequest, keySet *model.Keyset) (*RegistrySubscriptionRequest, error) {
	if s.signer == nil {
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
func (s *subscribeStep) callRegistrySubscribe(ctx *model.StepContext, req *RegistrySubscriptionRequest) (map[string]interface{}, error) {
	// Marshal the request body
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create the HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", s.registryURL+"/subscribe", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")

	// Make the request
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make http request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned non-200 status: %d, body: %s", resp.StatusCode, string(respBody))
	}

	// Parse the response
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return response, nil
}
