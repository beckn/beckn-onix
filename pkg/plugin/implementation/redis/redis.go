package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisNewClient is a package-level variable for redis.NewClient.
var RedisNewClient = redis.NewClient

// Cache implements the Cache interface using Redis.
type Cache struct {
	client *redis.Client
}

// New creates a new RedisCache instance and returns a close function.
func New(ctx context.Context, config map[string]string) (*Cache, func() error, error) {
	addr, ok := config["addr"]
	if !ok {
		return nil, nil, fmt.Errorf("missing required config 'addr'")
	}

	password, ok := config["password"]
	if !ok {
		password = ""
	}

	client := RedisNewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		//DB: 0 (default) is used for caching simplicity and isolation.
		DB: 0,
	})

	if _, err := client.Ping(ctx).Result(); err != nil {
		return nil, nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &Cache{client: client}, client.Close, nil
}

// GetClient is a getter method to get the redis client.
func (c *Cache) GetClient() *redis.Client {
	return c.client
}

// SetClient is a setter method to set the redis client.
func (c *Cache) SetClient(client *redis.Client) {
	c.client = client
}

// Get retrieves a value from Redis.
func (c *Cache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

// Set stores a value in Redis with a TTL.
func (c *Cache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

// Delete removes a value from Redis.
func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

// Clear removes all values from Redis.
func (c *Cache) Clear(ctx context.Context) error {
	return c.client.FlushDB(ctx).Err()
}
