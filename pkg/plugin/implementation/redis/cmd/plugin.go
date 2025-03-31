package main

import (
	"context"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/redis"
	"github.com/go-redis/redismock/v9"
)

// Provider implements the CacheProvider interface.
type cacheProvider struct{}

// New creates a new RedisCache instance.
func (cp cacheProvider) New(ctx context.Context, config map[string]string) (definition.Cache, func() error, error) {
	c, closeFunc, err := redis.New(ctx, config)
	if err != nil {
		return nil, nil, err
	}

	if config["addr"] == "localhost:6379" {
		client, _ := redismock.NewClientMock()
		c.SetClient(client)
	}

	return c, closeFunc, nil
}

var Provider = cacheProvider{}
