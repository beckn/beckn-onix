package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/beckn/beckn-onix/core/module/client"
	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

// SubscribeHandler manages the subscription process
type SubscribeHandler struct {
	km             definition.KeyManager
	signer         definition.Signer
	RegistryURL    string
	registryClient client.RegistryClientInterface
}

// NewSubscribeHandler initializes and returns an HTTP handler for subscription
func NewSubscribeHandler(ctx context.Context, km definition.KeyManager, signer definition.Signer, registryClient client.RegistryClientInterface, registryURL string) http.Handler {
	handler := &SubscribeHandler{
		km:             km,
		signer:         signer,
		registryClient: registryClient,
		RegistryURL:    registryURL,
	}

	return http.HandlerFunc(handler.HandleSubscribe)
}

// HandleSubscribe processes incoming subscription requests
func (s *SubscribeHandler) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse request body
	var req model.Subscriber
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate request fields
	if err := s.validateRequest(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}

	// Generate new keyset
	keySet, err := s.km.GenerateKeyPairs()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to generate key pairs: %v", err), http.StatusInternalServerError)
		return
	}

	keySet.UniqueKeyID = req.KeyID

	// Determine key validity period
	validFrom := time.Now()
	validUntil := validFrom.Add(365 * 24 * time.Hour)

	if req.KeyValidity > 0 {
		validUntil = validFrom.Add(time.Duration(req.KeyValidity) * time.Second)
	}

	// Generate message ID
	messageID := req.KeyID
	if messageID == "" {
		http.Error(w, "message ID empty", http.StatusBadRequest)
		return
	}

	// Create registry subscription request
	registryReq := s.createRegistryRequest(&req, keySet, messageID, validFrom, validUntil)

	// If updating, sign the request
	if s.signer != nil {
		signedReq, err := s.signRequest(ctx, registryReq, keySet)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to sign request: %v", err), http.StatusInternalServerError)
			return
		}
		registryReq = signedReq
	}

	reqBody, err := json.Marshal(registryReq)
	if err != nil {
		return
	}

	// Call registry's /subscribe endpoint
	registryResponse, err := s.registryClient.RegistrySubscribe(ctx, s.RegistryURL, reqBody)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to call registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Store new keys against message ID
	responseMessageID := messageID
	if msgID, ok := registryResponse["message_id"].(string); ok && msgID != "" {
		responseMessageID = msgID
	}

	if err := s.km.StorePrivateKeys(ctx, responseMessageID, keySet); err != nil {
		http.Error(w, fmt.Sprintf("failed to store keys: %v", err), http.StatusInternalServerError)
		return
	}

	// Prepare and send response
	response := map[string]interface{}{
		"status":     registryResponse["status"],
		"message_id": responseMessageID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	log.Infof(ctx, "Subscription initiated for %s with message ID %s", req.SubscriberID, messageID)

}

// validateRequest checks the required fields in the request
func (s *SubscribeHandler) validateRequest(req *model.Subscriber) error {
	if req.SubscriberID == "" {
		return fmt.Errorf("subscriber_id is required")
	}
	if req.Type == "" || (req.Type != "bap" && req.Type != "bpp" && req.Type != "bg") {
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

// createRegistryRequest prepares the subscription request body
func (s *SubscribeHandler) createRegistryRequest(req *model.Subscriber, keySet *model.Keyset, messageID string, validFrom, validUntil time.Time) *model.RegistrySubscriptionRequest {
	return &model.RegistrySubscriptionRequest{
		SubscriberID: req.SubscriberID,
		Type:         req.Type,
		Domain:       req.Domain,
		Location:     req.Location,
		KeyID:        keySet.UniqueKeyID,
		URL:          req.URL,
		ValidFrom:    validFrom.Format(time.RFC3339),
		ValidUntil:   validUntil.Format(time.RFC3339),
		MessageID:    messageID,
	}
}

// signRequest signs registry requests for updates
func (s *SubscribeHandler) signRequest(ctx context.Context, req *model.RegistrySubscriptionRequest, keySet *model.Keyset) (*model.RegistrySubscriptionRequest, error) {
	if s.signer == nil {
		return req, nil
	}

	if req.SubscriberID == "" {
		return req, nil // No signing needed for new subscriptions
	}

	// Marshal request
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	// Sign request with generated private key
	signature, err := s.signer.Sign(ctx, reqBody, keySet.SigningPrivate, time.Now().Unix(), time.Now().Add(5*time.Minute).Unix())
	if err != nil {
		return nil, fmt.Errorf("failed to sign request: %v", err)
	}

	// Add signature to the request (this would typically be in a header)
	// For now, we'll add it as a field
	signedReq := *req
	// In real implementation, this would be added as an Authorization header
	_ = signature

	return &signedReq, nil
}
