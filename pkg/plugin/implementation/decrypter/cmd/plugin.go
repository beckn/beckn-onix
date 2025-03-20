package main

import (
	"context"
	"errors"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	decrypter "github.com/beckn/beckn-onix/pkg/plugin/implementation/decrypter"
)

// DecrypterProvider implements the definition.DecrypterProvider interface.
type DecrypterProvider struct{}

// New creates a new Decrypter instance using the provided configuration.
func (dp DecrypterProvider) New(ctx context.Context, config map[string]string) (definition.Decrypter, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}
	return decrypter.New(ctx, &decrypter.Config{})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.DecrypterProvider = DecrypterProvider{}
