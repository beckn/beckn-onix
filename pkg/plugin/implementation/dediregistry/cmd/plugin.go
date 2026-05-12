package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/dediregistry"
)

// dediRegistryProvider implements the RegistryLookupProvider interface for the DeDi registry plugin.
type dediRegistryProvider struct {
	newFunc func(ctx context.Context, cfg *dediregistry.Config) (*dediregistry.DeDiRegistryClient, func() error, error)
}

func (d dediRegistryProvider) parseConfig(config map[string]string) (*dediregistry.Config, error) {
	dediConfig := &dediregistry.Config{
		URL:          config["url"],
		RegistryName: config["registryName"],
	}

	// Parse timeout if provided.
	if timeoutStr, exists := config["timeout"]; exists && timeoutStr != "" {
		timeout, err := strconv.Atoi(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout value '%s': %w", timeoutStr, err)
		}
		if timeout <= 0 {
			return nil, fmt.Errorf("timeout must be positive, got %d", timeout)
		}
		dediConfig.Timeout = timeout
	}

	// Parse retry_max if provided.
	if retryMaxStr, exists := config["retry_max"]; exists && retryMaxStr != "" {
		retryMax, err := strconv.Atoi(retryMaxStr)
		if err != nil {
			return nil, fmt.Errorf("invalid retry_max value '%s': %w", retryMaxStr, err)
		}
		if retryMax < 0 {
			return nil, fmt.Errorf("retry_max must be non-negative, got %d", retryMax)
		}
		dediConfig.RetryMax = retryMax
	}

	// Parse retry_wait_min if provided.
	if retryWaitMinStr, exists := config["retry_wait_min"]; exists && retryWaitMinStr != "" {
		retryWaitMin, err := time.ParseDuration(retryWaitMinStr)
		if err != nil {
			return nil, fmt.Errorf("invalid retry_wait_min value '%s': %w", retryWaitMinStr, err)
		}
		if retryWaitMin < 0 {
			return nil, fmt.Errorf("retry_wait_min must be non-negative, got %v", retryWaitMin)
		}
		dediConfig.RetryWaitMin = retryWaitMin
	}

	// Parse retry_wait_max if provided.
	if retryWaitMaxStr, exists := config["retry_wait_max"]; exists && retryWaitMaxStr != "" {
		retryWaitMax, err := time.ParseDuration(retryWaitMaxStr)
		if err != nil {
			return nil, fmt.Errorf("invalid retry_wait_max value '%s': %w", retryWaitMaxStr, err)
		}
		if retryWaitMax < 0 {
			return nil, fmt.Errorf("retry_wait_max must be non-negative, got %v", retryWaitMax)
		}
		dediConfig.RetryWaitMax = retryWaitMax
	}

	// Validate retry wait bounds relationship.
	if dediConfig.RetryWaitMin > 0 && dediConfig.RetryWaitMax > 0 && dediConfig.RetryWaitMin > dediConfig.RetryWaitMax {
		return nil, fmt.Errorf("retry_wait_min (%v) must not exceed retry_wait_max (%v)", dediConfig.RetryWaitMin, dediConfig.RetryWaitMax)
	}

	return dediConfig, nil
}

// New creates a new DeDi registry plugin instance.
func (d dediRegistryProvider) New(ctx context.Context, config map[string]string) (definition.RegistryLookup, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	dediConfig, err := d.parseConfig(config)
	if err != nil {
		log.Errorf(ctx, err, "Failed to parse DeDi registry configuration")
		return nil, nil, fmt.Errorf("failed to parse DeDi registry configuration: %w", err)
	}

	allowedNetworkIDs, err := resolveAllowedNetworkIDs(config)
	if err != nil {
		return nil, nil, err
	}
	dediConfig.AllowedNetworkIDs = allowedNetworkIDs

	log.Debugf(ctx, "DeDi Registry config mapped: %+v", dediConfig)

	dediClient, closer, err := d.newFunc(ctx, dediConfig)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create DeDi registry instance")
		return nil, nil, err
	}

	log.Infof(ctx, "DeDi Registry instance created successfully")
	return dediClient, closer, nil
}

func parseAllowedNetworkIDs(raw string) []string {
	parts := strings.Split(raw, ",")
	networkIDs := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		networkIDs = append(networkIDs, item)
	}
	return networkIDs
}

func resolveAllowedNetworkIDs(config map[string]string) ([]string, error) {
	if rawParentNamespaces, exists := config["allowedParentNamespaces"]; exists && rawParentNamespaces != "" {
		if _, hasAllowedNetworkIDs := config["allowedNetworkIDs"]; !hasAllowedNetworkIDs {
			return nil, fmt.Errorf("config key 'allowedParentNamespaces' is no longer supported; use 'allowedNetworkIDs' with full network IDs")
		}
	}

	if rawNetworkIDs, exists := config["allowedNetworkIDs"]; exists && rawNetworkIDs != "" {
		return parseAllowedNetworkIDs(rawNetworkIDs), nil
	}

	return nil, nil
}

// Provider is the exported plugin instance
var Provider = dediRegistryProvider{newFunc: dediregistry.New}
