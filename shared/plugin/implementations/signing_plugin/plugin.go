package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	plugins "beckn_onix/shared/plugin"
	signer "beckn_onix/shared/plugin/implementations/signing_plugin/signer"
)

// parseConfig converts a map of configuration values into a SigningConfig struct.
func parseConfig(config map[string]string) (signer.SigningConfig, error) {
	ttlStr, exists := config["ttl"]
	if !exists {
		return signer.SigningConfig{}, errors.New("ttl not found in config")
	}

	ttl, err := strconv.ParseInt(ttlStr, 10, 64)
	if err != nil {
		return signer.SigningConfig{}, fmt.Errorf("invalid ttl value: %w", err)
	}

	return signer.SigningConfig{TTL: ttl}, nil
}

// SignerProvider implements the plugin.SignerProvider interface.
type SignerProvider struct{}

// New creates a new SignerImpl instance using the provided configuration.
func (p SignerProvider) New(ctx context.Context, config map[string]string) (plugins.Signer, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return signer.NewSigner(ctx, &cfg)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = SignerProvider{}
