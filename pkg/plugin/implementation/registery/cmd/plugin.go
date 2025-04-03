package main

import (
	"context"
	"errors"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/registery"
)

// registryLookupProvider implements the definition.RegistryLookupProvider interface.
type registryLookupProvider struct{}

func (rp registryLookupProvider) New(ctx context.Context, config map[string]string) (definition.RegistryLookup, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	// Extract RegisteryURL from the config map
	registeryURL, ok := config["registeryURL"]
	if !ok || registeryURL == "" {
		return nil, nil, errors.New("config must contain 'registeryURL'")
	}
	// Create and return a new RegistryLookupClient instance with the provided configuration
	client, cleanup, err := registery.New(ctx, &registery.Config{RegistryURL: registeryURL})
	if err != nil {
		return nil, nil, err
	}

	return client, cleanup, nil
	//return registery.New(ctx)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.RegistryLookupProvider = registryLookupProvider{}
