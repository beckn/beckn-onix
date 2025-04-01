package main

import (
	"context"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/encrypter"
)

// encrypterProvider implements the definition.encrypterProvider interface.
type encrypterProvider struct{}

func (ep encrypterProvider) New(ctx context.Context, config map[string]string) (definition.Encrypter, func() error, error) {
	return encrypter.New(ctx)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = encrypterProvider{}
