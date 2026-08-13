package store

// provider.go — the pluggable-backend contract: what a crawler persistence
// backend must implement (Backend) and the backend-agnostic config a builder
// receives. Postgres is one implementation (postgres.go), selected by name
// through the factory; nothing outside this package names a driver.

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"time"
)

// Backend is a crawler persistence backend: the runner's Store port plus the
// lifecycle the composition root owns (schema migration and shutdown).
//
// The state methods mirror runner.Store exactly. They are restated here rather
// than embedded because this package cannot import runner: the runner's own
// tests import this package, and Go forbids the resulting test-time cycle. The
// mirror is not maintained by hand: provider_test.go asserts at compile time
// that Backend satisfies runner.Store, so the two drift only if that test is
// deleted. Like the port, every signature is in domain terms (catalog.*) only.
type Backend interface {
	// Catalog cursor + pass history.
	GetCatalogVersion(ctx context.Context, catalogID string) (version, entryVersion int64, seen bool, err error)
	UpsertCatalog(ctx context.Context, c catalog.CatalogState) error
	CountParked(ctx context.Context) (int, error)
	CountTracked(ctx context.Context) (int, error)
	GetCatalogReports(ctx context.Context, catalogID string) ([]catalog.PassReport, error)
	RecordFailure(ctx context.Context, catalogID, indexURL, participantID string, report catalog.PassReport) error
	GetCatalogEnvelope(ctx context.Context, catalogID string) (descriptor, provider []byte, catalogType, participantID string, ok bool, err error)

	// Per-index state: the change gate, the conditional-GET validators, cadence.
	GetIndex(ctx context.Context, indexURL string) (*catalog.IndexState, error)
	KnownIndexes(ctx context.Context) ([]catalog.KnownIndex, error)
	UpsertIndex(ctx context.Context, indexURL, participantID, source string, syncStatus string, nextCrawlAt time.Time, etag, lastModified string) error
	AdvanceIndexCadence(ctx context.Context, indexURL string, nextCrawlAt time.Time) error

	// Work queue: coalescing enqueue, atomic claim, retry/park, settle.
	Enqueue(ctx context.Context, item catalog.QueueItem) error
	ClaimNext(ctx context.Context) (*catalog.ClaimedItem, error)
	RescheduleQueueItem(ctx context.Context, id, claimID string, nextAttemptAt time.Time) error
	ParkQueueItem(ctx context.Context, id, claimID string) error
	Complete(ctx context.Context, id, claimID string, toVersion, entryVersion int64, c catalog.CatalogState) error
	QueueDepth(ctx context.Context) (int, error)

	// Migrate brings the backend's schema up to date. It is idempotent, so the
	// composition root runs it on every start.
	Migrate(ctx context.Context) error
	// Close releases the backend's resources. The backend owns whatever handle
	// it opened, so a driver never holds one.
	Close() error
}

// Config is the backend-agnostic connection config a builder receives. It stays
// deliberately small: anything backend-specific belongs in the DSN, so adding a
// backend never widens this struct.
type Config struct {
	DSN string // connection string for the selected backend (CRAWLER_DB_DSN)
}
