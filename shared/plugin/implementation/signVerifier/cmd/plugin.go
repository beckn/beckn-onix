package main

import (
	"context"
	"errors"

	"github.com/beckn/beckn-onix/shared/plugin/definition"

	plugin "github.com/beckn/beckn-onix/shared/plugin/definition"
	verifier "github.com/beckn/beckn-onix/shared/plugin/implementation/signVerifier"
)

// VerifierProvider provides instances of Verifier.
type VerifierProvider struct{}

// New initializes a new Verifier instance.
func (vp VerifierProvider) New(ctx context.Context, config map[string]string) (plugin.Verifier, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	return verifier.New(ctx, &verifier.Config{})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.VerifierProvider = VerifierProvider{}
