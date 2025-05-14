package main

import (
	"context"
	"errors"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/cache"
)

// cacheProvider implements the CacheProvider interface for the cache plugin.
type cacheProvider struct{}

// New creates a new cache plugin instance.
func (c cacheProvider) New(ctx context.Context, config map[string]string) (definition.Cache, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	// Create cache.Config directly from map - validation is handled by cache.New
	cacheConfig := &cache.Config{
		Addr: config["addr"],
	}
	
	return cache.New(ctx, cacheConfig)
}

// Provider is the exported plugin instance
var Provider definition.CacheProvider = cacheProvider{}