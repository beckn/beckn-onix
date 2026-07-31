// Package crawler is the composition root: it wires the concrete
// adapters (fetch, decode, publish, source, store) to the orchestration runner
// from a resolved config.Settings, and holds the telemetry adapters. It is the
// only file that names every concrete type; every other package depends only
// on the pure domain (catalog) and its own concern.
//
// The framework-agnostic crawler engine lives in the sub-packages; drivers (the
// onix plugin, the standalone cmd) build one via New.
package crawler

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/config"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/fetch"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/runner"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/source"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/store"
)

// Crawler is what New hands back: the supervisor surface a driver drives. Both
// the running engine and the inert disabled crawler satisfy it, so a driver
// needs no special case for "the operator did not turn this on".
type Crawler interface {
	Start(ctx context.Context) error
	Stop() error
	// CrawlRegistry runs an immediate registry-backed crawl: discover the
	// providers of the given networks under registryURL (via the DeDi /query
	// endpoint) and crawl each — the same registry-based input the scheduled pass
	// uses. Returns a run_id for correlating the crawl's async log lines.
	CrawlRegistry(ctx context.Context, registryURL string, networkIDs []string) (string, error)
}

// ErrDisabled is returned by the disabled crawler's CrawlRegistry: the plugin is
// loaded but the operator has not switched the crawler on.
var ErrDisabled = errors.New("crawler: disabled (set CRAWLER_ENABLED=true to run it)")

// ErrNoRegistry is returned by New when an enabled crawler is built without a
// RegistryLookup. The registry is the crawler's only source of publisher signing
// keys, and signature verification is mandatory and fails closed, so a crawler
// without one would fetch nothing and park every catalog it ever saw. Failing at
// startup turns that silent, all-day outage into one actionable message.
var ErrNoRegistry = errors.New("crawler: a registry plugin is required when the crawler is enabled (catalog signatures are verified against the publisher's registry key); configure the registry plugin on the crawl handler")

// Options are the collaborators the composition root can't derive from Settings
// (all optional; the runner fills sane defaults for Logger/Metrics).
type Options struct {
	// Registry resolves publisher signing keys. REQUIRED for an enabled crawler:
	// it is the trust anchor for every catalog file signature, and there is no
	// hand-configured key list to fall back on.
	Registry          fetch.KeyRegistry
	Validate          runner.Validator // optional /push schema validator
	Logger            runner.Logger    // optional (defaults to a no-op)
	Metrics           runner.Metrics   // optional (defaults to a no-op)
	NewID             func() string    // per-call id generator (uuid)
	AllowPrivateFetch bool             // allow loopback/private fetch hosts (tests only)
	// KeyCacheTTL bounds how long a resolved registry key is reused. Zero uses
	// fetch.DefaultKeyCacheTTL.
	KeyCacheTTL time.Duration
}

// disabledCrawler is the inert Crawler returned when Settings.Enabled is false.
// It touches no database and starts no job; the only observable difference from
// a running crawler is that an on-demand crawl says why it did nothing.
type disabledCrawler struct{}

func (disabledCrawler) Start(context.Context) error { return nil }
func (disabledCrawler) Stop() error                 { return nil }
func (disabledCrawler) CrawlRegistry(context.Context, string, []string) (string, error) {
	return "", ErrDisabled
}

