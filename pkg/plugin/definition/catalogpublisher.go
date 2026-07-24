package definition

import (
	"context"
	"encoding/json"
	"time"
)

// AuthMethod is one way a restricted catalog, or the catalog index itself,
// authenticates a crawler's fetch -- e.g. "signed-challenge" (Decentralized
// Catalog file spec, "Auth methods for restricted downloads"). A list, not
// an enum, so new methods extend without changing the shape.
type AuthMethod struct {
	Method           string
	Algorithm        string
	Header           string
	Challenge        []string
	FreshnessSeconds int
}

// CatalogSubmission is one catalog's input to a publish call: the plain
// Beckn Catalog object (no context/message envelope) plus the publisher-
// declared metadata that only the publisher can know -- NetworkIds and
// AuthMethods are never derived from the catalog content itself.
type CatalogSubmission struct {
	// CatalogID is the full participant-scoped id, e.g.
	// "open-economy.nfh.global/electronics-2026".
	CatalogID string

	// CatalogType defaults to "REGULAR" when empty (see the file spec's
	// catalogType field; "MASTER" is the design doc's still-open
	// "MASTER catalogs" case).
	CatalogType string

	SchemaTypes []string

	// NetworkIds scopes this catalog to specific networks; empty/nil means
	// public (file spec: "networkIds ... Empty or absent means public").
	NetworkIds []string

	// AuthMethods is only meaningful when NetworkIds is non-empty.
	AuthMethods []AuthMethod

	Catalog json.RawMessage
}

// FileRef is a signed pointer to one published catalog file (a baseline or
// a change file): its own version, where it lives, its size and digest,
// and the per-file signature tuple binding all of it together (file spec,
// "The signed entry is a tuple, not a bare hash"). Callers carry these
// forward across Publish calls -- Publish holds no storage-backed state of
// its own (see PriorCatalogState).
type FileRef struct {
	Version             int
	URL                 string
	Size                int64
	Digest              string
	SignatureKeyID      string
	SignatureValue      string
	SignatureValidUntil time.Time
}

// PriorCatalogState is what a caller must supply, per catalogId, to get
// incremental (diffing) behavior instead of a fresh baseline. Publish is a
// pure function of (submissions, prior state) -> result; it never reads or
// writes any storage of its own. Catalog is the full, reconstructed
// content last published for this catalogId (baseline with every change
// file applied) -- diffing compares the new submission against this, not
// against the original baseline alone. A catalog's "current version" is
// implicit: the last entry in ChangeFiles, or BaselineFile's version if
// ChangeFiles is empty -- there is no separate top-level version field,
// matching the file spec (version lives per-file, not per-catalog-entry).
type PriorCatalogState struct {
	Catalog      json.RawMessage
	BaselineFile *FileRef
	ChangeFiles  []FileRef
}

// PublishRequest is the input to CatalogPublisher.Publish.
type PublishRequest struct {
	Catalogs []CatalogSubmission

	// PriorState supplies, per catalogId, what was last published for the
	// catalogs actually submitted in Catalogs -- the only way Publish can
	// produce a change file instead of a fresh baseline. A submitted
	// catalogId absent from this map is always published as a new
	// baseline, same as when ForceBaseline is set.
	PriorState map[string]PriorCatalogState

	// CarryForward holds raw catalogs[] entries from the last published
	// index for catalogIds NOT present in Catalogs and NOT in Retire --
	// the catalog index lists every catalog the publisher has, not just
	// the ones touched this call, so these are included verbatim in the
	// output index unmodified. Publish only reads each entry's
	// "catalogId" field to de-duplicate against Catalogs/Retire; it does
	// not otherwise interpret them.
	CarryForward []json.RawMessage

	// Retire marks these catalogIds RETIRED this call: a tombstone entry
	// (status + retiredAt, no files) replaces whatever was there, per the
	// file spec's "a retired catalog stays as a tombstone" rule. A
	// catalogId present in both Retire and Catalogs is published
	// normally; Retire is ignored for it.
	Retire []string

	// PriorIndexVersion is the last published index-level version (the
	// crawler's cursor over the whole index, distinct from any one
	// catalog's file versions) -- 0 for a brand-new index.
	PriorIndexVersion int

	// ForceBaseline bypasses diffing against PriorState and always emits a
	// fresh baseline. For a catalog with no prior state this is a no-op
	// (already the default); for one with prior state, this is how a
	// caller triggers compaction -- a fresh baseline at the next version,
	// discarding the accumulated change list (file spec, "Compaction").
	ForceBaseline bool
}

// CatalogPublishOutcome reports what happened to one submitted catalog.
type CatalogPublishOutcome struct {
	CatalogID string

	// Version is this catalog's new current version after this call (the
	// version stamped on the file just published, or the unchanged
	// current version on a no-op).
	Version int

	Changed bool // false = no-op: diffed against PriorState and found no changes at all
	Digest  string

	// Mode is "baseline" (fresh full-file publish, including a forced
	// compaction), "change" (a diffed delta was produced), or "unchanged".
	// Content holds the new file's bytes for "baseline"/"change" and is
	// nil for "unchanged".
	Mode    string
	Content json.RawMessage
}

// PublishError is a non-fatal, per-catalog failure -- one bad submission
// must not fail the whole publish call, mirroring definition.CrawlError on
// the crawler side.
type PublishError struct {
	CatalogID string
	Stage     string // "validate" | "diff" | "sign"
	Reason    string
	Fatal     bool
}

// PublishResult is the output of a Publish call: the manifest (signed) and
// the catalog index (unsigned as a whole; trust rides on each file's own
// signature -- file spec, "The catalog index"), ready to be handed to a
// storage layer (not this plugin's concern -- see ArtifactStore, not yet
// built), plus per-catalog outcomes and errors.
type PublishResult struct {
	PublishedAt  time.Time
	Manifest     json.RawMessage
	Index        json.RawMessage
	IndexVersion int
	Catalogs     []CatalogPublishOutcome
	Errors       []PublishError
}

// CatalogPublisher turns a publisher's catalog submissions into a signed
// manifest and a catalog index whose file entries carry their own
// signatures. It is the producing side of the chain definition.Crawler
// consumes and verifies.
type CatalogPublisher interface {
	Publish(ctx context.Context, req PublishRequest) (PublishResult, error)
}

// CatalogPublisherProvider is the plugin constructor interface.
type CatalogPublisherProvider interface {
	New(ctx context.Context, keyManager KeyManager, config map[string]string) (CatalogPublisher, func() error, error)
}
