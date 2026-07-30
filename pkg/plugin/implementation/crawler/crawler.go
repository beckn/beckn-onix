// Package crawler is the thin onix plugin adapter over the framework-agnostic
// crawler engine in internal/crawler. It resolves settings from
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

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	crawler "github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/config"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/fetch"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/runner"
)

// New builds + starts the crawler engine. Config values come from the plugin
// config map, with env overriding, so secrets stay in env. validator (optional)
// performs Phase-1 catalog schema validation before push.
//
// registry is REQUIRED for an enabled crawler: catalog file signatures are
// verified against the publishing participant's public key in the network
// registry, so without it the signature gate has no trust anchor and the
// composition root refuses to build (crawler.ErrNoRegistry). It is not required
// for a disabled crawler, which verifies nothing because it fetches nothing.
//
// The crawler is opt-in (CRAWLER_ENABLED, default false). When it is off, New
// returns an inert crawler without requiring a DSN or touching a database, so
// loading the plugin costs an operator who does not run the crawler nothing.
func New(ctx context.Context, validator definition.SchemaValidator, registry definition.RegistryLookup, cfg map[string]string) (definition.Crawler, func() error, error) {
	get := func(k string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return cfg[k]
	}

	level := crawler.ParseLogLevel(get("CRAWLER_LOG_LEVEL"))
	logger := crawler.NewSlogLogger(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	settings, err := config.Load(get)
	if err != nil {
		// crawler.New owns the rest of the daemon lifecycle; config load happens
		// before it, so this is the one failed the driver still emits.
		logger.Error("crawler failed to start while loading config: "+err.Error(),
			"component", "daemon", "stage", "failed", "at", "config", "error", err.Error())
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

	// definition.RegistryLookup satisfies the engine's fetch.KeyRegistry port
	// structurally. A nil interface must stay nil rather than become a non-nil
	// interface holding a nil value, or the composition root's required-registry
	// check would pass and every file would fail at resolution time instead.
	var keyRegistry fetch.KeyRegistry
	if registry != nil {
		keyRegistry = registry
	}

	// New logs crawler.daemon.ready on success / crawler.daemon.failed on
	// db open/migrate failure.
	eng, closer, err := crawler.New(ctx, settings, crawler.Options{
		Registry: keyRegistry,
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
		logger.Error("crawler failed to start while launching the jobs: "+err.Error(),
			"component", "daemon", "stage", "failed", "at", "start", "error", err.Error())
		_ = closer()
		return nil, nil, fmt.Errorf("crawler: start: %w", err)
	}
	return eng, closer, nil
}
