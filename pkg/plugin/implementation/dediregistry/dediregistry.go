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
	BaseURL      string `yaml:"baseURL" json:"baseURL"`
	ApiKey       string `yaml:"apiKey" json:"apiKey"`
	NamespaceID  string `yaml:"namespaceID" json:"namespaceID"`
	RegistryName string `yaml:"registryName" json:"registryName"`
	RecordName   string `yaml:"recordName" json:"recordName"`
	Timeout      int    `yaml:"timeout" json:"timeout"`
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
	if cfg.BaseURL == "" {
		return fmt.Errorf("baseURL cannot be empty")
	}
	if cfg.ApiKey == "" {
		return fmt.Errorf("apiKey cannot be empty")
	}
	if cfg.NamespaceID == "" {
		return fmt.Errorf("namespaceID cannot be empty")
	}
	if cfg.RegistryName == "" {
		return fmt.Errorf("registryName cannot be empty")
	}
	if cfg.RecordName == "" {
		return fmt.Errorf("recordName cannot be empty")
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

// Lookup implements RegistryLookup interface - calls the DeDi lookup endpoint and returns Subscription.
func (c *DeDiRegistryClient) Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error) {
	lookupURL := fmt.Sprintf("%s/dedi/lookup/%s/%s/%s", 
		c.config.BaseURL, c.config.NamespaceID, c.config.RegistryName, c.config.RecordName)

	httpReq, err := retryablehttp.NewRequest("GET", lookupURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.config.ApiKey))
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

	var response model.DeDiResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	log.Debugf(ctx, "DeDi lookup request successful")
	
	// Convert DeDi response to Subscription format (essential fields only)
	subscription := model.Subscription{
		Subscriber: model.Subscriber{
			SubscriberID: response.Data.Schema.EntityName,
			URL:          response.Data.Schema.EntityURL,
		},
		SigningPublicKey: response.Data.Schema.PublicKey,
		Status:           response.Data.State,
		Created:          parseTime(response.Data.CreatedAt),
		Updated:          parseTime(response.Data.UpdatedAt),
	}
	
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