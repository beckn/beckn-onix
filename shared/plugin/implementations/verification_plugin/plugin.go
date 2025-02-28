package main

import (
	"context"

	plugins "plugins/shared/plugin"
	verifier "plugins/shared/plugin/implementations/verification_plugin/Verifier"
)

// VerifierProvider provides instances of Verifier.
type VerifierProvider struct{}

// New initializes a new Verifier instance.
func (vp *VerifierProvider) New(ctx context.Context, config map[string]string) (plugins.Validator, error) {
	return verifier.New(ctx, &verifier.Config{})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = VerifierProvider{}
