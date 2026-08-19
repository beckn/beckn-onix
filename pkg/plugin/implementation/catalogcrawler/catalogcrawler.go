// Package catalogcrawler is the onix plugin wiring for the decentralized-
// catalog crawl: it parses plugin config, builds the four concrete pieces
// crawlmanager.Params needs (a Postgres Store, a registry+static Source, an
// HTTP-push-to-Discovery Sink, and a ticker-driven Scheduler), and satisfies
// definition.Crawler by delegating to the Scheduler's lifecycle. No business
// logic of its own -- see github.com/beckn/catalog-core's
// pkg/catalog/crawlmanager for that.
//
// Logging here uses log/slog, not this repo's usual pkg/log (zerolog)
// directly, because crawlmanager.Params.Log and Scheduler are typed against
// *slog.Logger -- catalog-core is a dependency-free library and can't import
// onix's pkg/log. New wires slog.New(log.NewSlogHandler()) instead of
// slog.Default(), the same bridge catalogpublisher uses, so crawler logs
// still flow through onix's usual zerolog pipeline.
package catalogcrawler

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogcrawler/internal/sink"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogcrawler/internal/source"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogcrawler/internal/store"
	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
	"github.com/google/uuid"
)

// Config keys, all read from the plugin's config map[string]string. camelCase,
// matching onix's own config-key convention (e.g. schemaversionmediator's
// fetchTimeout/artifactCacheTTL).
const (
	cfgDBDSN              = "dbDsn"
	cfgNetworks           = "networks"        // comma-separated networkIds for registry-backed discovery
	cfgStaticIndexURLs    = "staticIndexUrls" // comma-separated, optional fixed index URLs
	cfgDediRegistryURL    = "dediRegistryUrl" // base URL for the DeDi /query endpoint used for discovery -- distinct from the RegistryLookup passed to New, which resolves signing keys
	cfgDiscoveryURL       = "discoveryPushUrl"
	cfgParticipantID      = "participantId" // this deployment's own bppId
	cfgBppURI             = "bppUri"        // this deployment's own bppUri
	cfgFetchTimeoutSec    = "fetchTimeoutSeconds"
	cfgMaxFetchBytes      = "maxFetchBytes"
	cfgMaxDecompressed    = "maxDecompressedBytes"
	cfgMaxPushBytes       = "maxPushBytes"
	cfgIndexIntervalSec   = "indexIntervalSeconds"
	cfgCatalogIntervalSec = "catalogIntervalSeconds"
	cfgAllowPrivateHosts  = "allowPrivateHosts" // "true" to allow loopback/private fetch targets; tests only
	cfgMaxAttempts        = "maxAttempts"       // transient-failure retries before parking; 0/unset => unlimited
)

const (
	defaultFetchTimeout    = 30 * time.Second
	defaultMaxFetchBytes   = 10 << 20
	defaultMaxDecompressed = 20 << 20
	defaultMaxPushBytes    = 10 << 20
)

// Provider implements definition.CrawlerProvider.
type Provider struct{}

