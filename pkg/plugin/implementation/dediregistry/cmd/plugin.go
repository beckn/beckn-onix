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
type dediRegistryProvider struct{}

// newDediRegistryFunc creates a new DeDi registry instance.
var newDediRegistryFunc = dediregistry.New

func (d dediRegistryProvider) parseConfig(ctx context.Context, config map[string]string) *dediregistry.Config {
	dediConfig := &dediregistry.Config{
		URL:          config["url"],
		RegistryName: config["registryName"],
	}

	// Parse timeout if provided.
	if timeoutStr, exists := config["timeout"]; exists && timeoutStr != "" {
		if timeout, err := strconv.Atoi(timeoutStr); err == nil {
			dediConfig.Timeout = timeout
		} else {
			log.Warnf(ctx, "Invalid timeout value '%s', using default", timeoutStr)
		}
	}

	// Parse retry_max if provided.
	if retryMaxStr, exists := config["retry_max"]; exists && retryMaxStr != "" {
		if retryMax, err := strconv.Atoi(retryMaxStr); err == nil {
			dediConfig.RetryMax = retryMax
		} else {
			log.Warnf(ctx, "Invalid retry_max value '%s', using default", retryMaxStr)
		}
	}

	// Parse retry_wait_min if provided.
	if retryWaitMinStr, exists := config["retry_wait_min"]; exists && retryWaitMinStr != "" {
		if retryWaitMin, err := time.ParseDuration(retryWaitMinStr); err == nil {
			dediConfig.RetryWaitMin = retryWaitMin
		} else {
			log.Warnf(ctx, "Invalid retry_wait_min value '%s', using default", retryWaitMinStr)
		}
	}

	// Parse retry_wait_max if provided.
	if retryWaitMaxStr, exists := config["retry_wait_max"]; exists && retryWaitMaxStr != "" {
		if retryWaitMax, err := time.ParseDuration(retryWaitMaxStr); err == nil {
			dediConfig.RetryWaitMax = retryWaitMax
		} else {
			log.Warnf(ctx, "Invalid retry_wait_max value '%s', using default", retryWaitMaxStr)
		}
	}

	return dediConfig
}

// New creates a new DeDi registry plugin instance.
func (d dediRegistryProvider) New(ctx context.Context, config map[string]string) (definition.RegistryLookup, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	dediConfig := d.parseConfig(ctx, config)
	allowedNetworkIDs, err := resolveAllowedNetworkIDs(config)
	if err != nil {
		return nil, nil, err
	}
	dediConfig.AllowedNetworkIDs = allowedNetworkIDs

	log.Debugf(ctx, "DeDi Registry config mapped: %+v", dediConfig)

	dediClient, closer, err := newDediRegistryFunc(ctx, dediConfig)
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
var Provider = dediRegistryProvider{}
