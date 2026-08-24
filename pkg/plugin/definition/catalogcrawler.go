package definition

import "context"

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
