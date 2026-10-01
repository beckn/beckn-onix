package main

import (
	"context"
	"errors"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/cache"
)

// Compile-time proof that the Redis cache also satisfies the optional
// definition.AtomicCache capability. Consumers discover this at runtime with a
// type assertion, so nothing else would catch an accidental signature change
// on SetNX -- the assertion would just start failing silently and callers
// would quietly fall back to the non-atomic path.
var _ definition.AtomicCache = (*cache.Cache)(nil)

// cacheProvider implements the CacheProvider interface for the cache plugin.
type cacheProvider struct{}

// New creates a new cache plugin instance.
func (c cacheProvider) New(ctx context.Context, config map[string]string) (definition.Cache, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}
	// Create cache.Config directly from map - validation is handled by cache.New
	cacheConfig := &cache.Config{
		Addr:   config["addr"],
		UseTLS: config["use_tls"] == "true",
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
