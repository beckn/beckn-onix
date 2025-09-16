package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/keymanager"
)

// keyManagerProvider implements the plugin provider for the KeyManager plugin.
type keyManagerProvider struct{}

// newKeyManagerFunc is a function type that creates a new KeyManager instance.
var newKeyManagerFunc = keymanager.New

// New creates and initializes a new KeyManager instance using the provided cache, registry lookup, and configuration.
func (k *keyManagerProvider) New(ctx context.Context, cache definition.Cache, registry definition.RegistryLookup, cfg map[string]string) (definition.KeyManager, func() error, error) {
	config := &keymanager.Config{
		VaultAddr: cfg["vaultAddr"],
		KVVersion: cfg["kvVersion"],
	}
	log.Debugf(ctx, "Keymanager config mapped: %+v", cfg)
	km, cleanup, err := newKeyManagerFunc(ctx, cache, registry, config)
	if err != nil {
		log.Error(ctx, err, "Failed to initialize KeyManager")
		return nil, nil, err
	}
	log.Debugf(ctx, "KeyManager instance created successfully")
	return km, cleanup, nil
}

// Provider is the exported instance of keyManagerProvider used for plugin registration.
var Provider = keyManagerProvider{}
