package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/secretskeymanager"
)

// keyMgrProvider implements the KeyManagerProvider interface.
type keyMgrProvider struct{}

// New creates a new KeyManager instance.
func (kp keyMgrProvider) New(ctx context.Context, cache definition.Cache, registry definition.RegistryLookup, config map[string]string) (definition.KeyManager, func() error, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid config: %w", err)
	}

	return secretskeymanager.New(ctx, cache, registry, cfg)
}

// parseConfig converts the map[string]string to the keyManager.Config struct.
func parseConfig(config map[string]string) (*secretskeymanager.Config, error) {
	projectID, exists := config["projectID"]
	if !exists {
		return nil, errors.New("projectID not found in config")
	}

	return &secretskeymanager.Config{
		ProjectID: projectID,
	}, nil
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = keyMgrProvider{}
