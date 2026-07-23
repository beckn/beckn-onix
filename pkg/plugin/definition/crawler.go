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

// PartRef identifies a fetched catalog part.
type PartRef struct {
	URL          string
	Digest       string
	LastModified time.Time
}

// VerificationOutcome captures the individual checks performed on a fetched
// catalog part. There is no signature check here: catalog parts carry no
// proof.jws of their own by design -- their integrity is inherited entirely
// from the digest the index declared for them (DigestMatch), not from a
// signature. The manifest and index each have their own signature verified
// separately (ManifestResult.Verified, and the index's own proof checked
// internally by the crawler).
type VerificationOutcome struct {
	DigestMatch bool
	SchemaValid bool
	// VersionOK is false only when a version rollback was detected.
	VersionOK bool
}

// CatalogResult is the outcome of fetching one catalog part referenced by an
// index.
type CatalogResult struct {
	CatalogID    string
	Version      int
	Status       string
	Visibility   string
	SchemaTypes  []string
	Source       PartRef
	Changed      bool
	Verification VerificationOutcome
	// Catalog is omitted when Changed is false, unless the crawl ran in full mode.
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
