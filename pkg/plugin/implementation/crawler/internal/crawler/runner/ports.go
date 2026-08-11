// Package runner is the crawler's orchestration: the two scheduled jobs (index
// + catalog) linked by the queue, the sync pipeline, and the retry/park policy.
// It defines the ports it consumes (store/fetch/push/source/validate + the
// log/metric sinks) and is wired to concrete adapters by the composition root;
// it imports the pure domain (catalog) and publish helpers, never a storage,
// fetch or decode adapter directly.
package runner

import (
	"context"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/source"
)

// Store is the crawler's persistence port: the whole surface the two jobs need
// to keep their state (the per-index change gate + cadence, the work queue, and
// the per-catalog version cursor with its pass history). Every signature is
// stated in domain terms (catalog.*) only, so a backend is genuinely pluggable:
// the runner never sees a driver handle, a DSN, or a storage-package type. The
// composition root picks the implementation by name; store/postgres.go is the
// one shipped today.
type Store interface {
	// Catalog cursor + pass history. version is the content-lineage cursor,
	// entryVersion the entry-level cursor -- independent (RFC NFH-014
	// §Versioning; see catalog/change.go).
	GetCatalogVersion(ctx context.Context, catalogID string) (version, entryVersion int64, seen bool, err error)
	UpsertCatalog(ctx context.Context, c catalog.CatalogState) error
	CountParked(ctx context.Context) (int, error)
	CountTracked(ctx context.Context) (int, error)
	GetCatalogReports(ctx context.Context, catalogID string) ([]catalog.PassReport, error)
	RecordFailure(ctx context.Context, catalogID, indexURL, participantID string, report catalog.PassReport) error

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
	Complete(ctx context.Context, id, claimID string, toVersion int64, c catalog.CatalogState) error
	QueueDepth(ctx context.Context) (int, error)
}

// IndexFetcher fetches + parses a publisher's catalog index, sending cond as
// If-None-Match / If-Modified-Since so an unchanged index can answer 304.
type IndexFetcher func(ctx context.Context, indexURL string, cond catalog.IndexConditions) (catalog.IndexResult, error)

// FileFetcher fetches + verifies + decodes one catalog file. nodeID is the
// enclosing index's publishing-node identity, used to resolve the file's
// self-signature against the registry. catalogID is the enclosing index
// entry's own catalogId, cross-checked against the file's internal
// catalogId/version (RFC NFH-014 CON-TBD-12).
type FileFetcher func(ctx context.Context, nodeID, catalogID string, f catalog.FileEntry) ([]byte, error)

// Validator schema-validates the /push request body before it is sent (Phase 1;
// reuses onix's schemav2validator). A nil error means valid.
type Validator func(ctx context.Context, pushBody []byte) error

// Pusher pushes one /push body to Discovery and reports the outcome.
type Pusher func(ctx context.Context, body []byte) (publish.BatchOutcome, error)

// Logger is the minimal structured-log sink the runner needs. It is injected
// (no process-global logger), so an onix plugin passes its own. Debug carries
// the interior/success/trace vocabulary (§9b) — off by default at INFO.
type Logger interface {
	Debug(event string, kv ...any)
	Info(event string, kv ...any)
	Warn(event string, kv ...any)
	Error(event string, kv ...any)
}

// NopLogger discards all events (handy default / for tests).
type NopLogger struct{}

func (NopLogger) Debug(string, ...any) {}
func (NopLogger) Info(string, ...any)  {}
func (NopLogger) Warn(string, ...any)  {}
func (NopLogger) Error(string, ...any) {}

// Metrics is the runner's metrics sink (injected; NopMetrics by default so the
// module stays framework-agnostic). All label values (outcome, fault, job,
// result) are low-cardinality categories, never full error strings, catalog
// ids, urls, or versions — those are high-cardinality and belong in traces/logs.
type Metrics interface {
	RecordSyncOutcome(outcome, fault string) // one labeled counter; replaces CatalogPushed + CatalogFailed
	MarkPassSuccess(job string)              // liveness: last time the crawl/sync loop completed a tick
	SetQueueDepth(n int)                     // backlog gauge
	SetCatalogsParked(n int)                 // gauge of permanently-failed items
	SetCatalogsTracked(n int)                // gauge of catalogs we track
	ObservePushSeconds(seconds float64)      // push latency
	ObserveIndexSeconds(seconds float64)     // index-crawl latency
	ObserveSyncLagSeconds(seconds float64)   // queue-residence lag: synced_at - enqueued_at
	RecordIndexPoll(result string)           // per index poll: updated/unchanged/not_modified/unreachable
}

// NopMetrics discards all metrics.
type NopMetrics struct{}

func (NopMetrics) RecordSyncOutcome(string, string) {}
func (NopMetrics) MarkPassSuccess(string)           {}
func (NopMetrics) SetQueueDepth(int)                {}
func (NopMetrics) SetCatalogsParked(int)            {}
func (NopMetrics) SetCatalogsTracked(int)           {}
func (NopMetrics) ObservePushSeconds(float64)       {}
func (NopMetrics) ObserveIndexSeconds(float64)      {}
func (NopMetrics) ObserveSyncLagSeconds(float64)    {}
func (NopMetrics) RecordIndexPoll(string)           {}

// EngineConfig is the engine's tunables (all config-driven, no hardcodes).
type EngineConfig struct {
	Networks        []string      // crawler's networkIds (selection + on-demand default)
	BppURI          string        // publisher URI for the push context
	IndexInterval   time.Duration // index-job cadence
	CatalogInterval time.Duration // catalog-job cadence
	MaxAttempts     int           // give up a catalog after this many failed pushes
	MaxPushBytes    int64         // max /push body size Discovery accepts (0 => default 10 MiB)
	MergeOnly       bool          // this version: always MERGE + delta-from-changefile (FULL/removals deferred)
}

// Deps are the engine's injected collaborators.
type Deps struct {
	Store  Store
	Source source.Source
	// NewRegistrySource builds an ad-hoc registry-backed source for an on-demand
	// /crawl (a registry URL + networks supplied per request), the same way the
	// scheduled source is built at composition. It lets CrawlRegistry discover
	// index URLs via the DeDi /query endpoint without the engine knowing how a
	// query client is constructed. Injected so tests can stand in a fake.
	NewRegistrySource func(registryURL string, networkIDs []string) source.Source
	FetchIndex        IndexFetcher
	FetchFile         FileFetcher
	Validate          Validator
	Push              Pusher
	Log               Logger
	Metrics           Metrics
	Now               func() time.Time
	NewID             func() string
}
