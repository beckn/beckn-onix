// Package crawler is the thin onix plugin adapter over the
// framework-agnostic crawler in pkg/crawler. It resolves settings from
// the plugin config map (with env taking precedence so secrets like the DB DSN
// stay out of YAML), injects the schema validator, builds + starts the engine
// via the crawler composition root, and returns a closer that stops it.
package crawler

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	crawler "github.com/beckn-one/beckn-onix/pkg/crawler"
	"github.com/beckn-one/beckn-onix/pkg/crawler/config"
	"github.com/beckn-one/beckn-onix/pkg/crawler/runner"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// New builds + starts the crawler engine. Config values come from the plugin
// config map, with env overriding, so secrets stay in env. validator (optional)
// performs Phase-1 catalog schema validation before push.
func New(ctx context.Context, validator definition.SchemaValidator, cfg map[string]string) (definition.Crawler, func() error, error) {
	get := func(k string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return cfg[k]
	}

	logger := crawler.NewSlogLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	settings, err := config.LoadSettings(get)
	if err != nil {
		// crawler.New owns the rest of the daemon lifecycle; config load happens
		// before it, so this is the one start_failed the driver still emits.
		logger.Error("crawler.daemon.start_failed",
			"lifecycle", "daemon", "state", "start_failed", "stage", "config", "error", err.Error())
		return nil, nil, fmt.Errorf("crawler: config: %w", err)
	}

	var validate runner.Validator
	if validator != nil {
		// schemav2validator keys on reqURL.Path to select the action schema;
		// the crawler validates the /push body against catalog/publish.
		action := get("CRAWLER_SCHEMA_ACTION")
		if action == "" {
			action = "catalog/publish"
		}
		schemaURL := &url.URL{Path: action}
		validate = func(c context.Context, pushBody []byte) error {
			return validator.Validate(c, schemaURL, pushBody)
		}
	}

	metrics, err := crawler.NewOTelMetrics(otel.Meter("crawler"))
	if err != nil {
		metrics = runner.NopMetrics{}
	}

	// New logs crawler.daemon.ready on success / crawler.daemon.start_failed on
	// db open/migrate failure.
	eng, closer, err := crawler.New(ctx, settings, crawler.Options{
		Validate: validate,
		Logger:   logger,
		Metrics:  metrics,
		NewID:    func() string { return uuid.NewString() },
	})
	if err != nil {
		return nil, nil, err
	}

	// Start on a background context so job lifetime is bound to Stop(), not to
	// the plugin-registration context.
	if err := eng.Start(context.Background()); err != nil {
		logger.Error("crawler.daemon.start_failed",
			"lifecycle", "daemon", "state", "start_failed", "stage", "start", "error", err.Error())
		_ = closer()
		return nil, nil, fmt.Errorf("crawler: start: %w", err)
	}
	return eng, closer, nil
}
