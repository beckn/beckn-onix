package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/registry"
)

// registryLookupProvider implements the definition.RegistryLookupProvider interface.
type registryLookupProvider struct{}

// Config holds the configuration settings for the registry plugin.
type Config struct {
	RegistryURL  string
	RetryMax     int
	LookupURL    string
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
}

func parseConfig(config map[string]string) (*Config, error) {
	// Required fields validation
	registryURL, ok := config["registeryURL"]
	if !ok || registryURL == "" {
		return nil, errors.New("config must contain 'registeryURL'")
	}

	lookupURL, ok := config["lookupURL"]
	if !ok || lookupURL == "" {
		return nil, errors.New("config must contain 'lookupURL'")
	}

	// Initialize with default values
	cfg := &Config{
		RegistryURL:  registryURL,
		LookupURL:    lookupURL,
		RetryMax:     3,
		RetryWaitMin: 1 * time.Second, // Default
		RetryWaitMax: 5 * time.Second, // Default
	}

	// Parse RetryMax
	if val, ok := config["retryMax"]; ok {
		parsedVal, _ := strconv.Atoi(val)
		cfg.RetryMax = parsedVal
	}

	// Parse RetryWaitMin
	if val, ok := config["retryWaitMin"]; ok {
		duration, err := time.ParseDuration(val)
		if err != nil || duration < 0 {
			return nil, errors.New("retryWaitMin must be a non-negative duration (e.g. '1s')")
		}
		cfg.RetryWaitMin = duration
	}

	// Parse RetryWaitMax
	if val, ok := config["retryWaitMax"]; ok {
		duration, err := time.ParseDuration(val)
		if err != nil || duration < 0 {
			return nil, errors.New("retryWaitMax must be a non-negative duration (e.g. '5s')")
		}
		cfg.RetryWaitMax = duration
	}

	// Cross-field validation
	if cfg.RetryWaitMin > cfg.RetryWaitMax {
		return nil, errors.New("retryWaitMin cannot be greater than retryWaitMax")
	}

	// URL validation
	if _, err := url.Parse(cfg.RegistryURL); err != nil {
		return nil, fmt.Errorf("invalid registeryURL: %w", err)
	}

	if _, err := url.Parse(cfg.LookupURL); err != nil {
		return nil, fmt.Errorf("invalid lookupURL: %w", err)
	}

	return cfg, nil
}

// convertToRegisteryConfig converts your custom Config to registery.Config.
func convertToRegisteryConfig(cfg *Config) *registry.Config {
	return &registry.Config{
		LookupURL: cfg.RegistryURL,
		RetryMax:  cfg.RetryMax,
	}
}

func (rp registryLookupProvider) New(ctx context.Context, config map[string]string) (definition.RegistryLookup, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	regCfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}

	// Convert to registery.Config
	registryConfig := convertToRegisteryConfig(regCfg)

	return registry.New(ctx, registryConfig)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = registryLookupProvider{}
