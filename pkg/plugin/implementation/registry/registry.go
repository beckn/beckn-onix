package registry

import (
	"bytes"
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

// Config holds configuration parameters for the registry client.
type Config struct {
	URL          string        `yaml:"url" json:"url"`
	RetryMax     int           `yaml:"retry_max" json:"retry_max"`
	RetryWaitMin time.Duration `yaml:"retry_wait_min" json:"retry_wait_min"`
	RetryWaitMax time.Duration `yaml:"retry_wait_max" json:"retry_wait_max"`
}

// RegistryClient encapsulates the logic for calling the registry endpoints.
type RegistryClient struct {
	config *Config
	client *retryablehttp.Client
}

// validate checks if the provided registry configuration is valid.
func validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("registry config cannot be nil")
	}
	if cfg.URL == "" {
		return fmt.Errorf("registry URL cannot be empty")
	}
	return nil
}

// New creates a new instance of RegistryClient.
func New(ctx context.Context, cfg *Config) (*RegistryClient, func() error, error) {
	log.Debugf(ctx, "Initializing Registry client with config: %+v", cfg)

	if err := validate(cfg); err != nil {
		return nil, nil, err
	}

	rc := retryablehttp.NewClient()

	// Configure retry settings if provided
	if cfg.RetryMax > 0 {
		rc.RetryMax = cfg.RetryMax
	}
	if cfg.RetryWaitMin > 0 {
		rc.RetryWaitMin = cfg.RetryWaitMin
	}
	if cfg.RetryWaitMax > 0 {
		rc.RetryWaitMax = cfg.RetryWaitMax
	}

	client := &RegistryClient{
		config: cfg,
		client: rc,
	}

	// Cleanup function
	closer := func() error {
		log.Debugf(ctx, "Cleaning up Registry client resources")
		if client.client != nil {
			client.client.HTTPClient.CloseIdleConnections()
		}
		return nil
	}

	log.Infof(ctx, "Registry client is created successfully")
	return client, closer, nil
}

// Subscribe calls the /subscribe endpoint with retry.
func (c *RegistryClient) Subscribe(ctx context.Context, subscription *model.Subscription) error {
	subscribeURL := fmt.Sprintf("%s/subscribe", c.config.URL)

	jsonData, err := json.Marshal(subscription)
	if err != nil {
		return model.NewBadReqErr(fmt.Errorf("failed to marshal subscription data: %w", err))
	}

	req, err := retryablehttp.NewRequest("POST", subscribeURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)

	log.Debugf(ctx, "Making subscribe request to: %s", subscribeURL)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send subscribe request with retry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Errorf(ctx, nil, "Subscribe request failed with status: %s, response: %s", resp.Status, string(body))
		return fmt.Errorf("subscribe request failed with status: %s", resp.Status)
	}

	log.Debugf(ctx, "Subscribe request is initiated successfully")
	return nil
}

// Lookup calls the /lookup endpoint with retry and returns a slice of Subscription.
func (c *RegistryClient) Lookup(ctx context.Context, subscription *model.Subscription) ([]model.Subscription, error) {
	lookupURL := fmt.Sprintf("%s/lookup", c.config.URL)

	jsonData, err := json.Marshal(subscription)
	if err != nil {
		return nil, model.NewBadReqErr(fmt.Errorf("failed to marshal subscription data: %w", err))
	}

	req, err := retryablehttp.NewRequest("POST", lookupURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)

	log.Debugf(ctx, "Making lookup request to: %s", lookupURL)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send lookup request with retry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Errorf(ctx, nil, "Lookup request failed with status: %s, response: %s", resp.Status, string(body))
		return nil, fmt.Errorf("lookup request failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var results []model.Subscription
	err = json.Unmarshal(body, &results)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	log.Debugf(ctx, "Lookup request successful, found %d subscriptions", len(results))
	return results, nil
}
