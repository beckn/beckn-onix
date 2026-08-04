package store

// registry.go — the small generic provider registry the factory is built on:
// implementations self-register under a name in their own init(), and callers
// create one by name. It knows nothing about Postgres (or any backend), so a
// second backend is a new file, never an edit here.

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Builder constructs one provider instance from a resolved config.
type Builder[TConfig any, TProvider any] func(TConfig) (TProvider, error)

// Registry maps a provider name to its builder. It is safe for concurrent use:
// registration happens in init(), lookups happen at composition time.
type Registry[TConfig any, TProvider any] struct {
	mu       sync.RWMutex
	builders map[string]Builder[TConfig, TProvider]
}

// NewRegistry creates an empty registry.
func NewRegistry[TConfig any, TProvider any]() *Registry[TConfig, TProvider] {
	return &Registry[TConfig, TProvider]{builders: make(map[string]Builder[TConfig, TProvider])}
}

// Register adds (or replaces) the builder for name.
func (r *Registry[TConfig, TProvider]) Register(name string, b Builder[TConfig, TProvider]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.builders[name] = b
}

// IsRegistered reports whether a provider is registered under name.
func (r *Registry[TConfig, TProvider]) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.builders[name]
	return ok
}

// Available lists the registered provider names, sorted for deterministic
// error messages and logs.
func (r *Registry[TConfig, TProvider]) Available() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.builders))
	for name := range r.builders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Create builds the provider registered under name. An unknown name is an
// operator-facing config error, so it names what is available.
func (r *Registry[TConfig, TProvider]) Create(name string, cfg TConfig) (TProvider, error) {
	r.mu.RLock()
	build, ok := r.builders[name]
	r.mu.RUnlock()

	var zero TProvider
	if !ok {
		return zero, fmt.Errorf("store: unknown provider %q (available: %s)", name, strings.Join(r.Available(), ", "))
	}
	p, err := build(cfg)
	if err != nil {
		return zero, fmt.Errorf("store: building provider %q: %w", name, err)
	}
	return p, nil
}
