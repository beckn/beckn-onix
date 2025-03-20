package main

import (
	"context"
	"errors"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/encrypter"
)

// EncrypterProvider implements the definition.EncrypterProvider interface.
type EncrypterProvider struct{}

func (ep EncrypterProvider) New(ctx context.Context, config map[string]string) (definition.Encrypter, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}
	cfg := &encrypter.Config{}
	return encrypter.New(ctx, cfg)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.EncrypterProvider = EncrypterProvider{}
