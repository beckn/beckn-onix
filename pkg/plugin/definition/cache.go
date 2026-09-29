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

// AtomicCache is an OPTIONAL capability a Cache implementation may also
// provide. It is deliberately a separate interface rather than extra methods
// on Cache: adding a method to Cache would break every third-party cache
// plugin compiled against the current interface, whereas callers can discover
// this one with a type assertion and degrade gracefully when it is absent.
//
// Consumers that need atomicity should assert for it:
//
//	if ac, ok := cache.(definition.AtomicCache); ok { ... }
//
// The replay guard (core/module/handler/replayguard.go) is the first consumer:
// a plain Get-then-Set cannot decide "was I the first to claim this key"
// because two concurrent callers both observe a miss and both proceed.
type AtomicCache interface {
	Cache

	// SetNX stores value at key with the given TTL only if key is not already
	// present, and reports whether this call was the one that stored it.
	//
	// It returns (true, nil) when the key was absent and is now set, and
	// (false, nil) when the key already existed and was left untouched. A
	// non-nil error means the operation could not be completed and the
	// returned bool carries no meaning.
	//
	// Implementations MUST perform the check and the store as one atomic
	// operation, so that exactly one of N concurrent callers for the same key
	// receives true.
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
}
