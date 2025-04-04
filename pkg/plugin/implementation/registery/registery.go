package registery

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

// RegistryLookup implements the RegistryLookup interface.
type RegistryLookup struct {
	Client *retryablehttp.Client
	Config *Config
}

// Config struct for RegistryLookupClient.
type Config struct {
	RegistryURL  string
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
	RetryMax     int
}

// New creates a new registryLookup instance with the given configuration.
func New(ctx context.Context, config *Config) (*RegistryLookup, func() error, error) {
	client := retryablehttp.NewClient()
	client.RetryWaitMin = config.RetryWaitMin
	client.RetryWaitMax = config.RetryWaitMax
	client.RetryMax = config.RetryMax

	r := &RegistryLookup{
		Client: client,
		Config: config,
	}

	return r, nil, nil
}

// Lookup calls the /lookup endpoint with retry and returns a slice of Subscription.
func (r *RegistryLookup) Lookup(ctx context.Context, subscription *model.Subscription) ([]model.Subscription, error) {
	lookupURL := fmt.Sprintf("%s/lookUp", r.Config.RegistryURL)

	jsonData, err := json.Marshal(subscription)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal subscription data: %w", err)
	}

	req, err := retryablehttp.NewRequest("POST", lookupURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.Client.Do(req)
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
