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
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
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
	cfgDBDSN                = "dbDsn"
	cfgNetworks             = "networks"        // comma-separated networkIds for registry-backed discovery
	cfgStaticIndexURLs      = "staticIndexUrls" // comma-separated, optional fixed index URLs
	cfgDiscoveryURL         = "discoveryPushUrl"
	cfgParticipantID        = "participantId" // this deployment's own bppId
	cfgBppURI               = "bppUri"        // this deployment's own bppUri
	cfgFetchTimeoutSec      = "fetchTimeoutSeconds"
	cfgMaxFetchBytes        = "maxFetchBytes"
	cfgMaxDecompressed      = "maxDecompressedBytes"
	cfgMaxPushBytes         = "maxPushBytes"
	cfgIndexIntervalSec     = "indexIntervalSeconds"
	cfgCatalogIntervalSec   = "catalogIntervalSeconds"
	cfgAllowPrivateHosts    = "allowPrivateHosts"        // "true" to allow loopback/private fetch targets; tests only
	cfgMaxAttempts          = "maxAttempts"              // transient-failure retries before parking; 0/unset => unlimited
	cfgParkSweepIntervalSec = "parkSweepIntervalSeconds" // how often RequeueOrAbandonParked runs; 0/unset => DefaultParkSweepInterval (15m)
	cfgParkOlderThanSec     = "parkOlderThanSeconds"     // how long a catalog must sit parked before this sweep touches it; 0/unset => act on anything parked
	cfgMaxParkCount         = "maxParkCount"             // revivals allowed before abandoning a parked catalog; 0/unset => derived from the sweep interval and DefaultMaxParkRetryBudget (12h)
)

const (
	defaultFetchTimeout    = 30 * time.Second
	defaultMaxFetchBytes   = 10 << 20
	defaultMaxDecompressed = 20 << 20
	defaultMaxPushBytes    = 10 << 20
	// DefaultMaxParkRetryBudget is the total wall-clock time a parked
	// catalog keeps getting revived before being abandoned, by default --
	// combined with the actual (possibly overridden) park-sweep interval
	// via crawlmanager.DeriveMaxParkCount to compute Params.MaxParkCount.
	DefaultMaxParkRetryBudget = 12 * time.Hour
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
	schedCfg := SchedulerConfig{
		IndexInterval:     durationSecondsOr(config[cfgIndexIntervalSec], DefaultIndexInterval),
		CatalogInterval:   durationSecondsOr(config[cfgCatalogIntervalSec], DefaultCatalogInterval),
		ParkSweepInterval: durationSecondsOr(config[cfgParkSweepIntervalSec], DefaultParkSweepInterval),
		ParkOlderThan:     durationSecondsOr(config[cfgParkOlderThanSec], 0),
	}
	// DefaultMaxParkRetryBudget/schedCfg's own (possibly overridden)
	// ParkSweepInterval together give the actual default maxParkCount --
	// crawlmanager.DefaultMaxParkCount (12) doesn't itself know this
	// plugin's sweep cadence, so deriving it here (rather than leaving
	// Params.MaxParkCount at 0) is what makes a 15-minute sweep actually
	// mean "abandon after ~12h" instead of ~3h.
	//
	// Deliberately not int64Or here (unlike cfgMaxAttempts below): for
	// maxAttempts, 0 and "unset" really do mean the same thing
	// ("unlimited"), so collapsing them is fine. Here they don't -- an
	// operator writing maxParkCount: "0" means "abandon on the very first
	// park, no revivals", a real, distinct value from "unset" (derive the
	// ~12h default). int64Or's n<=0-means-default rule would silently
	// discard that explicit "0" and substitute the derived default
	// instead, so this parses the raw config value directly and only
	// falls back to the derived default when it's actually empty (or not
	// a valid non-negative integer).
	maxParkCount := crawlmanager.DeriveMaxParkCount(schedCfg.ParkSweepInterval, DefaultMaxParkRetryBudget)
	if v := strings.TrimSpace(config[cfgMaxParkCount]); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			maxParkCount = int(n)
		}
	}
	params := crawlmanager.Params{
		Fetcher: fetcher, Source: src, Sink: snk, Store: st, Log: log,
		Networks: networks, MaxAttempts: int(int64Or(config[cfgMaxAttempts], 0)),
		MaxParkCount: maxParkCount,
	}

	c := &crawlerImpl{
		params:         params,
		sched:          NewScheduler(params, schedCfg, log),
		metadataLookup: metadataLookup,
		log:            log,
		st:             st,
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
	return multiSource{sources: sources, log: log}
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

	// mu guards consecutiveFails, which persists across Discover calls on
	// this instance (the scheduler builds one registryDiscoverer in
	// buildSource and polls it every tick from its own single goroutine, so
	// a network that keeps failing tick after tick can be escalated instead
	// of only ever logging at Warn -- see consecutiveFailEscalateThreshold
	// below). The lock isn't guarding against a concurrent caller that
	// exists today -- CrawlRegistry (the on-demand /crawl/trigger path)
	// builds its own separate registryDiscoverer per call rather than
	// reusing this one, so it never contends with the scheduled tick or
	// with itself, and consequently never accumulates or benefits from a
	// failure streak across repeated triggers either. It's here as cheap
	// insurance against a future change (e.g. concurrent scheduled +
	// triggered polling sharing one instance) rather than an active need.
	mu               sync.Mutex
	consecutiveFails map[string]int
}

