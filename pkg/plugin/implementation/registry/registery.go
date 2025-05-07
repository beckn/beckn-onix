package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/beckn/beckn-onix/pkg/model"

	"github.com/hashicorp/go-retryablehttp"
)

type registryLookup struct {
	client *retryablehttp.Client
	config *Config
}

// Config struct for RegistryLookupClient.
type Config struct {
	LookupURL    string
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
	RetryMax     int
}

// validateConfig checks if the provided configuration is valid.
func validateConfig(config *Config) error {
	if config.LookupURL == "" {
		return errors.New("RegistryURL cannot be empty")
	}
	if config.RetryWaitMin < 0 {
		return errors.New("RetryWaitMin must be non-negative")
	}
	if config.RetryWaitMax < 0 {
		return errors.New("RetryWaitMax must be non-negative")
	}
	if config.RetryWaitMin > config.RetryWaitMax {
		return errors.New("RetryWaitMin cannot be greater than RetryWaitMax")
	}
	if config.RetryMax < 0 {
		return errors.New("RetryMax must be non-negative")
	}
	return nil
}

// New creates a new registryLookup instance with the given configuration.
func New(ctx context.Context, config *Config) (*registryLookup, func() error, error) {
	// Validate the configuration
	if err := validateConfig(config); err != nil {
		return nil, nil, err
	}
	client := retryablehttp.NewClient()
	client.RetryWaitMin = config.RetryWaitMin
	client.RetryWaitMax = config.RetryWaitMax
	client.RetryMax = config.RetryMax

	r := &registryLookup{
		client: client,
		config: config,
	}
	return r, nil, nil
}

// Lookup performs a POST request to the /lookUp endpoint of the registry service with retry logic.
func (r *registryLookup) Lookup(ctx context.Context, subscription *model.Subscription) ([]model.Subscription, error) {
	lookupURL := r.config.LookupURL
	jsonData, err := json.Marshal(subscription)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal subscription data: %w", err)
	}
	req, err := retryablehttp.NewRequest("POST", lookupURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
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
