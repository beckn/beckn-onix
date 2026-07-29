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
	"fmt"
	"net/url"

	"github.com/beckn-one/beckn-onix/pkg/crawler/config"
	"github.com/beckn-one/beckn-onix/pkg/crawler/fetch"
	"github.com/beckn-one/beckn-onix/pkg/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/crawler/runner"
	"github.com/beckn-one/beckn-onix/pkg/crawler/source"
	"github.com/beckn-one/beckn-onix/pkg/crawler/store"
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
// New (and its closer) own the crawler-process daemon component (see
// docs/crawler-logs.md): both drivers get an identical crawler.daemon.{ready,
// failed,stopping,stopped} contract without repeating it, so a start failure
// reads the same whether it came from the plugin or the standalone cmd.
func New(ctx context.Context, s config.Settings, opts Options) (*runner.Engine, func() error, error) {
	log := opts.Logger
	if log == nil {
		log = runner.NopLogger{}
	}

	db, err := store.Open(s.DBDSN)
	if err != nil {
		log.Error("crawler failed to start while opening the database: "+err.Error(),
			"component", "daemon", "stage", "failed", "at", "db_open", "error", err.Error())
		return nil, nil, fmt.Errorf("crawler: opening db: %w", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		_ = db.Close()
		log.Error("crawler failed to start while migrating the database: "+err.Error(),
			"component", "daemon", "stage", "failed", "at", "db_migrate", "error", err.Error())
		return nil, nil, fmt.Errorf("crawler: migrate: %w", err)
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
		log.Info("crawler stopping", "component", "daemon", "stage", "stopping")
		stopErr := eng.Stop()
		if cerr := db.Close(); cerr != nil && stopErr == nil {
			stopErr = cerr
		}
		log.Info("crawler stopped", "component", "daemon", "stage", "stopped")
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
	log.Info(fmt.Sprintf("crawler started — polling %d source(s) every %s, pushing to %s",
		len(s.IndexURLs), s.IndexInterval.String(), pushHost),
		"component", "daemon", "stage", "ready",
		"source_mode", sourceMode, "push_host", pushHost,
		"sources", len(s.IndexURLs),
		"index_interval", s.IndexInterval.String(), "catalog_interval", s.CatalogInterval.String(),
		"max_attempts", s.MaxAttempts)
	return eng, closer, nil
}
