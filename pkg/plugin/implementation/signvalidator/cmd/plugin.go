package main

import (
	"context"
	"errors"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/signvalidator"
)

// provider provides instances of Verifier.
type provider struct{}

// New initializes a new Verifier instance.
func (vp provider) New(ctx context.Context, config map[string]string) (definition.SignValidator, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	return signvalidator.New(ctx, &signvalidator.Config{})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = provider{}
