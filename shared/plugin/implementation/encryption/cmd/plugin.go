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
	return encryption.New(ctx, &encryption.Config{})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.EncrypterProvider = EncrypterProvider{}