// consecutiveFailEscalateThreshold is the number of consecutive per-network
// lookup failures after which a network's failure log is escalated from
// Warn to Error -- a single bad tick is expected and recoverable (see
// Discover's doc comment), but a network stuck failing for several ticks in
// a row is worth surfacing loudly even while other networks keep succeeding.
const consecutiveFailEscalateThreshold = 3

// Discover queries lookup once per configured network and returns one
// IndexRef per catalog index URL any record declares in its
// meta.catalog_index_urls (a record with more than one URL yields more than
// one ref, one per catalog per node), deduped by index URL so a provider
// found in multiple networks is crawled once.
//
// A single network's lookup failing (e.g. an unregistered/misconfigured
// networkId 404ing against the registry) does not fail the whole call --
// it's logged and skipped so every other configured network still gets
// discovered and polled this tick. Only when every configured network fails
// is that treated as fatal (returned as an error) -- a single bad network is
// an expected, recoverable config state, but a registry that's unreachable
// for all of them is a real outage that should still surface loudly instead
// of quietly discovering zero indexes every tick. See #921.
func (d *registryDiscoverer) Discover(ctx context.Context) ([]crawlmanager.IndexRef, error) {
	seen := make(map[string]bool)
	var refs []crawlmanager.IndexRef
	var failed []error
	for _, net := range d.networkIDs {
		records, err := d.lookup.QueryByNetwork(ctx, net)
		if err != nil {
			netErr := fmt.Errorf("networkId %q: %w", net, err)
			failed = append(failed, netErr)
			if streak := d.recordFailure(net); streak >= consecutiveFailEscalateThreshold {
				d.log.ErrorContext(ctx, "catalogcrawler: registry lookup failing repeatedly, skipping network", "networkId", net, "consecutiveFailures", streak, "error", err)
			} else {
				d.log.WarnContext(ctx, "catalogcrawler: registry lookup failed, skipping network", "networkId", net, "error", err)
			}
			continue
		}
		d.recordSuccess(net)
		// found counts every non-empty catalog_index_urls entry the registry
		// returned for this network, before the cross-network seen-URL dedup
		// below -- so it reflects what the registry actually reported, not
		// how many of those survived deduping, which is what an operator
		// comparing this log against the registry's own record count expects.
		found := 0
		for _, rec := range records {
			for _, entry := range rec.MetaArrays["catalog_index_urls"] {
				idx := strings.TrimSpace(entry)
				d.log.DebugContext(ctx, "catalogcrawler: registry lookup provider", "networkId", net, "participantId", rec.SubscriberID, "indexUrl", idx)
				if idx == "" {
					continue
				}
				found++
				if seen[idx] {
					continue
				}
				seen[idx] = true
				refs = append(refs, crawlmanager.IndexRef{IndexURL: idx, ParticipantID: rec.SubscriberID})
			}
		}
		d.log.InfoContext(ctx, "catalogcrawler: registry lookup succeeded", "networkId", net, "providersFound", found)
	}
	if err := errIfAllFailed(len(d.networkIDs), failed, fmt.Sprintf("registry lookup failed for all %d configured network(s)", len(failed))); err != nil {
		d.log.ErrorContext(ctx, "catalogcrawler: registry lookup failed for every configured network", "networks", d.networkIDs, "error", err)
		return nil, err
	}
	return refs, nil
}

// recordFailure tracks a network's consecutive lookup-failure streak across
// Discover calls and returns the updated count.
func (d *registryDiscoverer) recordFailure(networkID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.consecutiveFails == nil {
		d.consecutiveFails = make(map[string]int)
	}
	d.consecutiveFails[networkID]++
	return d.consecutiveFails[networkID]
}

// recordSuccess resets a network's consecutive-failure streak once its
// lookup succeeds again.
func (d *registryDiscoverer) recordSuccess(networkID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.consecutiveFails, networkID)
}

// multiSource unions several Sources' Discover results, deduped by index
// URL -- one provider found via more than one source is crawled once.
//
// One source erroring (e.g. registryDiscoverer's all-networks-failed case)
// does not discard refs another source already found -- the same
// partial-failure tolerance registryDiscoverer applies across networks
// applies here across sources, so a broken registry doesn't also blank out
// a working staticIndexUrls config, or vice versa. Only when every source
// errors is that fatal. See #921/#922.
//
// A partial failure (some, not all, sources erroring) still logs at Warn:
// unlike the all-failed case, it doesn't fail the tick, so without its own
// log line it would be invisible to anything watching only for a failed
// poll tick or /crawl/status -- a fully broken registry sitting behind an
// otherwise-healthy staticIndexUrls config would otherwise look identical
// to a fully healthy tick. Warn rather than Error because a source can fail
// on a single tick and recover on the next (e.g. a transient network blip);
// an Error-level alert firing on that self-recovering case would be noisier
// than the condition warrants -- a source repeatedly failing tick after
// tick is exactly what registryDiscoverer's own per-network Warn-to-Error
// escalation (consecutiveFailEscalateThreshold) is for.
type multiSource struct {
	sources []crawlmanager.Source
	log     *slog.Logger
}