// New builds a Crawler from config, wiring a Postgres Store, a
// registry+static Source, a Discovery-push Sink, and a ticker Scheduler.
// registry is REQUIRED: it is the key-distribution channel every fetched
// index entry/file's self-signature is verified against.
func (Provider) New(ctx context.Context, registry definition.RegistryLookup, config map[string]string) (definition.Crawler, func() error, error) {
	if registry == nil {
		return nil, nil, fmt.Errorf("catalogcrawler: a RegistryLookup is required")
	}
	dsn := strings.TrimSpace(config[cfgDBDSN])
	if dsn == "" {
		return nil, nil, fmt.Errorf("catalogcrawler: config %q is required", cfgDBDSN)
	}
	discoveryURL := strings.TrimSpace(config[cfgDiscoveryURL])
	if discoveryURL == "" {
		return nil, nil, fmt.Errorf("catalogcrawler: config %q is required", cfgDiscoveryURL)
	}

	log := slog.New(log.NewSlogHandler())

	db, err := store.Open(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("catalogcrawler: opening database: %w", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("catalogcrawler: migrating database: %w", err)
	}
	st := store.New(db)

	fetchTimeout := durationSecondsOr(config[cfgFetchTimeoutSec], defaultFetchTimeout)
	maxFetchBytes := int64Or(config[cfgMaxFetchBytes], defaultMaxFetchBytes)
	maxDecompressed := int64Or(config[cfgMaxDecompressed], defaultMaxDecompressed)
	allowPrivate := config[cfgAllowPrivateHosts] == "true"

	keys := newRegistryKeySource(registry)
	client := crawler.NewClient(fetchTimeout, maxFetchBytes, allowPrivate)
	fetcher := catalog.NewFetcher(client, keys, maxDecompressed)

	src := buildSource(config, fetchTimeout, log)
	snk := sink.NewDiscoverySink(discoveryURL, config[cfgParticipantID], config[cfgBppURI], int64Or(config[cfgMaxPushBytes], defaultMaxPushBytes), fetchTimeout)

	// The same configured networks drive both registry-backed discovery
	// (buildSource) and scope filtering (Params.Networks) -- one deployment
	// concept, "which networks do I carry", not two.
	networks := splitNonEmpty(config[cfgNetworks])
	params := crawlmanager.Params{
		Fetcher: fetcher, Source: src, Sink: snk, Store: st, Log: log,
		Networks: networks, MaxAttempts: int(int64Or(config[cfgMaxAttempts], 0)),
	}
	schedCfg := SchedulerConfig{
		IndexInterval:   durationSecondsOr(config[cfgIndexIntervalSec], DefaultIndexInterval),
		CatalogInterval: durationSecondsOr(config[cfgCatalogIntervalSec], DefaultCatalogInterval),
	}

	c := &crawlerImpl{
		params:       params,
		sched:        NewScheduler(params, schedCfg, log),
		fetchTimeout: fetchTimeout,
		log:          log,
	}
	return c, db.Close, nil
}

// buildSource unions a static config list (if any) with a DeDi registry
// lookup (if any networks are configured) -- an index crawled via either
// path is polled the same way once discovered.
func buildSource(config map[string]string, timeout time.Duration, log *slog.Logger) crawlmanager.Source {
	var sources []crawlmanager.Source
	if urls := splitNonEmpty(config[cfgStaticIndexURLs]); len(urls) > 0 {
		sources = append(sources, source.NewConfigSource(urls))
	}
	if networks := splitNonEmpty(config[cfgNetworks]); len(networks) > 0 {
		if base := strings.TrimSpace(config[cfgDediRegistryURL]); base != "" {
			client := source.NewDediQueryClient(base, timeout)
			sources = append(sources, source.NewRegistrySource(client, networks, log))
		}
	}
	return multiSource(sources)
}

// multiSource unions several Sources' Discover results, deduped by index
// URL -- one provider found via more than one source is crawled once.
type multiSource []crawlmanager.Source

func (m multiSource) Discover(ctx context.Context) ([]crawlmanager.IndexRef, error) {
	seen := make(map[string]bool)
	var refs []crawlmanager.IndexRef
	for _, s := range m {
		found, err := s.Discover(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range found {
			if seen[r.IndexURL] {
				continue
			}
			seen[r.IndexURL] = true
			refs = append(refs, r)
		}
	}
	return refs, nil
}

// crawlerImpl implements definition.Crawler.
type crawlerImpl struct {
	params       crawlmanager.Params
	sched        *Scheduler
	fetchTimeout time.Duration
	log          *slog.Logger
}

func (c *crawlerImpl) Start(ctx context.Context) error {
	c.sched.Start(ctx)
	return nil
}

func (c *crawlerImpl) Stop() error {
	c.sched.Stop()
	return nil
}

// CrawlRegistry runs an immediate registry-backed crawl against registryURL
// and networkIDs -- the same DeDi /query discovery the scheduled pass uses,
// just against caller-supplied parameters instead of the configured
// defaults. It launches one background poll and returns a run ID
// immediately; the poll's outcome is only observable via logs/the queue, the
// same as a scheduled tick. The poll runs under the Scheduler's own
// lifecycle (via Scheduler.RunOnce), not the caller's context, so it
// survives a request-scoped caller returning and is still waited-for (not
// orphaned) by Stop.
func (c *crawlerImpl) CrawlRegistry(ctx context.Context, registryURL string, networkIDs []string) (string, error) {
	if strings.TrimSpace(registryURL) == "" {
		return "", fmt.Errorf("catalogcrawler: registryURL is required")
	}
	if len(networkIDs) == 0 {
		return "", fmt.Errorf("catalogcrawler: at least one networkID is required")
	}
	client := source.NewDediQueryClient(registryURL, c.fetchTimeout)
	adhoc := c.params
	adhoc.Source = source.NewRegistrySource(client, networkIDs, c.log)

	runID := uuid.NewString()
	started := c.sched.RunOnce(func(ctx context.Context) {
		if err := adhoc.PollIndexes(ctx); err != nil {
			c.log.ErrorContext(ctx, "catalogcrawler: on-demand crawl failed", "runId", runID, "registryUrl", registryURL, "error", err)
		}
	})
	if !started {
		return "", fmt.Errorf("catalogcrawler: crawler is not running")
	}
	return runID, nil
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func durationSecondsOr(s string, def time.Duration) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return time.Duration(n) * time.Second
}

func int64Or(s string, def int64) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
