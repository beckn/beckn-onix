package main

import (
	"context"

	plugins "beckn_onix/shared/plugin"
	signer "beckn_onix/shared/plugin/implementations/signing_plugin/signer"
)

// SignerProvider implements the plugin.SignerProvider interface.
type SignerProvider struct{}

// New creates a new SignerImpl instance using the provided configuration.
func (p *SignerProvider) New(ctx context.Context, config map[string]string) (plugins.Signer, error) {
	return signer.NewSigner(ctx, &signer.SigningConfig{})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = &SignerProvider{}
