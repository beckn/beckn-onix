package definition

import (
	"context"
	"time"
)

// Cache defines the general cache interface for caching plugins.
type Cache interface {
	// Get retrieves a value from the cache based on the given key.
	Get(ctx context.Context, key string) (string, error)

	// Set stores a value in the cache with the given key and TTL (time-to-live) in seconds.
	Set(ctx context.Context, key, value string, ttl time.Duration) error

	// Delete removes a value from the cache based on the given key.
	Delete(ctx context.Context, key string) error

	// Clear removes all values from the cache.
	Clear(ctx context.Context) error
}

// CacheProvider interface defines the contract for managing cache instances.
type CacheProvider interface {
	// New initializes a new cache instance with the given configuration.
	New(ctx context.Context, config map[string]string) (Cache, func() error, error)
}