// New builds a ready-to-Start crawler from Settings: it opens + migrates the
// configured store backend, constructs the fetch + push clients, resolves the
// source, and wires them to the runner. The returned closer stops the crawler
// and closes the backend; call it after Stop-worthy shutdown.
//
// A disabled crawler (Settings.Enabled false) short-circuits all of that: no
// backend is selected, opened or migrated, so no DSN is needed to construct one.
//
// New (and its closer) own the crawler-process daemon component (see
// docs/crawler-logs.md): both drivers get an identical crawler.daemon.{ready,
// failed,stopping,stopped} contract without repeating it, so a start failure
// reads the same whether it came from the plugin or the standalone cmd.
func New(ctx context.Context, s config.Settings, opts Options) (Crawler, func() error, error) {
	log := opts.Logger
	if log == nil {
		log = runner.NopLogger{}
	}

	if !s.Enabled {
		log.Info("crawler disabled (set CRAWLER_ENABLED=true to run it)",
			"component", "daemon", "stage", "disabled")
		return disabledCrawler{}, func() error { return nil }, nil
	}

	// Trust anchor first, before anything is opened: the fetch client's signature
	// gate fails closed, so a crawler that cannot reach the registry to resolve
	// publisher keys is not "less strict", it is completely inert. Refuse here
	// rather than let an operator discover it from a parked-catalog count hours
	// later. There is deliberately no flag to skip verification.
	if opts.Registry == nil {
		log.Error("crawler failed to start: no registry configured to resolve publisher keys",
			"component", "daemon", "stage", "failed", "at", "registry",
			"error", ErrNoRegistry.Error())
		return nil, nil, ErrNoRegistry
	}

	// The backend is chosen by name, so Postgres is one implementation rather
	// than a hardcoded dependency of the crawler.
	backend, err := store.NewBackend(s.StoreProvider, store.Config{DSN: s.DBDSN})
	if err != nil {
		log.Error("crawler failed to start while opening the database: "+err.Error(),
			"component", "daemon", "stage", "failed", "at", "db_open",
			"store_provider", s.StoreProvider, "error", err.Error())
		return nil, nil, fmt.Errorf("crawler: opening store: %w", err)
	}
	if err := backend.Migrate(ctx); err != nil {
		_ = backend.Close()
		log.Error("crawler failed to start while migrating the database: "+err.Error(),
			"component", "daemon", "stage", "failed", "at", "db_migrate",
			"store_provider", s.StoreProvider, "error", err.Error())
		return nil, nil, fmt.Errorf("crawler: migrate: %w", err)
	}

	// Keys come from the registry, the same channel the transport signature path
	// uses. Nothing about publisher keys is deployment config.
	fc := fetch.NewClient(s.FetchTimeout, s.MaxArtifactBytes, s.MaxDecompressedBytes, opts.AllowPrivateFetch,
		fetch.WithTrustedKeys(fetch.RegistryKeys(opts.Registry, opts.KeyCacheTTL)))
	pc := publish.NewClient(s.FetchTimeout)

	// The source is either the registry-backed discovery source or the static
	// config list; selectSource picks one and reports the mode + startup count
	// for the ready log. The DeDi query client is bounded by the same
	// FetchTimeout as the fetch client.
	src, sourceMode, sourceCount := selectSource(s)

	eng := runner.New(runner.EngineConfig{
		Networks:        s.NetworkIDs,
		BppURI:          s.BppURI,
		IndexInterval:   s.IndexInterval,
		CatalogInterval: s.CatalogInterval,
		MaxAttempts:     s.MaxAttempts,
		MaxPushBytes:    s.MaxPushBytes,
		MergeOnly:       s.MergeOnly,
	}, runner.Deps{
		Store:  backend,
		Source: src,
		// The on-demand /crawl builds its registry source per request (the registry
		// URL + networks come in the request body), the same way selectSource builds
		// the scheduled one — bounded by the same FetchTimeout.
		NewRegistrySource: func(registryURL string, networkIDs []string) source.Source {
			return source.NewRegistrySource(source.NewDediQueryClient(registryURL, s.FetchTimeout), networkIDs)
		},
		FetchIndex: fc.FetchIndex,
		FetchFile:  fc.FetchFile,
		Validate:   opts.Validate,
		Push: func(c context.Context, body []byte) (publish.BatchOutcome, error) {
			return pc.Push(c, s.PushEndpoint, body)
		},
		Log:     log,
		Metrics: opts.Metrics,
		NewID:   opts.NewID,
	})

	closer := func() error {
		log.Info("crawler stopping", "component", "daemon", "stage", "stopping")
		stopErr := eng.Stop()
		if cerr := backend.Close(); cerr != nil && stopErr == nil {
			stopErr = cerr
		}
		log.Info("crawler stopped", "component", "daemon", "stage", "stopped")
		return stopErr
	}

	pushHost := ""
	if u, err := url.Parse(s.PushEndpoint); err == nil {
		pushHost = u.Host
	}
	log.Info(fmt.Sprintf("crawler started — polling %d source(s) every %s, pushing to %s",
		sourceCount, s.IndexInterval.String(), pushHost),
		"component", "daemon", "stage", "ready",
		"source_mode", sourceMode, "push_host", pushHost,
		"store_provider", s.StoreProvider,
		"sources", sourceCount, "key_source", "registry",
		"index_interval", s.IndexInterval.String(), "catalog_interval", s.CatalogInterval.String(),
		"max_attempts", s.MaxAttempts)
	return eng, closer, nil
}

// selectSource picks the crawler's index source from settings and reports the
// mode + startup count for the ready log. A registry base URL WITH at least one
// network selects the registry-backed discovery source (its count is the
// networks it polls); otherwise the static config list is used (its count is the
// fixed URL list). The `len(NetworkIDs) > 0` guard mirrors config.LoadSettings'
// source-required check: a registry URL with nothing to look up in it is not a
// usable source, so a bare CRAWLER_REGISTRY_URL (which LoadSettings only accepts
// alongside a static list) falls back to that list rather than silently building
// an empty registry source that discovers nothing. The DeDi query client is
// bounded by the crawler's FetchTimeout.
func selectSource(s config.Settings) (source.Source, string, int) {
	if s.RegistryURL != "" && len(s.NetworkIDs) > 0 {
		return source.NewRegistrySource(source.NewDediQueryClient(s.RegistryURL, s.FetchTimeout), s.NetworkIDs),
			source.KindRegistry, len(s.NetworkIDs)
	}
	return source.NewConfigSource(s.IndexURLs), source.KindConfig, len(s.IndexURLs)
}
