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
// index entry/file's self-signature is verified against. metadataLookup is
// REQUIRED whenever registry-backed discovery (the "networks" config) is
// used: it resolves each configured networkId to its member providers via
// the dediregistry plugin's QueryByNetwork, rather than a direct DeDi call.
// A deployment using only staticIndexUrls (no networks) does not need it and
// may pass nil.
func (Provider) New(ctx context.Context, registry definition.RegistryLookup, metadataLookup definition.RegistryMetadataLookup, config map[string]string) (definition.Crawler, func() error, error) {
	if registry == nil {
		return nil, nil, fmt.Errorf("catalogcrawler: a RegistryLookup is required")
	}
	if len(splitNonEmpty(config[cfgNetworks])) > 0 && metadataLookup == nil {
		return nil, nil, fmt.Errorf("catalogcrawler: config %q requires a registry plugin that supports RegistryMetadataLookup (network-scoped discovery)", cfgNetworks)
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

	src := buildSource(config, metadataLookup, log)
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
		params:         params,
		sched:          NewScheduler(params, schedCfg, log),
		metadataLookup: metadataLookup,
		log:            log,
	}
	return c, db.Close, nil
}

// buildSource unions a static config list (if any) with a registry-backed
// lookup (if any networks are configured) -- an index crawled via either
// path is polled the same way once discovered.
func buildSource(config map[string]string, metadataLookup definition.RegistryMetadataLookup, log *slog.Logger) crawlmanager.Source {
	var sources []crawlmanager.Source
	if urls := splitNonEmpty(config[cfgStaticIndexURLs]); len(urls) > 0 {
		sources = append(sources, source.NewConfigSource(urls))
	}
	if networks := splitNonEmpty(config[cfgNetworks]); len(networks) > 0 && metadataLookup != nil {
		sources = append(sources, &registryDiscoverer{lookup: metadataLookup, networkIDs: networks, log: log})
	}
	return multiSource(sources)
}

// registryDiscoverer implements crawlmanager.Source by asking the
// configured registry plugin's RegistryMetadataLookup.QueryByNetwork for the
// participant records of each configured networkId -- the registry plugin
// (e.g. dediregistry) is the sole source of truth for network membership;
// this is just the mapping from its generic SubscriberRecord shape to
// catalog-core's IndexRef, not a second discovery mechanism.
type registryDiscoverer struct {
	lookup     definition.RegistryMetadataLookup
	networkIDs []string
	log        *slog.Logger
}

// Discover queries lookup once per configured network and returns one
// IndexRef per catalog index URL any record declares in its
// meta.catalog_index_urls (a record with more than one URL yields more than
// one ref, one per catalog per node), deduped by index URL so a provider
// found in multiple networks is crawled once.
func (d *registryDiscoverer) Discover(ctx context.Context) ([]crawlmanager.IndexRef, error) {
	seen := make(map[string]bool)
	var refs []crawlmanager.IndexRef
	for _, net := range d.networkIDs {
		records, err := d.lookup.QueryByNetwork(ctx, net)
		if err != nil {
			d.log.ErrorContext(ctx, "catalogcrawler: registry lookup failed", "networkId", net, "error", err)
			return nil, fmt.Errorf("catalogcrawler: registry lookup %q: %w", net, err)
		}
		found := 0
		for _, rec := range records {
			for _, entry := range rec.MetaArrays["catalog_index_urls"] {
				idx := strings.TrimSpace(entry)
				if idx == "" || seen[idx] {
					continue
				}
				seen[idx] = true
				found++
				refs = append(refs, crawlmanager.IndexRef{IndexURL: idx, ParticipantID: rec.SubscriberID})
			}
		}
		d.log.InfoContext(ctx, "catalogcrawler: registry lookup succeeded", "networkId", net, "providersFound", found)
	}
	return refs, nil
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
	params         crawlmanager.Params
	sched          *Scheduler
	metadataLookup definition.RegistryMetadataLookup
	log            *slog.Logger
}

func (c *crawlerImpl) Start(ctx context.Context) error {
	c.sched.Start(ctx)
	return nil
}

func (c *crawlerImpl) Stop() error {
	c.sched.Stop()
	return nil
}

// CrawlRegistry runs an immediate registry-backed crawl against networkIDs --
// the same dediregistry-backed discovery the scheduled pass uses, just
// against caller-supplied networks instead of the configured defaults. It
// launches one background poll and returns a run ID immediately; the poll's
// outcome is only observable via logs/the queue, the same as a scheduled
// tick. The poll runs under the Scheduler's own lifecycle (via
// Scheduler.RunOnce), not the caller's context, so it survives a
// request-scoped caller returning and is still waited-for (not orphaned) by
// Stop.
//
// registryURL is validated but otherwise unused: discovery now goes through
// the dediregistry plugin instance, which owns its own configured URL
// (registry.config.url) rather than a per-call base URL.
func (c *crawlerImpl) CrawlRegistry(ctx context.Context, registryURL string, networkIDs []string) (string, error) {
	if strings.TrimSpace(registryURL) == "" {
		return "", fmt.Errorf("catalogcrawler: registryURL is required")
	}
	if len(networkIDs) == 0 {
		return "", fmt.Errorf("catalogcrawler: at least one networkID is required")
	}
	if c.metadataLookup == nil {
		return "", fmt.Errorf("catalogcrawler: no RegistryMetadataLookup configured for registry-backed discovery")
	}
	adhoc := c.params
	adhoc.Source = &registryDiscoverer{lookup: c.metadataLookup, networkIDs: networkIDs, log: c.log}

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
