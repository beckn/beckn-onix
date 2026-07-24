package definition

import (
	"context"
	"encoding/json"
	"time"
)

// CrawlMode controls whether a crawl re-fetches every artifact or skips
// artifacts whose digest hasn't changed since the last crawl.
type CrawlMode string

const (
	// CrawlModeIncremental skips re-fetching a catalog part whose digest
	// matches the previous crawl. This is the default when Mode is empty.
	CrawlModeIncremental CrawlMode = "incremental"
	// CrawlModeFull re-fetches and re-verifies every artifact regardless of
	// digest.
	CrawlModeFull CrawlMode = "full"
)

// CrawlRequest describes a single subscriber crawl.
type CrawlRequest struct {
	// SubscriberID is the provider node's identifying URI (e.g. its bppURI).
	SubscriberID string
	// NetworkID scopes the crawl for standing/visibility filtering.
	NetworkID string
	// Mode defaults to CrawlModeIncremental when empty.
	Mode CrawlMode
}

// ManifestResult captures the outcome of fetching and verifying a
// subscriber's manifest (level 1 of the chain).
type ManifestResult struct {
	URL        string
	Digest     string
	Verified   bool
	VerifiedAt time.Time
}

// PartRef identifies a fetched catalog file.
type PartRef struct {
	URL          string
	Digest       string
	Size         int64
	LastModified time.Time
}

// VerificationOutcome captures the individual checks performed on a
// catalog's fetched files. Per the file spec, each baseline/change file
// carries its own signature tuple ({catalogId, version, url, digest,
// validUntil}) -- unlike the earlier DeDi-wrapper model, where catalog
// parts carried no signature at all and only the whole index did.
// SignatureValid is true only when every one of this catalog's fetched
// files (baseline and every changes[] entry) verified.
type VerificationOutcome struct {
	DigestMatch    bool
	SchemaValid    bool
	SignatureValid bool
	// VersionOK is false only when a version rollback was detected.
	VersionOK bool
}

// CatalogResult is the outcome of processing one catalog entry from the
// index: its baseline, fetched and verified, with every changes[] entry
// fetched, verified, and applied in order (file spec's upsert/removal
// model, by id) to produce the catalog's current effective content.
type CatalogResult struct {
	CatalogID   string
	Version     int
	Status      string // "ACTIVE" | "RETIRED"
	CatalogType string
	NetworkIds  []string
	SchemaTypes []string
	// Source identifies the baseline file only, for diagnostic reference
	// -- a catalog's current content may be composed from several files
	// (baseline + N change files), not just one.
	Source       PartRef
	Changed      bool
	Verification VerificationOutcome
	// RetiredAt is set only when Status == "RETIRED"; Catalog and Source
	// are then both zero (a tombstone carries no files).
	RetiredAt *time.Time
	// Catalog is the fully composed current content (baseline with every
	// change file applied); omitted when Changed is false, unless the
	// crawl ran in full mode.
	Catalog json.RawMessage
}

// CrawlError is a non-fatal, per-artifact error surfaced alongside an
// otherwise-successful CrawlResult.
type CrawlError struct {
	CatalogID string
	Stage     string // "index_fetch" | "index_verify" | "part_fetch" | "part_verify"
	Reason    string
	Fatal     bool
}

// CrawlResult is the return value of a single CrawlSubscriber call.
type CrawlResult struct {
	SubscriberID string
	CrawledAt    time.Time
	Mode         CrawlMode
	Manifest     ManifestResult
	Catalogs     []CatalogResult
	Errors       []CrawlError
}

// Crawler fetches, verifies, and returns a subscriber's published catalogs.
type Crawler interface {
	CrawlSubscriber(ctx context.Context, req CrawlRequest) (CrawlResult, error)
}

// CrawlerProvider initializes a new Crawler instance.
type CrawlerProvider interface {
	New(ctx context.Context, signer Signer, keyManager KeyManager, config map[string]string) (Crawler, func() error, error)
}
