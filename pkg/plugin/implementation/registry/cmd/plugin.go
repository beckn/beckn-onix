package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/registry"
)

// registryProvider implements the RegistryLookupProvider interface for the registry plugin.
type registryProvider struct{}

// New creates a new registry plugin instance.
func (r registryProvider) New(ctx context.Context, config map[string]string) (definition.RegistryLookup, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	// Parse configuration from map
	registryConfig := &registry.Config{
		URL: config["url"],
	}

	// Parse optional retry settings
	if retryMaxStr, exists := config["retry_max"]; exists && retryMaxStr != "" {
		if retryMax, err := strconv.Atoi(retryMaxStr); err == nil {
			registryConfig.RetryMax = retryMax
		}
	}

	if retryWaitMinStr, exists := config["retry_wait_min"]; exists && retryWaitMinStr != "" {
		if retryWaitMin, err := time.ParseDuration(retryWaitMinStr); err == nil {
			registryConfig.RetryWaitMin = retryWaitMin
		}
	}

	if retryWaitMaxStr, exists := config["retry_wait_max"]; exists && retryWaitMaxStr != "" {
		if retryWaitMax, err := time.ParseDuration(retryWaitMaxStr); err == nil {
			registryConfig.RetryWaitMax = retryWaitMax
		}
	}

	log.Debugf(ctx, "Registry config mapped: %+v", registryConfig)

	registryClient, closer, err := registry.New(ctx, registryConfig)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create registry instance")
		return nil, nil, err
	}

	log.Infof(ctx, "Registry instance created successfully")
	return registryClient, closer, nil
}

// Provider is the exported plugin instance
var Provider = registryProvider{}
