package main

import (
	"context"
	"errors"

	"beckn-onix/shared/plugin/definition"
	"beckn-onix/shared/plugin/implementation/encryption"
)

// EncrypterProvider implements the definition.EncrypterProvider interface.
type EncrypterProvider struct{}

// New creates a new Encrypter instance using the provided configuration.
func (p EncrypterProvider) New(ctx context.Context, config map[string]string) (definition.Encrypter, error) {
	if ctx == nil {
		return nil, errors.New("context cannot be nil")
	}

	// Check for required configuration fields
	if _, ok := config["publicKey"]; !ok {
		return nil, errors.New("publicKey is required in config")
	}

	// Attempt to create a new Encrypter
	encrypter, err := encryption.New(ctx, &encryption.Config{})
	if err != nil {
		return nil, err
	}

	return encrypter, nil
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.EncrypterProvider = EncrypterProvider{}
