package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/oanregistry"
)

// Defaults for settings an operator leaves out. Only parseConfig can tell
// "absent" from "explicitly zero" -- retry_max of 0 is a legitimate "do not
// retry" -- so they are applied here. The values themselves live in the
// oanregistry package so there is exactly one place to change them.
const (
	defaultEntity         = oanregistry.DefaultEntity
	defaultProviderEntity = oanregistry.DefaultProviderEntity
	defaultTimeout        = oanregistry.DefaultTimeoutSeconds
	defaultRetryMax       = oanregistry.DefaultRetryMax
	defaultRetryWaitMin   = oanregistry.DefaultRetryWaitMin
	defaultRetryWaitMax   = oanregistry.DefaultRetryWaitMax
)

// oanRegistryProvider implements the RegistryLookupProvider interface for the
// OAN registry plugin.
type oanRegistryProvider struct{}

// newOANRegistryFunc creates a new OAN registry client. Indirected for tests.
var newOANRegistryFunc = oanregistry.New

// parseConfig parses the configuration map into an oanregistry.Config, starting
// from the defaults and overriding whatever the operator supplied.
func (o oanRegistryProvider) parseConfig(config map[string]string) (*oanregistry.Config, error) {
	cfg := &oanregistry.Config{
		URL:            config["url"],
		Entity:         defaultEntity,
		ProviderEntity: defaultProviderEntity,
		Timeout:        defaultTimeout,
		RetryMax:       defaultRetryMax,
		RetryWaitMin:   defaultRetryWaitMin,
		RetryWaitMax:   defaultRetryWaitMax,
	}

	// Parse entity
	if entity, exists := config["entity"]; exists && entity != "" {
		cfg.Entity = entity
	}

	// Parse providerEntity
	if providerEntity, exists := config["providerEntity"]; exists && providerEntity != "" {
		cfg.ProviderEntity = providerEntity
	}

	// Parse cacheTTL. Absent means caching is off: the TTL is how long a
	// suspended participant keeps verifying, so it is opt-in.
	if cacheTTLStr, exists := config["cacheTTL"]; exists && cacheTTLStr != "" {
		cacheTTL, err := time.ParseDuration(cacheTTLStr)
		if err != nil {
			return nil, fmt.Errorf("invalid cacheTTL value '%s': %w", cacheTTLStr, err)
		}
		if cacheTTL < 0 {
			return nil, fmt.Errorf("cacheTTL must be non-negative, got %v", cacheTTL)
		}
		cfg.CacheTTL = cacheTTL
	}

	// Parse timeout
	if timeoutStr, exists := config["timeout"]; exists && timeoutStr != "" {
		timeout, err := strconv.Atoi(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout value '%s': %w", timeoutStr, err)
		}
		if timeout <= 0 {
			return nil, fmt.Errorf("timeout must be positive, got %d", timeout)
		}
		cfg.Timeout = timeout
	}

	// Parse retry_max
	if retryMaxStr, exists := config["retry_max"]; exists && retryMaxStr != "" {
		retryMax, err := strconv.Atoi(retryMaxStr)
		if err != nil {
			return nil, fmt.Errorf("invalid retry_max value '%s': %w", retryMaxStr, err)
		}
		if retryMax < 0 {
			return nil, fmt.Errorf("retry_max must be non-negative, got %d", retryMax)
		}
		cfg.RetryMax = retryMax
	}

	// Parse retry_wait_min
	if retryWaitMinStr, exists := config["retry_wait_min"]; exists && retryWaitMinStr != "" {
		retryWaitMin, err := time.ParseDuration(retryWaitMinStr)
		if err != nil {
			return nil, fmt.Errorf("invalid retry_wait_min value '%s': %w", retryWaitMinStr, err)
		}
		if retryWaitMin < 0 {
			return nil, fmt.Errorf("retry_wait_min must be non-negative, got %v", retryWaitMin)
		}
		cfg.RetryWaitMin = retryWaitMin
	}

	// Parse retry_wait_max
	if retryWaitMaxStr, exists := config["retry_wait_max"]; exists && retryWaitMaxStr != "" {
		retryWaitMax, err := time.ParseDuration(retryWaitMaxStr)
		if err != nil {
			return nil, fmt.Errorf("invalid retry_wait_max value '%s': %w", retryWaitMaxStr, err)
		}
		if retryWaitMax < 0 {
			return nil, fmt.Errorf("retry_wait_max must be non-negative, got %v", retryWaitMax)
		}
		cfg.RetryWaitMax = retryWaitMax
	}

	if cfg.RetryWaitMin > cfg.RetryWaitMax {
		return nil, fmt.Errorf("retry_wait_min (%v) must not exceed retry_wait_max (%v)", cfg.RetryWaitMin, cfg.RetryWaitMax)
	}

	return cfg, nil
}

// New creates a new OAN registry plugin instance.
func (o oanRegistryProvider) New(ctx context.Context, cache definition.Cache, config map[string]string) (definition.RegistryLookup, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	cfg, err := o.parseConfig(config)
	if err != nil {
		log.Errorf(ctx, err, "Failed to parse OAN registry configuration")
		return nil, nil, fmt.Errorf("failed to parse oan registry configuration: %w", err)
	}

	log.Debugf(ctx, "OAN registry config mapped: %+v", cfg)

	client, closer, err := newOANRegistryFunc(ctx, cache, cfg)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create OAN registry instance")
		return nil, nil, err
	}

	log.Infof(ctx, "OAN registry instance created successfully")
	return client, closer, nil
}

// Provider is the exported plugin instance.
var Provider = oanRegistryProvider{}
