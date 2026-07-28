// Command catalog-crawler runs the crawler engine as a standalone,
// config-driven worker — the framework-agnostic "second driver" over the
// same pkg/catalogcrawler module the onix plugin uses. All config comes
// from CRAWLER_* env vars; nothing is hardcoded, and it never targets a
// real cluster on its own.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	cc "github.com/beckn-one/beckn-onix/pkg/catalogcrawler"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/state"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	s, err := cc.LoadSettings(os.Getenv)
	if err != nil {
		logger.Error("crawler.config.failed", "err", err)
		os.Exit(1)
	}

	db, err := state.Open(s.DBDSN)
	if err != nil {
		logger.Error("crawler.db.open_failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := state.Migrate(ctx, db); err != nil {
		logger.Error("crawler.db.migrate_failed", "err", err)
		os.Exit(1)
	}

	httpc := cc.NewHTTPClient(s.FetchTimeout, s.MaxArtifactBytes, false)
	metrics, err := cc.NewOTelMetrics(otel.Meter("catalogcrawler"))
	if err != nil {
		metrics = cc.NopMetrics{}
	}
	eng := cc.New(cc.Config{
		Networks:        s.NetworkIDs,
		BppURI:          s.BppURI,
		IndexInterval:   s.IndexInterval,
		CatalogInterval: s.CatalogInterval,
		MaxAttempts:     s.MaxAttempts,
		PushBatchSize:   s.PushBatchSize,
	}, cc.Deps{
		Store:      state.New(db),
		Source:     cc.NewConfigSource(s.IndexURLs),
		FetchIndex: httpc.FetchIndex,
		FetchFile:  httpc.FetchFile,
		Push: func(ctx context.Context, body []byte) (cc.PartOutcome, error) {
			return httpc.Push(ctx, s.PushEndpoint, body)
		},
		Log:     cc.NewSlogLogger(logger),
		Metrics: metrics,
		NewID:   func() string { return uuid.NewString() },
	})

	if err := eng.Start(ctx); err != nil {
		logger.Error("crawler.start_failed", "err", err)
		os.Exit(1)
	}
	logger.Info("crawler.started", "indexUrls", len(s.IndexURLs), "networks", s.NetworkIDs)

	<-ctx.Done()
	logger.Info("crawler.stopping")
	eng.Stop()
	logger.Info("crawler.stopped")
}