func (m multiSource) Discover(ctx context.Context) ([]crawlmanager.IndexRef, error) {
	seen := make(map[string]bool)
	var refs []crawlmanager.IndexRef
	var failed []error
	for _, s := range m.sources {
		found, err := s.Discover(ctx)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		for _, r := range found {
			if seen[r.IndexURL] {
				continue
			}
			seen[r.IndexURL] = true
			refs = append(refs, r)
		}
	}
	if err := errIfAllFailed(len(m.sources), failed, fmt.Sprintf("all %d discovery source(s) failed", len(failed))); err != nil {
		return nil, err
	}
	if len(failed) > 0 {
		m.log.WarnContext(ctx, "catalogcrawler: one or more discovery sources failed this tick, continuing with partial results from the rest", "failedSources", len(failed), "totalSources", len(m.sources), "error", errors.Join(failed...))
	}
	return refs, nil
}

// crawlerImpl implements definition.Crawler.
type crawlerImpl struct {
	params         crawlmanager.Params
	sched          *Scheduler
	metadataLookup definition.RegistryMetadataLookup
	log            *slog.Logger

	// st is the same *store.Store instance as params.Store, held separately
	// (and typed concretely, not as crawlmanager.Store) because Status
	// needs its own reporting query -- not part of crawlmanager.Store's
	// narrow scheduler-facing surface.
	st *store.Store
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
// the same registry-backed discovery the scheduled pass uses, just against
// caller-supplied networks instead of the configured defaults. Discovery
// goes through the configured RegistryMetadataLookup plugin instance, which
// owns its own registry URL -- there is no per-call registry URL to select a
// different endpoint. It launches one background poll and returns a run ID
// immediately; the poll's outcome is only observable via logs/the queue, the
// same as a scheduled tick. The poll runs under the Scheduler's own
// lifecycle (via Scheduler.RunOnce), not the caller's context, so it
// survives a request-scoped caller returning and is still waited-for (not
// orphaned) by Stop.
//
// Each call builds its own registryDiscoverer rather than reusing the
// scheduled crawler's -- this is a one-off diagnostic/backfill action, not a
// monitored recurring path, so it intentionally starts with a clean
// per-network failure streak every time and never contributes to or
// benefits from the scheduled tick's consecutiveFailEscalateThreshold
// escalation (see registryDiscoverer.mu's doc comment). Repeated triggers
// against a persistently broken network will each log at Warn, not escalate
// to Error, unlike the scheduled path.
func (c *crawlerImpl) CrawlRegistry(ctx context.Context, networkIDs []string) (string, error) {
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
			c.log.ErrorContext(ctx, "catalogcrawler: on-demand crawl failed", "runId", runID, "networkIds", networkIDs, "error", err)
		}
	})
	if !started {
		return "", fmt.Errorf("catalogcrawler: crawler is not running")
	}
	return runID, nil
}

// Status implements definition.Crawler. Unlike CrawlRegistry, this is a
// plain read against persisted state -- it works whether or not the
// scheduler has been Start()-ed, since it never touches c.sched.
func (c *crawlerImpl) Status(ctx context.Context, subscriberID, catalogID string) ([]definition.CrawlStatus, error) {
	rows, err := c.st.Status(ctx, subscriberID, catalogID)
	if err != nil {
		return nil, fmt.Errorf("catalogcrawler: Status: %w", err)
	}
	out := make([]definition.CrawlStatus, len(rows))
	for i, r := range rows {
		out[i] = definition.CrawlStatus{
			CatalogID:         r.CatalogID,
			IndexURL:          r.IndexURL,
			EverSynced:        r.EverSynced,
			EntryVersion:      r.EntryVersion,
			Retired:           r.Retired,
			LastError:         r.Reason,
			Queued:            r.Queued,
			Attempts:          r.Attempts,
			NextAttemptAt:     r.NextAttemptAt,
			Parked:            r.Parked,
			ParkCount:         r.ParkCount,
			Abandoned:         r.Abandoned,
			AbandonedAt:       r.AbandonedAt,
			UpdatedAt:         r.UpdatedAt,
			IndexLastPolledAt: r.IndexPolledAt,
		}
	}
	return out, nil
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

// errIfAllFailed returns a wrapped error joining errs when every one of
// total attempts failed (len(errs) == total > 0), and nil otherwise -- the
// shared "tolerate partial failure, only escalate to a hard error when
// everything failed" policy both registryDiscoverer.Discover (across
// networks) and multiSource.Discover (across sources) apply. what describes
// what failed, e.g. "registry lookup failed for all %d configured
// network(s)".
func errIfAllFailed(total int, errs []error, what string) error {
	if total == 0 || len(errs) != total {
		return nil
	}
	return fmt.Errorf("%s: %w", what, errors.Join(errs...))
}
