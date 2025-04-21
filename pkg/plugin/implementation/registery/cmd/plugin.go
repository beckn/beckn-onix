package main

import (
	"context"
	"errors"
	"strconv"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/registery"
)

// registryLookupProvider implements the definition.RegistryLookupProvider interface.
type registryLookupProvider struct{}

// Config holds the configuration settings for the registry plugin.
type Config struct {
	RegistryURL string
	RetryMax    int
}

func parseConfig(config map[string]string) (*Config, error) {
	registryURL, ok := config["registeryURL"]
	if !ok || registryURL == "" {
		return nil, errors.New("config must contain 'registeryURL'")
	}

	var retryMax int

	// Parse RetryMax
	if val, ok := config["retryMax"]; ok {
		if parsedVal, err := strconv.Atoi(val); err == nil {
			retryMax = parsedVal
		}
	}

	return &Config{
		RegistryURL: registryURL,
		RetryMax:    retryMax,
	}, nil
}

// convertToRegisteryConfig converts your custom Config to registery.Config.
func convertToRegisteryConfig(cfg *Config) *registery.Config {
	return &registery.Config{
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

	return registery.New(ctx, registryConfig)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = registryLookupProvider{}
