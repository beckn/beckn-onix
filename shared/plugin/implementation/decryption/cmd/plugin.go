package main

import (
	"context"
	"errors"

	"beckn-onix/shared/plugin/definition"
	"beckn-onix/shared/plugin/implementation/decryption"
)

// DecrypterProvider implements the definition.DecrypterProvider interface.
type DecrypterProvider struct{}

// New creates a new Decrypter instance using the provided configuration.
func (p DecrypterProvider) New(ctx context.Context, config map[string]string) (definition.Decrypter, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}
	decrypter, cleanup, err := decryption.New(ctx, &decryption.Config{})
	if err != nil {
		return nil, nil, err
	}
	return decrypter, cleanup, nil
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.DecrypterProvider = DecrypterProvider{}
