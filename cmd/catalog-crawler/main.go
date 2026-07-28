// Command catalog-crawler runs the crawler as a standalone, config-driven
// worker — the framework-agnostic "second driver" over the same
// pkg/catalogcrawler module the onix plugin uses. All config comes from
// CRAWLER_* env vars; nothing is hardcoded, and it never targets a real
// cluster on its own.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	crawler "github.com/beckn-one/beckn-onix/pkg/catalogcrawler"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/config"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/runner"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	s, err := config.LoadSettings(os.Getenv)
	if err != nil {
		logger.Error("crawler.config.failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	metrics, err := crawler.NewOTelMetrics(otel.Meter("catalogcrawler"))
	if err != nil {
		metrics = runner.NopMetrics{}
	}

	eng, closer, err := crawler.New(ctx, s, crawler.Options{
		Logger:  crawler.NewSlogLogger(logger),
		Metrics: metrics,
		NewID:   func() string { return uuid.NewString() },
	})
	if err != nil {
		logger.Error("crawler.init_failed", "err", err)
		os.Exit(1)
	}
	defer closer() //nolint:errcheck // best-effort stop + db close on shutdown

	if err := eng.Start(ctx); err != nil {
		logger.Error("crawler.start_failed", "err", err)
		os.Exit(1)
	}
	logger.Info("crawler.started", "indexUrls", len(s.IndexURLs), "networks", s.NetworkIDs)

	<-ctx.Done()
	logger.Info("crawler.stopping")
}
