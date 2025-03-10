package main

import (
	"context"

	"github.com/beckn/beckn-onix/shared/plugin/definition"
	"github.com/beckn/beckn-onix/shared/plugin/implementations/verifier"

	plugin "github.com/beckn/beckn-onix/shared/plugin/definition"
)

// VerifierProvider provides instances of Verifier.
type VerifierProvider struct{}

// New initializes a new Verifier instance.
func (vp *VerifierProvider) New(ctx context.Context, config map[string]string) (plugin.Validator, error) {
	return verifier.New(ctx, &verifier.Config{})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.ValidatorProvider = &VerifierProvider{}
