package main

import (
	"context"
	"errors"
	"strconv"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/cache"
)

// cacheProvider implements the definition.CacheProvider interface.
type cacheProvider struct{}

// Config holds the configuration settings for the Redis cache plugin.
type Config struct {
	Addr     string // Redis address (host:port)
	DB       int    // Redis database number (optional, defaults to 0)
	Password string // Redis password (optional, can be empty or from env)
}

// parseConfig converts the string map configuration to a Config struct.
func parseConfig(config map[string]string) (*Config, error) {
	addr, ok := config["addr"]
	if !ok || addr == "" {
		return nil, errors.New("config must contain 'addr'")
	}

	// Default values
	db := 0
	password := ""

	// Parse DB if provided
	if val, ok := config["db"]; ok && val != "" {
		if parsedVal, err := strconv.Atoi(val); err == nil {
			db = parsedVal
		}
	}

	// Get password if provided
	if val, ok := config["password"]; ok {
		password = val
	}

	return &Config{
		Addr:     addr,
		DB:       db,
		Password: password,
	}, nil
}

// convertToRedisConfig converts the plugin Config to redis.Config.
func convertToRedisConfig(cfg *Config) *cache.Config {
	return &cache.Config{
		Addr: cfg.Addr,
	}
}

// New initializes a new Redis cache with the given configuration.
func (p cacheProvider) New(ctx context.Context, config map[string]string) (definition.Cache, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}

	// Convert to redis.Config
	redisConfig := convertToRedisConfig(cfg)

	return cache.New(ctx, redisConfig)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = cacheProvider{}
