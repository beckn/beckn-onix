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
func New(ctx context.Context, s config.Settings, opts Options) (*runner.Engine, func() error, error) {
	db, err := store.Open(s.DBDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("catalogcrawler: opening db: %w", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		_ = db.Close()
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
		Log:     opts.Logger,
		Metrics: opts.Metrics,
		NewID:   opts.NewID,
	})

	closer := func() error {
		stopErr := eng.Stop()
		if cerr := db.Close(); cerr != nil && stopErr == nil {
			stopErr = cerr
		}
		return stopErr
	}
	return eng, closer, nil
}
