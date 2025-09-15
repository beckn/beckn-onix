package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/simplekeymanager"
)

// simpleKeyManagerProvider implements the plugin provider for the SimpleKeyManager plugin.
type simpleKeyManagerProvider struct{}

// newSimpleKeyManagerFunc is a function type that creates a new SimpleKeyManager instance.
var newSimpleKeyManagerFunc = simplekeymanager.New

// New creates and initializes a new SimpleKeyManager instance using the provided cache, registry lookup, and configuration.
func (k *simpleKeyManagerProvider) New(ctx context.Context, cache definition.Cache, registry definition.RegistryLookup, cfg map[string]string) (definition.KeyManager, func() error, error) {
	config := &simplekeymanager.Config{
		NetworkParticipant: cfg["networkParticipant"],
		KeyID:              cfg["keyId"],
		SigningPrivateKey:  cfg["signingPrivateKey"],
		SigningPublicKey:   cfg["signingPublicKey"],
		EncrPrivateKey:     cfg["encrPrivateKey"],
		EncrPublicKey:      cfg["encrPublicKey"],
	}
	log.Debugf(ctx, "SimpleKeyManager config mapped: np=%s, keyId=%s, has_signing_private=%v, has_signing_public=%v, has_encr_private=%v, has_encr_public=%v",
		config.NetworkParticipant,
		config.KeyID,
		config.SigningPrivateKey != "",
		config.SigningPublicKey != "",
		config.EncrPrivateKey != "",
		config.EncrPublicKey != "")

	km, cleanup, err := newSimpleKeyManagerFunc(ctx, cache, registry, config)
	if err != nil {
		log.Error(ctx, err, "Failed to initialize SimpleKeyManager")
		return nil, nil, err
	}
	log.Debugf(ctx, "SimpleKeyManager instance created successfully")
	return km, cleanup, nil
}

// Provider is the exported instance of simpleKeyManagerProvider used for plugin registration.
var Provider = simpleKeyManagerProvider{}
