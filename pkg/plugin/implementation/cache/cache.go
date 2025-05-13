package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

type Config struct {
	Addr string
}

type Cache struct {
	client *redis.Client
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

func New(ctx context.Context, cfg *Config) (*Cache, func() error, error) {
	if err := validate(cfg); err != nil {
		return nil, nil, err
	}

	// Get password from environment variable
	password := os.Getenv("REDIS_PASSWORD")
	// Allow empty password for local testing
	// if password == "" {
	// 	return nil, nil, ErrCredentialMissing
	// }

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: password,
		DB:       0, // Always use default DB 0
	})

	if _, err := client.Ping(ctx).Result(); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrConnectionFail, err)
	}

	return &Cache{client: client}, client.Close, nil
}

func (c *Cache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

func (c *Cache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

func (c *Cache) Clear(ctx context.Context) error {
	return c.client.FlushDB(ctx).Err()
}
