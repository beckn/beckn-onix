package dediregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/hashicorp/go-retryablehttp"
)

// Config holds configuration parameters for the DeDi registry client.
type Config struct {
	URL               string        `yaml:"url" json:"url"`
	RegistryName      string        `yaml:"registryName" json:"registryName"`
	AllowedNetworkIDs []string      `yaml:"allowedNetworkIDs" json:"allowedNetworkIDs"`
	Timeout           int           `yaml:"timeout" json:"timeout"`
	RetryMax          int           `yaml:"retry_max" json:"retry_max"`
	RetryWaitMin      time.Duration `yaml:"retry_wait_min" json:"retry_wait_min"`
	RetryWaitMax      time.Duration `yaml:"retry_wait_max" json:"retry_wait_max"`
}

// DeDiRegistryClient encapsulates the logic for calling the DeDi registry endpoints.
type DeDiRegistryClient struct {
	config *Config
	client *retryablehttp.Client
}

// validate checks if the provided DeDi registry configuration is valid.
func validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("DeDi registry config cannot be nil")
	}
	if cfg.URL == "" {
		return fmt.Errorf("url cannot be empty")
	}
	if cfg.RegistryName == "" {
		return fmt.Errorf("registryName cannot be empty")
	}
	return nil
}

// New creates a new instance of DeDiRegistryClient.
func New(ctx context.Context, cfg *Config) (*DeDiRegistryClient, func() error, error) {
	log.Debugf(ctx, "Initializing DeDi Registry client with config: %+v", cfg)

	if err := validate(cfg); err != nil {
		return nil, nil, err
	}

	retryClient := retryablehttp.NewClient()

	// Configure timeout if provided
	if cfg.Timeout > 0 {
		retryClient.HTTPClient.Timeout = time.Duration(cfg.Timeout) * time.Second
	}

	// Configure retry settings if provided
	if cfg.RetryMax > 0 {
		retryClient.RetryMax = cfg.RetryMax
	}
	if cfg.RetryWaitMin > 0 {
		retryClient.RetryWaitMin = cfg.RetryWaitMin
	}
	if cfg.RetryWaitMax > 0 {
		retryClient.RetryWaitMax = cfg.RetryWaitMax
	}

	client := &DeDiRegistryClient{
		config: cfg,
		client: retryClient,
	}

	// Cleanup function
	closer := func() error {
		log.Debugf(ctx, "Cleaning up DeDi Registry client resources")
		if client.client != nil {
			client.client.HTTPClient.CloseIdleConnections()
		}
		return nil
	}

	log.Infof(ctx, "DeDi Registry client connection established successfully")
	return client, closer, nil
}

// Lookup implements RegistryLookup interface - calls the DeDi wrapper lookup endpoint and returns Subscription.
func (c *DeDiRegistryClient) Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error) {
	// Extract subscriber ID and key ID from request (both come from Authorization header parsing)
	subscriberID := req.SubscriberID
	keyID := req.KeyID
	log.Infof(ctx, "DeDi Registry: Looking up subscriber ID: %s, key ID: %s", subscriberID, keyID)
	if subscriberID == "" {
		return nil, fmt.Errorf("subscriber_id is required for DeDi lookup")
	}
	if keyID == "" {
		return nil, fmt.Errorf("key_id is required for DeDi lookup")
	}

	lookupURL := fmt.Sprintf("%s/lookup/%s/%s/%s",
		c.config.URL, subscriberID, c.config.RegistryName, keyID)

	httpReq, err := retryablehttp.NewRequest("GET", lookupURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq = httpReq.WithContext(ctx)

	log.Debugf(ctx, "Making DeDi lookup request to: %s", lookupURL)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send DeDi lookup request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Errorf(ctx, nil, "DeDi lookup request failed with status: %s, response: %s", resp.Status, string(body))
		return nil, fmt.Errorf("DeDi lookup request failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response
	var responseData map[string]interface{}
	err = json.Unmarshal(body, &responseData)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	log.Debugf(ctx, "DeDi lookup request successful, parsing response")

	// Extract data field
	data, ok := responseData["data"].(map[string]interface{})
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi response format: missing or invalid data field")
		return nil, fmt.Errorf("invalid response format: missing data field")
	}

	// Extract details field
	details, ok := data["details"].(map[string]interface{})
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi response format: missing or invalid details field")
		return nil, fmt.Errorf("invalid response format: missing details field")
	}

	// Extract required fields from details
	signingPublicKey, ok := details["signing_public_key"].(string)
	if !ok || signingPublicKey == "" {
		return nil, fmt.Errorf("invalid or missing signing_public_key in response")
	}

	// Extract fields from details
	detailsURL, _ := details["url"].(string)
	detailsType, _ := details["type"].(string)
	detailsDomain, _ := details["domain"].(string)
	detailsSubscriberID, _ := details["subscriber_id"].(string)

	// Validate network memberships if configured.
	networkMemberships := extractStringSlice(ctx, "network_memberships", data["network_memberships"])
	if len(c.config.AllowedNetworkIDs) > 0 {
		if len(networkMemberships) == 0 || !containsAny(networkMemberships, c.config.AllowedNetworkIDs) {
			return nil, fmt.Errorf("registry entry with subscriber_id '%s' does not belong to any configured networks (registry.config.allowedNetworkIDs)", detailsSubscriberID)
		}
	}

	// Extract encr_public_key if available (optional field)
	encrPublicKey, _ := details["encr_public_key"].(string)

	// Extract fields from data level
	createdAt, _ := data["created_at"].(string)
	updatedAt, _ := data["updated_at"].(string)

	// Convert to Subscription format
	subscription := model.Subscription{
		Subscriber: model.Subscriber{
			SubscriberID: detailsSubscriberID,
			URL:          detailsURL,
			Domain:       detailsDomain,
			Type:         detailsType,
		},
		KeyID:            keyID, // Use original keyID from request
		SigningPublicKey: signingPublicKey,
		EncrPublicKey:    encrPublicKey, // May be empty if not provided
		Created:          parseTime(createdAt),
		Updated:          parseTime(updatedAt),
	}

	log.Debugf(ctx, "DeDi lookup successful, found subscription for subscriber: %s", detailsSubscriberID)
	return []model.Subscription{subscription}, nil
}

// parseTime converts string timestamp to time.Time
func parseTime(timeStr string) time.Time {
	if timeStr == "" {
		return time.Time{}
	}
	parsedTime, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return time.Time{}
	}
	return parsedTime
}

func extractStringSlice(ctx context.Context, fieldName string, value interface{}) []string {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for i, item := range v {
			str, ok := item.(string)
			if !ok {
				log.Warnf(ctx, "Ignoring invalid %s entry at index %d during registry lookup: expected a string network ID, got %T. This entry will not be considered for allowlist validation.", fieldName, i, item)
				continue
			}
			if str != "" {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

func containsAny(values []string, allowed []string) bool {
	if len(values) == 0 || len(allowed) == 0 {
		return false
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, entry := range allowed {
		if entry == "" {
			continue
		}
		allowedSet[entry] = struct{}{}
	}
	for _, value := range values {
		if _, ok := allowedSet[value]; ok {
			return true
		}
	}
	return false
}
