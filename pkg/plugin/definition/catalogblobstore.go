package definition

import (
	"context"

	"github.com/beckn/catalog-core/pkg/catalog/store"
)

// ErrBlobNotFound is the sentinel a CatalogBlobStore.Get implementation
// must return when path has never been written. It's the same sentinel
// catalog-core's store.Store checks for (via errors.Is) to tell "nothing
// here yet, start fresh" apart from a real backend failure -- aliased
// here rather than redeclared so every onix CatalogBlobStore plugin
// stays compatible with it without importing catalog-core directly.
var ErrBlobNotFound = store.ErrBlobNotFound

// CatalogBlobStore is the only backend-specific capability a catalog
// storage backend needs: read/write bytes at a path. It carries no
// catalog-file vocabulary at all -- every backend (local disk, S3, GCS,
// git, an authenticated CDN write root) implements exactly this and
// nothing more. catalogstore.Store is the one shared layer that
// understands how a catalog index, its baseline, change files, and
// "latest" pointer fit together, built on top of whichever
// CatalogBlobStore is configured -- that understanding is common across
// every backend, so it is deliberately not part of this interface.
type CatalogBlobStore interface {
	// Get returns the bytes stored at path, or ErrBlobNotFound if nothing
	// has been written there yet.
	Get(ctx context.Context, path string) ([]byte, error)

	// Put writes content at path, creating or overwriting it. Every path
	// catalogstore.Store passes is already content/version-addressed (a
	// versioned filename, or the fixed "latest"/index path), so a
	// backend needs no versioning or locking of its own beyond an atomic
	// per-path overwrite.
	Put(ctx context.Context, path string, content []byte) error
}

// CatalogBlobStoreProvider is the plugin constructor interface.
type CatalogBlobStoreProvider interface {
	New(ctx context.Context, config map[string]string) (CatalogBlobStore, func() error, error)
}
