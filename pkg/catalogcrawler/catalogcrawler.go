// Package catalogcrawler is the composition root: it wires the concrete
// adapters (fetch, decode, publish, source, store) to the orchestration runner
// from a resolved config.Settings, and holds the telemetry adapters. It is the
// only file that names every concrete type; every other package depends only
// on the pure domain (catalog) and its own concern.
//
// The framework-agnostic crawler engine lives in the sub-packages; drivers (the
// onix plugin, the standalone cmd) build one via New.
package catalogcrawler

import (
	"context"
	"fmt"
	"net/url"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/config"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/fetch"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/runner"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/source"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/store"
)

// Options are the collaborators the composition root can't derive from Settings
// (all optional; the runner fills sane defaults for Logger/Metrics).
type Options struct {
	Validate          runner.Validator // optional /push schema validator
	Logger            runner.Logger    // optional (defaults to a no-op)
	Metrics           runner.Metrics   // optional (defaults to a no-op)
	NewID             func() string    // per-call id generator (uuid)
	AllowPrivateFetch bool             // allow loopback/private fetch hosts (tests only)
}

// New builds a ready-to-Start crawler Engine from Settings: it opens + migrates
// the store, constructs the fetch + push clients, resolves the source, and
// wires them to the runner. The returned closer stops the engine and closes the
// DB; call it after Stop-worthy shutdown.
//
// New (and its closer) own the crawler-process daemon lifecycle (§9b,
// lifecycle=daemon): both drivers get an identical crawler.daemon.{ready,
// start_failed,stopping,stopped} contract without repeating it, so a start
// failure reads the same whether it came from the plugin or the standalone cmd.
func New(ctx context.Context, s config.Settings, opts Options) (*runner.Engine, func() error, error) {
	log := opts.Logger
	if log == nil {
		log = runner.NopLogger{}
	}

	db, err := store.Open(s.DBDSN)
	if err != nil {
		log.Error("crawler.daemon.start_failed",
			"lifecycle", "daemon", "state", "start_failed", "stage", "db_open", "error", err.Error())
		return nil, nil, fmt.Errorf("catalogcrawler: opening db: %w", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		_ = db.Close()
		log.Error("crawler.daemon.start_failed",
			"lifecycle", "daemon", "state", "start_failed", "stage", "db_migrate", "error", err.Error())
		return nil, nil, fmt.Errorf("catalogcrawler: migrate: %w", err)
	}

	fc := fetch.NewClient(s.FetchTimeout, s.MaxArtifactBytes, s.MaxDecompressedBytes, opts.AllowPrivateFetch)
	pc := publish.NewClient(s.FetchTimeout)

	eng := runner.New(runner.EngineConfig{
		Networks:        s.NetworkIDs,
		BppURI:          s.BppURI,
		IndexInterval:   s.IndexInterval,
		CatalogInterval: s.CatalogInterval,
		MaxAttempts:     s.MaxAttempts,
		MaxPushBytes:    s.MaxPushBytes,
		MergeOnly:       s.MergeOnly,
	}, runner.Deps{
		Store:      store.New(db),
		Source:     source.NewConfigSource(s.IndexURLs),
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
		log.Info("crawler.daemon.stopping", "lifecycle", "daemon", "state", "stopping")
		stopErr := eng.Stop()
		if cerr := db.Close(); cerr != nil && stopErr == nil {
			stopErr = cerr
		}
		log.Info("crawler.daemon.stopped", "lifecycle", "daemon", "state", "stopped")
		return stopErr
	}

	sourceMode := "static"
	if s.RegistryURL != "" {
		sourceMode = "registry"
	}
	pushHost := ""
	if u, err := url.Parse(s.PushEndpoint); err == nil {
		pushHost = u.Host
	}
	log.Info("crawler.daemon.ready",
		"lifecycle", "daemon", "state", "ready",
		"source_mode", sourceMode, "sources_count", len(s.IndexURLs), "networks", s.NetworkIDs,
		"push_host", pushHost,
		"index_interval", s.IndexInterval.String(), "catalog_interval", s.CatalogInterval.String(),
		"max_artifact_bytes", s.MaxArtifactBytes, "max_decompressed_bytes", s.MaxDecompressedBytes,
		"max_attempts", s.MaxAttempts)
	return eng, closer, nil
}
