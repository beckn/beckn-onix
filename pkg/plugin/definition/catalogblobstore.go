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
	// per-path overwrite -- as long as only one adapter process writes
	// to it. See ConditionalCatalogBlobStore otherwise.
	Put(ctx context.Context, path string, content []byte) error
}

// ErrBlobPreconditionFailed is the sentinel a
// ConditionalCatalogBlobStore.PutIfVersion implementation must return
// when the blob's current version is not the one the caller named.
// Aliased from catalog-core for the same reason as ErrBlobNotFound.
var ErrBlobPreconditionFailed = store.ErrPreconditionFailed

// ConditionalCatalogBlobStore is an optional capability of a
// CatalogBlobStore: a versioned read and a write that only happens
// while that version is still current. A backend that more than one
// adapter process can write to (a shared volume, a bucket) must
// implement it: catalogstore.Store detects it with a type assertion and
// then compare-and-swaps the catalog index, so replicas no longer drop
// each other's entries. A backend that only implements CatalogBlobStore
// keeps working, for a single adapter process.
//
// It is the same method set as catalog-core's store.ConditionalBlobStore.
type ConditionalCatalogBlobStore interface {
	CatalogBlobStore

	// GetVersioned returns the bytes at path and an opaque, non-empty
	// token naming that exact content generation, or ErrBlobNotFound.
	GetVersioned(ctx context.Context, path string) (content []byte, version string, err error)

	// PutIfVersion writes content at path only if the blob's current
	// version is ifVersion (empty: only if nothing is stored there),
	// atomically with respect to every other PutIfVersion on that path,
	// and returns the new version. Otherwise it writes nothing and
	// returns ErrBlobPreconditionFailed.
	PutIfVersion(ctx context.Context, path string, content []byte, ifVersion string) (newVersion string, err error)
}

// CatalogBlobStoreProvider is the plugin constructor interface.
type CatalogBlobStoreProvider interface {
	New(ctx context.Context, config map[string]string) (CatalogBlobStore, func() error, error)
}
