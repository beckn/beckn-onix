package definition

import "context"

// Crawler runs the decentralized-catalog crawl as two scheduled jobs (an
// index job and a catalog job) that resolve published catalogs and push them
// to Discovery, plus an on-demand trigger to re-crawl one index immediately.
type Crawler interface {
	// Start launches the background jobs; it returns immediately.
	Start(ctx context.Context) error
	// Stop signals the jobs and waits for the in-flight pass to drain.
	Stop() error
	// CrawlNow runs an immediate crawl for one index URL (supportability:
	// re-pull a stuck provider on demand).
	CrawlNow(ctx context.Context, indexURL string) error
}

// CrawlerProvider initializes a new Crawler. It receives an optional
// SchemaValidator (Phase-1 catalog schema validation before push) and the
// plugin's config map.
type CrawlerProvider interface {
	New(ctx context.Context, validator SchemaValidator, config map[string]string) (Crawler, func() error, error)
}
