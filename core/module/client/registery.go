package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/hashicorp/go-retryablehttp"
)

// Config struct to hold configuration parameters.
type Config struct {
	RegisteryURL string
	RetryMax     int
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
}

// RegisteryClient encapsulates the logic for calling the subscribe and lookup endpoints.
type RegisteryClient struct {
	Config *Config
	Client *retryablehttp.Client
}

// NewRegisteryClient creates a new instance of Client.
func NewRegisteryClient(config *Config) *RegisteryClient {
	retryClient := retryablehttp.NewClient()

	return &RegisteryClient{Config: config, Client: retryClient}
}

// Subscribe calls the /subscribe endpoint with retry.
func (c *RegisteryClient) Subscribe(ctx context.Context, subscription *model.Subscription) error {
	subscribeURL := fmt.Sprintf("%s/subscribe", c.Config.RegisteryURL)

	jsonData, err := json.Marshal(subscription)
	if err != nil {
		return fmt.Errorf("failed to marshal subscription data: %w", err)
	}

	req, err := retryablehttp.NewRequest("POST", subscribeURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request with retry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("subscribe request failed with status: %s", resp.Status)
	}
	return nil
}

// Lookup calls the /lookup endpoint with retry and returns a slice of Subscription.
func (c *RegisteryClient) Lookup(ctx context.Context, subscription *model.Subscription) ([]model.Subscription, error) {
	lookupURL := fmt.Sprintf("%s/lookUp", c.Config.RegisteryURL)

	jsonData, err := json.Marshal(subscription)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal subscription data: %w", err)
	}

	req, err := retryablehttp.NewRequest("POST", lookupURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request with retry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
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

	return results, nil
}
