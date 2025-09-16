package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/redis/go-redis/v9"
)

// RedisCl global variable for the Redis client, can be overridden in tests
var RedisCl *redis.Client

// RedisClient is an interface for Redis operations that allows mocking
type RedisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	FlushDB(ctx context.Context) *redis.StatusCmd
	Ping(ctx context.Context) *redis.StatusCmd
	Close() error
}

// Config holds the configuration required to connect to Redis.
type Config struct {
	Addr string
}

// Cache wraps a Redis client to provide basic caching operations.
type Cache struct {
	Client RedisClient
}

// Error variables to describe common failure modes.
var (
	ErrEmptyConfig       = errors.New("empty config")
	ErrAddrMissing       = errors.New("missing required field 'Addr'")
	ErrCredentialMissing = errors.New("missing Redis credentials in environment")
	ErrConnectionFail    = errors.New("failed to connect to Redis")
)

// validate checks if the provided Redis configuration is valid.
func validate(cfg *Config) error {
	if cfg == nil {
		return ErrEmptyConfig
	}
	if cfg.Addr == "" {
		return ErrAddrMissing
	}
	return nil
}

// RedisClientFunc is a function variable that creates a Redis client based on the provided configuration.
// It can be overridden for testing purposes.
var RedisClientFunc = func(cfg *Config) RedisClient {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})
}

// New initializes and returns a Cache instance along with a close function to release resources.
func New(ctx context.Context, cfg *Config) (*Cache, func() error, error) {
	log.Debugf(ctx, "Initializing Cache with config: %+v", cfg)
	if err := validate(cfg); err != nil {
		return nil, nil, err
	}

	client := RedisClientFunc(cfg)

	if _, err := client.Ping(ctx).Result(); err != nil {
		log.Errorf(ctx, err, "Failed to ping Redis server")
		return nil, nil, fmt.Errorf("%w: %v", ErrConnectionFail, err)
	}

	log.Infof(ctx, "Cache connection to Redis established successfully")
	return &Cache{Client: client}, client.Close, nil
}

// Get retrieves the value for the specified key from Redis.
func (c *Cache) Get(ctx context.Context, key string) (string, error) {
	return c.Client.Get(ctx, key).Result()
}

// Set stores the given key-value pair in Redis with the specified TTL (time to live).
func (c *Cache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.Client.Set(ctx, key, value, ttl).Err()
}

// Delete removes the specified key from Redis.
func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.Client.Del(ctx, key).Err()
}

// Clear removes all keys in the currently selected Redis database.
func (c *Cache) Clear(ctx context.Context) error {
	return c.Client.FlushDB(ctx).Err()
}
