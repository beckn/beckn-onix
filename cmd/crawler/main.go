// Command crawler runs the crawler as a standalone, config-driven
// worker — the framework-agnostic "second driver" over the same
// pkg/crawler module the onix plugin uses. All config comes from
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

	crawler "github.com/beckn-one/beckn-onix/pkg/crawler"
	"github.com/beckn-one/beckn-onix/pkg/crawler/config"
	"github.com/beckn-one/beckn-onix/pkg/crawler/runner"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	s, err := config.LoadSettings(os.Getenv)
	if err != nil {
		// crawler.New owns the rest of the daemon lifecycle; config load happens
		// before it, so this is the one failed the driver still emits.
		logger.Error("crawler failed to start while loading config: "+err.Error(),
			"component", "daemon", "stage", "failed", "at", "config", "error", err.Error())
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	metrics, err := crawler.NewOTelMetrics(otel.Meter("crawler"))
	if err != nil {
		metrics = runner.NopMetrics{}
	}

	// New logs crawler.daemon.ready on success / crawler.daemon.failed on
	// db open/migrate failure, so the driver just exits on error.
	eng, closer, err := crawler.New(ctx, s, crawler.Options{
		Logger:  crawler.NewSlogLogger(logger),
		Metrics: metrics,
		NewID:   func() string { return uuid.NewString() },
	})
	if err != nil {
		os.Exit(1)
	}
	defer closer() //nolint:errcheck // best-effort stop + db close on shutdown (logs daemon.stopping/stopped)

	if err := eng.Start(ctx); err != nil {
		logger.Error("crawler failed to start while launching the jobs: "+err.Error(),
			"component", "daemon", "stage", "failed", "at", "start", "error", err.Error())
		os.Exit(1)
	}

	<-ctx.Done()
}
