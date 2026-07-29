// Package runner is the crawler's orchestration: the two scheduled jobs (index
// + catalog) linked by the queue, the sync pipeline, and the retry/park policy.
// It defines the ports it consumes (fetch/push/source/validate + the
// log/metric sinks) and is wired to concrete adapters by the composition root;
// it imports the pure domain (catalog), the store, and publish helpers, never a
// fetch/decode adapter directly.
package runner

import (
	"context"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/crawler/source"
	"github.com/beckn-one/beckn-onix/pkg/crawler/store"
)

// IndexFetcher fetches + parses a publisher's catalog index, sending cond as
// If-None-Match / If-Modified-Since so an unchanged index can answer 304.
type IndexFetcher func(ctx context.Context, indexURL string, cond catalog.IndexConditions) (catalog.IndexResult, error)

// FileFetcher fetches + verifies + decodes one catalog file.
type FileFetcher func(ctx context.Context, f catalog.FileEntry) ([]byte, error)

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
// module stays framework-agnostic). Reasons passed to CatalogFailed are
// low-cardinality categories, never full error strings.
type Metrics interface {
	CatalogPushed()
	CatalogFailed(reason string)
	SetQueueDepth(n int)
	ObservePushSeconds(seconds float64)
	ObserveIndexSeconds(seconds float64)
}

// NopMetrics discards all metrics.
type NopMetrics struct{}

func (NopMetrics) CatalogPushed()              {}
func (NopMetrics) CatalogFailed(string)        {}
func (NopMetrics) SetQueueDepth(int)           {}
func (NopMetrics) ObservePushSeconds(float64)  {}
func (NopMetrics) ObserveIndexSeconds(float64) {}

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
	Store      *store.Store
	Source     source.Source
	FetchIndex IndexFetcher
	FetchFile  FileFetcher
	Validate   Validator
	Push       Pusher
	Log        Logger
	Metrics    Metrics
	Now        func() time.Time
	NewID      func() string
}
