package main

import (
	"context"
	"errors"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/cache"
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
	log.Debugf(ctx, "Cache config mapped: %+v", cacheConfig)
	cache, closer, err := cache.New(ctx, cacheConfig)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create cache instance")
		return nil, nil, err
	}

	log.Infof(ctx, "Cache instance created successfully")
	return cache, closer, nil
}

// Provider is the exported plugin instance
var Provider = cacheProvider{}
