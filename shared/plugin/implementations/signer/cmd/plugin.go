package main

import (
	"context"

	"github.com/beckn/beckn-onix/shared/plugin/definition"
	"github.com/beckn/beckn-onix/shared/plugin/implementations/signer"
)

// SignerProvider implements the definition.SignerProvider interface.
type SignerProvider struct{}

// New creates a new SignerImpl instance using the provided configuration.
func (p *SignerProvider) New(ctx context.Context, config map[string]string) (definition.Signer, error) {
	return signer.NewSigner(ctx, &signer.Config{})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.SignerProvider = &SignerProvider{}
