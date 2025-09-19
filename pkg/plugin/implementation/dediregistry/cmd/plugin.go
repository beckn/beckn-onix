package main

import (
	"context"
	"errors"
	"strconv"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/dediregistry"
)

// dediRegistryProvider implements the RegistryLookupProvider interface for the DeDi registry plugin.
type dediRegistryProvider struct{}

// New creates a new DeDi registry plugin instance.
func (d dediRegistryProvider) New(ctx context.Context, config map[string]string) (definition.RegistryLookup, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	// Create dediregistry.Config directly from map - validation is handled by dediregistry.New
	dediConfig := &dediregistry.Config{
		BaseURL:      config["baseURL"],
		ApiKey:       config["apiKey"],
		NamespaceID:  config["namespaceID"],
		RegistryName: config["registryName"],
		RecordName:   config["recordName"],
	}

	// Parse timeout if provided
	if timeoutStr, exists := config["timeout"]; exists && timeoutStr != "" {
		if timeout, err := strconv.Atoi(timeoutStr); err == nil {
			dediConfig.Timeout = timeout
		}
	}

	log.Debugf(ctx, "DeDi Registry config mapped: %+v", dediConfig)
	
	dediClient, closer, err := dediregistry.New(ctx, dediConfig)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create DeDi registry instance")
		return nil, nil, err
	}

	log.Infof(ctx, "DeDi Registry instance created successfully")
	return dediClient, closer, nil
}

// Provider is the exported plugin instance
var Provider = dediRegistryProvider{}