package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/registry"
)

// registryProvider implements the RegistryLookupProvider interface for the registry plugin.
type registryProvider struct{}

// newRegistryFunc is a function type that creates a new Registry instance.
var newRegistryFunc = registry.New

// parseConfig parses the configuration map and returns a registry.Config with optional parameters.
func (r registryProvider) parseConfig(config map[string]string) (*registry.Config, error) {
	registryConfig := &registry.Config{
		URL: config["url"],
	}

	// Parse retry_max
	if retryMaxStr, exists := config["retry_max"]; exists && retryMaxStr != "" {
		retryMax, err := strconv.Atoi(retryMaxStr)
		if err != nil {
			return nil, fmt.Errorf("invalid retry_max value '%s': %w", retryMaxStr, err)
		}
		registryConfig.RetryMax = retryMax
	}

	// Parse retry_wait_min
	if retryWaitMinStr, exists := config["retry_wait_min"]; exists && retryWaitMinStr != "" {
		retryWaitMin, err := time.ParseDuration(retryWaitMinStr)
		if err != nil {
			return nil, fmt.Errorf("invalid retry_wait_min value '%s': %w", retryWaitMinStr, err)
		}
		registryConfig.RetryWaitMin = retryWaitMin
	}

	// Parse retry_wait_max
	if retryWaitMaxStr, exists := config["retry_wait_max"]; exists && retryWaitMaxStr != "" {
		retryWaitMax, err := time.ParseDuration(retryWaitMaxStr)
		if err != nil {
			return nil, fmt.Errorf("invalid retry_wait_max value '%s': %w", retryWaitMaxStr, err)
		}
		registryConfig.RetryWaitMax = retryWaitMax
	}

	return registryConfig, nil
}

// New creates a new registry plugin instance.
func (r registryProvider) New(ctx context.Context, config map[string]string) (definition.RegistryLookup, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	// Parse configuration from map using the dedicated method
	registryConfig, err := r.parseConfig(config)
	if err != nil {
		log.Errorf(ctx, err, "Failed to parse registry configuration")
		return nil, nil, fmt.Errorf("failed to parse registry configuration: %w", err)
	}

	log.Debugf(ctx, "Registry config mapped: %+v", registryConfig)

	registryClient, closer, err := newRegistryFunc(ctx, registryConfig)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create registry instance")
		return nil, nil, err
	}

	log.Infof(ctx, "Registry instance created successfully")
	return registryClient, closer, nil
}

// Provider is the exported plugin instance
var Provider = registryProvider{}
