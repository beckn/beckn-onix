package cache

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
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
	Addr   string
	UseTLS bool
}

// Cache wraps a Redis client to provide basic caching operations.
type Cache struct {
	Client  RedisClient
	metrics *CacheMetrics
}

// Error variables to describe common failure modes.
var (
	ErrEmptyConfig       = errors.New("empty config")
	ErrAddrMissing       = errors.New("missing required field 'Addr'")
	ErrCredentialMissing = errors.New("missing Redis credentials in environment")
	ErrConnectionFail    = errors.New("failed to connect to Redis")
	ErrInvalidUseTLS     = errors.New("use_tls must be a boolean")
)

// validate checks if the provided Redis configuration is valid.
func validate(cfg *Config) error {
	if cfg == nil {
		return ErrEmptyConfig
	}
	if cfg.Addr == "" {
		return ErrAddrMissing
	}

	if cfg.UseTLS != true && cfg.UseTLS != false {
		return ErrInvalidUseTLS
	}

	return nil
}

// RedisClientFunc is a function variable that creates a Redis client based on the provided configuration.
// It can be overridden for testing purposes.
var RedisClientFunc = func(cfg *Config) RedisClient {
	opts := &redis.Options{
		Addr:     cfg.Addr,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	}
	if cfg.UseTLS {
		opts.TLSConfig = &tls.Config{}
	}
	return redis.NewClient(opts)
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

	// Enable OpenTelemetry instrumentation for tracing and metrics
	// This will automatically collect Redis operation metrics and expose them via /metrics endpoint
	if redisClient, ok := client.(*redis.Client); ok {
		if err := redisotel.InstrumentTracing(redisClient); err != nil {
			// Log error but don't fail - instrumentation is optional
			log.Debugf(ctx, "Failed to instrument Redis tracing: %v", err)
		}

	}

	metrics, _ := GetCacheMetrics(ctx)

	log.Infof(ctx, "Cache connection to Redis established successfully")
	return &Cache{Client: client, metrics: metrics}, client.Close, nil
}

// Get retrieves the value for the specified key from Redis.
func (c *Cache) Get(ctx context.Context, key string) (string, error) {
	result, err := c.Client.Get(ctx, key).Result()
	if c.metrics != nil {
		attrs := []attribute.KeyValue{
			telemetry.AttrOperation.String("get"),
		}
		switch {
		case err == redis.Nil:
			c.metrics.CacheMissesTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
			c.metrics.CacheOperationsTotal.Add(ctx, 1,
				metric.WithAttributes(append(attrs, telemetry.AttrStatus.String("miss"))...))
		case err != nil:
			c.metrics.CacheOperationsTotal.Add(ctx, 1,
				metric.WithAttributes(append(attrs, telemetry.AttrStatus.String("error"))...))
		default:
			c.metrics.CacheHitsTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
			c.metrics.CacheOperationsTotal.Add(ctx, 1,
				metric.WithAttributes(append(attrs, telemetry.AttrStatus.String("hit"))...))
		}
	}
	return result, err
}

// Set stores the given key-value pair in Redis with the specified TTL (time to live).
func (c *Cache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
	spanCtx, span := tracer.Start(ctx, "redis_set")
	defer span.End()

	err := c.Client.Set(spanCtx, key, value, ttl).Err()
	c.recordOperation(spanCtx, "set", err)
	return err
}

// Delete removes the specified key from Redis.
func (c *Cache) Delete(ctx context.Context, key string) error {
	err := c.Client.Del(ctx, key).Err()
	c.recordOperation(ctx, "delete", err)
	return err
}

// Clear removes all keys in the currently selected Redis database.
func (c *Cache) Clear(ctx context.Context) error {
	return c.Client.FlushDB(ctx).Err()
}

func (c *Cache) recordOperation(ctx context.Context, op string, err error) {
	if c.metrics == nil {
		return
	}
	status := "success"
	if err != nil {
		status = "error"
	}
	c.metrics.CacheOperationsTotal.Add(ctx, 1,
		metric.WithAttributes(
			telemetry.AttrOperation.String(op),
			telemetry.AttrStatus.String(status),
		))
}
