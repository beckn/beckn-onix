package definition

import (
	"context"
	"time"
)

// Crawler runs the decentralized-catalog crawl: discovering indexes,
// detecting which catalogs changed, and pushing each changed catalog's
// current content onward -- as background scheduled jobs, plus an on-demand
// trigger to run an immediate registry-backed crawl.
type Crawler interface {
	// Start launches the background jobs; it returns immediately.
	Start(ctx context.Context) error
	// Stop signals the jobs and waits for the in-flight pass to drain.
	Stop() error
	// CrawlRegistry runs an immediate registry-backed crawl: it discovers the
	// providers of the given networks (via the configured registry plugin's
	// RegistryMetadataLookup) and crawls each -- the same registry-based
	// input the scheduled pass uses, so a manual trigger and the background
	// pass take one input model. Discovery always goes through the plugin
	// instance configured at construction, which owns its own registry URL
	// -- there is no per-call registry URL, so this cannot be pointed at a
	// different registry than the one the deployment is configured with.
	// Returns a run ID the caller can use to correlate the crawl's
	// (asynchronous) log lines.
	//
	// ctx bounds only this call's synchronous validation, not the crawl
	// itself: the crawl runs under the Crawler's own lifecycle (so it
	// survives a request-scoped ctx returning, and is waited-for by Stop)
	// rather than being canceled if ctx is.
	CrawlRegistry(ctx context.Context, networkIDs []string) (string, error)

	// Status reports the current crawl/sync state for every catalog owned
	// by subscriberID (the authenticated caller -- see the catalogCrawlStatus
	// handler), or just catalogID if it's non-empty. A catalogID not owned
	// by subscriberID is indistinguishable from one that doesn't exist at
	// all: both return an empty slice, not an error -- callers must not be
	// able to probe for another subscriber's catalogIds.
	Status(ctx context.Context, subscriberID, catalogID string) ([]CrawlStatus, error)
}

// CrawlStatus reports one catalog's last-known crawl/sync state, as
// currently persisted -- not a live check. Only fields the crawler actually
// populates today are included; see catalogcrawler's internal/store package
// for which crawler_catalog/crawler_queue/crawler_index columns are real
// versus unused schema inherited from an earlier prototype.
type CrawlStatus struct {
	CatalogID string `json:"catalogId"`
	IndexURL  string `json:"indexUrl,omitempty"`

	// EverSynced is false for a catalog queued for its very first sync --
	// EntryVersion/Retired/LastError/UpdatedAt are all zero-valued in that
	// case, since nothing has settled yet. Check this (not EntryVersion
	// being zero) to tell "never synced" apart from a genuinely zero
	// version, and check Queued to see whether that first sync is in fact
	// pending right now.
	EverSynced bool `json:"everSynced"`

	// EntryVersion is this catalog's last-settled entry-level version (see
	// CatalogPublishOutcome.EntryVersion for what it tracks) -- what the
	// crawler last successfully applied, which may lag the publisher's
	// actual current state if a sync is still queued or retrying (see
	// Queued/Attempts/NextAttemptAt below). Deliberately the only version
	// reported here, not the file-lineage Version CatalogPublishOutcome
	// also carries: EntryVersion is what a publisher already has in hand
	// from its own Publish call (RFC NFH-014's entry-level versioning) to
	// cross-check the crawler actually caught up, and it bumps on every
	// entry change -- content or metadata-only -- unlike Version, which can
	// stay unchanged on a metadata-only publish and so would under-report
	// staleness here.
	EntryVersion int64 `json:"entryVersion"`

	// Retired is true once this catalog's index entry carried a tombstone
	// (retiredAt) on its last successful sync.
	Retired bool `json:"retired"`

	// LastError is the most recent sync failure's reason, or empty if the
	// catalog has never failed, or last failed before its most recent
	// success (a success clears this). Its presence does not by itself mean
	// the catalog is out of sync -- check Queued/Attempts too, since a
	// once-failed catalog that later succeeded reports LastError empty.
	LastError string `json:"lastError,omitempty"`

	// Queued is true while a sync for this catalog is actively pending or
	// in flight -- Attempts/NextAttemptAt are only meaningful when this is
	// true. Parked and Abandoned are separate, mutually exclusive states
	// (see their own doc comments below), not folded into Queued.
	Queued        bool      `json:"queued"`
	Attempts      int       `json:"attempts,omitempty"`
	NextAttemptAt time.Time `json:"nextAttemptAt,omitempty"`

	// Parked is true once a permanent (or retry-budget-exhausted) failure
	// parked this catalog -- crawlmanager stops retrying it on its own
	// schedule. It isn't necessarily stuck forever: a periodic sweep (see
	// ParkCount) may revive it automatically, and publishing a fresh
	// version of the catalog reactivates it immediately regardless.
	Parked bool `json:"parked,omitempty"`
	// ParkCount is how many times this catalog has been parked, ever
	// (cumulative -- not reset by a revival). Compared against the
	// deployment's own configured park-retry budget to decide whether the
	// next park attempt abandons it instead.
	ParkCount int `json:"parkCount,omitempty"`

	// Abandoned is true once the periodic park sweep gave up on this
	// catalog after too many parks -- terminal, no further automatic
	// retries, though a fresh publish still reactivates it.
	Abandoned bool `json:"abandoned,omitempty"`
	// AbandonedAt is when Abandoned became true.
	AbandonedAt time.Time `json:"abandonedAt,omitempty"`

	// UpdatedAt is when this catalog's settled state was last written --
	// i.e. its last successful sync or failure record, whichever is more
	// recent.
	UpdatedAt time.Time `json:"updatedAt,omitempty"`

	// IndexLastPolledAt is when IndexURL was last actually fetched (any
	// outcome, changed or not) -- the crawler has no per-index next-crawl
	// timestamp of its own to report (PollIndexes polls every discovered
	// index unconditionally every tick), so there is no exact
	// "next scheduled crawl" to return; a caller wanting an estimate can
	// add the deployment's own configured indexIntervalSeconds to this.
	IndexLastPolledAt time.Time `json:"indexLastPolledAt,omitempty"`
}

// CrawlerProvider initializes a new Crawler. It receives a RegistryLookup
// (used to resolve publisher signing keys -- catalog index entries and files
// self-sign, and the registry is the key distribution channel, exactly as
// signvalidator verifies transport signatures), a RegistryMetadataLookup
// (used to resolve each configured network's member providers via
// QueryByNetwork, for registry-backed discovery), and the plugin's config
// map.
//
// RegistryLookup is REQUIRED for an enabled crawler: there is deliberately
// no per-deployment trusted-key configuration. RegistryMetadataLookup is
// REQUIRED whenever registry-backed discovery (the "networks" config) is
// used.
type CrawlerProvider interface {
	New(ctx context.Context, registry RegistryLookup, metadataLookup RegistryMetadataLookup, config map[string]string) (Crawler, func() error, error)
}
