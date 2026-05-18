package main

import (
	"context"
	"errors"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/payloadstore"
)

type provider struct{}

func (p provider) New(ctx context.Context, cache definition.Cache, namespace string, cfg map[string]string) (definition.PayloadStore, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}
	return payloadstore.New(ctx, cache, namespace, cfg)
}

// Provider is the plugin entry point looked up by the plugin manager.
var Provider = provider{}
