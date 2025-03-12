package main

import (
	"context"
	"errors"

	"github.com/beckn/beckn-onix/shared/plugin/definition"
	"github.com/beckn/beckn-onix/shared/plugin/implementation/signer"
)

// SignerProvider implements the definition.SignerProvider interface.
type SignerProvider struct{}

// New creates a new SignerImpl instance using the provided configuration.
func (p SignerProvider) New(ctx context.Context, config map[string]string) (definition.Signer, error) {
	if ctx == nil {
		return nil, errors.New("context cannot be nil")
	}
	return signer.New(ctx, &signer.Config{})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.SignerProvider = SignerProvider{}
