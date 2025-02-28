package main

import (
	"context"

	plugins "plugins/shared/plugin"
	"plugins/shared/plugin/implementations/verification_plugin/Verifier"
)

// VerifierProvider provides instances of Verifier.
type VerifierProvider struct{}

// New initializes a new Verifier instance.
func (vp *VerifierProvider) New(ctx context.Context, config map[string]string) (plugins.Validator, error) {
	return Verifier.New(ctx, &Verifier.Config{})
}

var Provider = VerifierProvider{}
