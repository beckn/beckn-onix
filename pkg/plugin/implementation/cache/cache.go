package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

// Global variable for the Redis client, can be overridden in tests
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

type Config struct {
	Addr string
}

type Cache struct {
	Client RedisClient
}

var (
	ErrEmptyConfig       = errors.New("empty config")
	ErrAddrMissing       = errors.New("missing required field 'Addr'")
	ErrCredentialMissing = errors.New("missing Redis credentials in environment")
	ErrConnectionFail    = errors.New("failed to connect to Redis")
)

func validate(cfg *Config) error {
	if cfg == nil {
		return ErrEmptyConfig
	}
	if cfg.Addr == "" {
		return ErrAddrMissing
	}
	return nil
}

var RedisClientFunc = func(cfg *Config) RedisClient {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})
}

func New(ctx context.Context, cfg *Config) (*Cache, func() error, error) {
	if err := validate(cfg); err != nil {
		return nil, nil, err
	}

	client := RedisClientFunc(cfg)

	if _, err := client.Ping(ctx).Result(); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrConnectionFail, err)
	}

	return &Cache{Client: client}, client.Close, nil
}

func (c *Cache) Get(ctx context.Context, key string) (string, error) {
	return c.Client.Get(ctx, key).Result()
}

func (c *Cache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.Client.Set(ctx, key, value, ttl).Err()
}

func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.Client.Del(ctx, key).Err()
}

func (c *Cache) Clear(ctx context.Context) error {
	return c.Client.FlushDB(ctx).Err()
}
