package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

// RegistryClient interface for registry client
type RegistryClient interface {
	CreateRequest(ctx context.Context, method, endpoint string, body []byte) (*http.Response, error)
}

// CheckSubscriptionStatus verifies if the participant is SUBSCRIBED and re-associates keys to subscriberID
func CheckSubscriptionStatus(
	ctx context.Context,
	cfg *Config,
	keyManager definition.KeyManager,
	domain string,
	typeName string,
	keyID string,
	registryClient RegistryClient,
) error {
	requestPayload := model.LookupRequest{
		SubscriberID: cfg.SubscriberID,
		KeyID:        keyID,
		Domain:       domain,
		Type:         typeName,
	}

	const (
		maxRetries    = 3
		retryInterval = 3 * time.Second
	)

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("Attempt %d: Checking subscription status...\n", attempt)

		reqBody, err := json.Marshal(requestPayload)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}

		// url := fmt.Sprintf("%s/lookup", cfg.RegistryURL)
		// req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
		// if err != nil {
		// 	return fmt.Errorf("failed to create HTTP request: %w", err)
		// }
		// req.Header.Set("Content-Type", "application/json")

		// resp, err := http.DefaultClient.Do(req)
		// if err != nil {
		// 	log.Printf("Request failed: %v", err)
		// 	time.Sleep(retryInterval)
		// 	continue
		// }
		// defer resp.Body.Close()

		resp, err := registryClient.CreateRequest(ctx, "POST", "lookup", reqBody)
		if err != nil {
			log.Printf("Request failed: %v", err)
			time.Sleep(retryInterval)
			continue
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Failed to read response: %v", err)
			time.Sleep(retryInterval)
			continue
		}

		var lookupResp model.LookupResponse
		if err := json.Unmarshal(respBody, &lookupResp); err != nil {
			log.Printf("Failed to parse JSON: %v\nRaw response: %s", err, string(respBody))
			time.Sleep(retryInterval)
			continue
		}

		if lookupResp.Status == "SUBSCRIBED" {
			log.Printf("Participant is SUBSCRIBED")

			// Re-associate keys to subscriber_id
			// First: get signing and encryption private keys by key_id
			signingKey, _, err := keyManager.SigningPrivateKey(ctx, keyID)
			if err != nil {
				return fmt.Errorf("failed to fetch signing private key: %w", err)
			}
			encryptionKey, _, err := keyManager.EncrPrivateKey(ctx, keyID)
			if err != nil {
				return fmt.Errorf("failed to fetch encryption private key: %w", err)
			}

			// Construct a new Keyset model
			keyset := &model.Keyset{
				SigningPrivate: signingKey,
				EncrPrivate:    encryptionKey,
			}

			// Store under keyID again (assumes same keyID now scoped to subscriber)
			if err := keyManager.StorePrivateKeys(ctx, keyID, keyset); err != nil {
				return fmt.Errorf("failed to store keyset under subscriber ID: %w", err)
			}

			log.Printf("Keys successfully stored for subscriber: %s", cfg.SubscriberID)
			return nil
		}

		log.Printf("Status: %s — retrying after %v", lookupResp.Status, retryInterval)
		time.Sleep(retryInterval)
	}

	return fmt.Errorf("subscription not confirmed after %d retries", maxRetries)
}
