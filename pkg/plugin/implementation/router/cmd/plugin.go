package main

import (
	"context"
	"errors"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/router"
)

// RouterProvider provides instances of Router.
type RouterProvider struct{}

// New initializes a new Router instance.
func (rp RouterProvider) New(ctx context.Context, config map[string]string) (definition.Router, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	// Parse the routingConfig key from the config map
	routingConfig, ok := config["routingConfig"]
	if !ok {
		return nil, nil, errors.New("routingConfig is required in the configuration")
	}
	return router.New(ctx, &router.Config{
		RoutingConfig: routingConfig,
	})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = RouterProvider{}
