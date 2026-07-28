// Package catalogcrawler is the thin onix plugin adapter over the
// framework-agnostic crawler engine in pkg/catalogcrawler. It builds the
// engine from the plugin config map (with env taking precedence so secrets
// like the DB DSN stay out of YAML), injects the schema validator, starts the
// scheduled jobs, and returns a closer that stops them.
package catalogcrawler

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	engine "github.com/beckn-one/beckn-onix/pkg/catalogcrawler"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/state"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// New builds + starts the crawler engine. Config values come from the plugin
// config map, with env overriding, so secrets stay in env. validator
// (optional) performs Phase-1 catalog schema validation before push.
func New(ctx context.Context, validator definition.SchemaValidator, config map[string]string) (definition.Crawler, func() error, error) {
	get := func(k string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return config[k]
	}

	settings, err := engine.LoadSettings(get)
	if err != nil {
		return nil, nil, fmt.Errorf("catalogcrawler: config: %w", err)
	}

	db, err := state.Open(settings.DBDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("catalogcrawler: opening db: %w", err)
	}
	if err := state.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("catalogcrawler: migrate: %w", err)
	}

	httpc := engine.NewHTTPClient(settings.FetchTimeout, settings.MaxArtifactBytes, settings.MaxDecompressedBytes, false)

	var validate engine.Validator
	if validator != nil {
		// schemav2validator keys on reqURL.Path to select the action schema;
		// the crawler validates the /push body against catalog/publish.
		action := get("CRAWLER_SCHEMA_ACTION")
		if action == "" {
			action = "catalog/publish"
		}
		schemaURL := &url.URL{Path: action}
		validate = func(ctx context.Context, pushBody []byte) error {
			return validator.Validate(ctx, schemaURL, pushBody)
		}
	}

	logger := engine.NewSlogLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	metrics, err := engine.NewOTelMetrics(otel.Meter("catalogcrawler"))
	if err != nil {
		metrics = engine.NopMetrics{}
	}

	eng := engine.New(engine.Config{
		Networks:        settings.NetworkIDs,
		BppURI:          settings.BppURI,
		IndexInterval:   settings.IndexInterval,
		CatalogInterval: settings.CatalogInterval,
		MaxAttempts:     settings.MaxAttempts,
		MaxPushBytes:    settings.MaxPushBytes,
		MergeOnly:       settings.MergeOnly,
	}, engine.Deps{
		Store:      state.New(db),
		Source:     engine.NewConfigSource(settings.IndexURLs),
		FetchIndex: httpc.FetchIndex,
		FetchFile:  httpc.FetchFile,
		Validate:   validate,
		Push: func(ctx context.Context, body []byte) (engine.PartOutcome, error) {
			return httpc.Push(ctx, settings.PushEndpoint, body)
		},
		Log:     logger,
		Metrics: metrics,
		NewID:   func() string { return uuid.NewString() },
	})

	// Start on a background context so job lifetime is bound to Stop(), not to
	// the plugin-registration context.
	if err := eng.Start(context.Background()); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("catalogcrawler: start: %w", err)
	}

	closer := func() error {
		stopErr := eng.Stop()
		if cerr := db.Close(); cerr != nil && stopErr == nil {
			stopErr = cerr
		}
		return stopErr
	}
	return eng, closer, nil
}
