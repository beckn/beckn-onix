package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	plugins "plugins/shared/plugin"
	"plugins/shared/plugin/implementations/signing_plugin/Signer"
)

// parseConfig converts a map of configuration values into a SigningConfig struct.
func parseConfig(config map[string]string) (Signer.SigningConfig, error) {
	ttlStr, exists := config["ttl"]
	if !exists {
		return Signer.SigningConfig{}, errors.New("ttl not found in config")
	}

	ttl, err := strconv.ParseInt(ttlStr, 10, 64)
	if err != nil {
		return Signer.SigningConfig{}, fmt.Errorf("invalid ttl value: %w", err)
	}

	return Signer.SigningConfig{TTL: ttl}, nil
}

// SignerProvider implements the plugin.SignerProvider interface.
type SignerProvider struct{}

// New creates a new SignerImpl instance using the provided configuration.
func (p SignerProvider) New(ctx context.Context, config map[string]string) (plugins.Signer, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return Signer.NewSigner(ctx, cfg)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = SignerProvider{}
