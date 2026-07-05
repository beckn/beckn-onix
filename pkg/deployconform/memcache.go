// In-memory cache satisfying the plugin Cache interface, so the standalone
// verifier can reuse the manifestloader plugin (and its signature
// verification path) without a Redis instance.
package deployconform

import (
	"context"
	"sync"
	"time"
)

// memCache is a minimal TTL cache for single-process, short-lived use.
type memCache struct {
	mu      sync.Mutex
	entries map[string]memEntry
}

// memEntry is one cached value and its expiry instant (zero = no expiry).
type memEntry struct {
	value     string
	expiresAt time.Time
}

// newMemCache returns an empty in-memory cache.
func newMemCache() *memCache {
	return &memCache{entries: make(map[string]memEntry)}
}

// Get returns the cached value for key, or "" when absent or expired.
func (c *memCache) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || (!entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt)) {
		delete(c.entries, key)
		return "", nil
	}
	return entry.value, nil
}

// Set stores value under key with the given TTL (0 or negative = no expiry).
func (c *memCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := memEntry{value: value}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}
	c.entries[key] = entry
	return nil
}

// Delete removes key from the cache.
func (c *memCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	return nil
}

// Clear removes every entry from the cache.
func (c *memCache) Clear(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]memEntry)
	return nil
}
