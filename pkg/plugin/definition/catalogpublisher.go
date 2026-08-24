package definition

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// CatalogSubmission is one catalog's input to a publish call: the plain
// Beckn Catalog object (no context/message envelope) plus the publisher-
// declared metadata that only the publisher can know -- NetworkIds is
// never derived from the catalog content itself. Catalogs are public,
// unconditionally (RFC NFH-014, "Catalog access is public") -- there is
// no restricted-catalog or per-catalog-auth concept; NetworkIds is a
// Discovery-service relevance filter only, never an access control.
//
// IsActive is deliberately absent here: the index entry's isActive
// mirrors Catalog's own pre-existing isActive field (NFH-014 §Schema
// Changes, "Catalog Index"), so Publish reads it out of Catalog itself
// rather than duplicating it as a second input that could disagree.
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

	// Dependencies lists, for a REGULAR catalog, every MASTER catalog any
	// of its resources currently extend via
	// resourceDirectives[].extends.masterResourceId (NFH-014 §10.3,
	// CON-TBD-30). Publish does not derive this from Catalog's own
	// resource content -- a masterResourceId names a resource, not the
	// catalog it lives in, so resolving which catalog (and which index)
	// owns it needs cross-catalog knowledge only the caller has. Nil for a
	// catalog with no MASTER dependencies (including every MASTER catalog
	// itself).
	Dependencies []MasterDependency

	// CrawlHint is an optional suggested crawl frequency (NFH-014's
	// "comparable to a sitemap's changefreq") a crawler MAY honor. Empty
	// means no hint is published.
	CrawlHint string

	Catalog json.RawMessage
}

// MasterDependency is one MASTER catalog a REGULAR catalog's resources
// extend, per NFH-014 §10.3's dependencies.masters[]. IndexURL is an
// unauthenticated locator hint only (CON-TBD-31) -- a crawler still
// verifies whatever it fetches from it exactly as it would via ordinary
// discovery. Version is the MASTER's baseline.version last validated
// against -- the caller's responsibility to keep current as that changes,
// same as the rest of this struct (Publish only writes it through).
type MasterDependency struct {
	CatalogID string
	Version   int
	IndexURL  string
}

// FileRef is a pointer to one published catalog file (a baseline or a
// change file): its own version, where it lives, its size and digest.
// Callers carry these forward across Publish calls -- Publish holds no
// storage-backed state of its own (see PriorCatalogState). File-level
// integrity now comes from the file's own embedded self-signature (file
// spec v2, "Catalog files and change files"), not a signature carried
// here -- trust for the index entry as a whole comes from
// CatalogPublishOutcome's catalog-entry-level signature instead.
// FromVersion is meaningful for a change file only (mirroring
// CatalogChangeFile's own fromVersion) -- zero for a baseline/latest
// FileRef. It must be carried explicitly rather than reconstructed from
// sequence order: once a catalog has been compacted, PriorCatalogState.
// ChangeFiles legitimately contains superseded (pre-compaction) entries
// alongside live (post-baseline) ones for the CON-TBD-32 grace period,
// so "whatever came immediately before it in the slice" is no longer a
// safe way to infer a change file's real FromVersion.
type FileRef struct {
	FromVersion int
	Version     int
	URL         string
	Size        int64
	Digest      string

	// Encoding names the artifact packaging ("" / "json" = plain JSON,
	// "gzip" = gzipped JSON) -- carried through so a round trip via this
	// type doesn't silently lose it even though a reader can still fall
	// back to the URL's own suffix.
	Encoding string
}

// PriorCatalogState is what a caller must supply, per catalogId, to get
// incremental (diffing) behavior instead of a fresh baseline. Publish is a
// pure function of (submissions, prior state) -> result; it never reads or
// writes any storage of its own. Catalog is the full, reconstructed
// content last published for this catalogId (baseline with every change
// file applied) -- diffing compares the new submission against this, not
// against the original baseline alone. A catalog's "current file version"
// is implicit: the last entry in ChangeFiles, or BaselineFile's version if
// ChangeFiles is empty -- matching the file spec (a file-lineage version
// lives per-file, not per-catalog-entry).
//
// EntryVersion and the metadata fields below are the entry-level state
// (NFH-014 §Versioning's "catalog-entry level -- has anything changed"):
// distinct from the file-lineage version above, EntryVersion bumps on any
// change to the entry, content or metadata, so Publish needs the
// previously-published values to detect a metadata-only change (e.g. only
// NetworkIds edited, no new file) and still bump it correctly.
//
// ChangeFiles doubles as the compaction grace-period mechanism (NFH-014
// §10.1, CON-TBD-32): Publish itself holds no timer or storage, so
// whether -- and for how long -- to keep passing a compacted baseline's
// pre-compaction change files here (so they stay listed, not just hosted,
// for a DS mid-lineage) is entirely the caller's own policy to enforce by
// what it includes or drops from this slice on the next call.
type PriorCatalogState struct {
	Catalog      json.RawMessage
	BaselineFile *FileRef
	ChangeFiles  []FileRef

	EntryVersion int
	CatalogType  string
	NetworkIds   []string
	SchemaTypes  []string
	IsActive     bool
	Dependencies []MasterDependency
	CrawlHint    string

	// LatestPublished reports whether a "latest" full-CatalogFile pointer
	// was previously published for this catalog (NFH-014 §Schema Changes,
	// CON-TBD-38) -- independent of whether Config.PublishLatest is
	// currently on, since retiring a catalog that had one MUST make one
	// final write to that same stable URL populating CatalogFile.retiredAt,
	// regardless of today's config. False (the zero value) for a catalog
	// that never had one, in which case retiring it touches no "latest"
	// file at all.
	LatestPublished bool
}

// PublishRequest is the input to CatalogPublisher.Publish.
type PublishRequest struct {
	Catalogs []CatalogSubmission

	// PriorState supplies, per catalogId, what was last published for the
	// catalogs actually submitted in Catalogs -- the only way Publish can
	// produce a change file instead of a fresh baseline. A submitted
	// catalogId absent from this map is always published as a new
	// baseline, same as when ForceBaseline is set.
	//
	// The catalogpublisher plugin implementation ignores this field: since
	// Publish now owns the full publish operation end to end (see Publish's
	// doc comment), it loads its own prior state from its configured
	// CatalogBlobStore rather than trusting a caller-supplied snapshot. The
	// field stays on this struct as a caller-supplied *override* path for a
	// future/alternate CatalogPublisher implementation that might want one
	// (e.g. a test double, or a caller that legitimately owns storage
	// itself) -- deciding whether to formally repurpose or remove it is a
	// separate, later, better-scoped change.
	PriorState map[string]PriorCatalogState

	// Retire marks these catalogIds RETIRED this call: a tombstone entry
	// (retiredAt, no isActive/baseline/changes) replaces whatever was
	// there, per NFH-014 §10.4's "a retired catalog stays as a tombstone"
	// rule. A retired catalogId's prior CatalogType/NetworkIds/SchemaTypes
	// and EntryVersion still come from PriorState[id] -- a tombstone keeps
	// those (NFH-014 Appendix A, Example 4's third entry), it only drops
	// isActive/baseline/changes. A catalogId present in both Retire and
	// Catalogs is published normally; Retire is ignored for it.
	Retire []string

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

	// SignedEntry is this catalog's complete, already-signed catalogs[]
	// entry -- ready to hand a catalog storage layer (e.g.
	// pkg/catalog/store's CatalogUpdate.SignedEntry) to merge into the
	// index. Publish itself never assembles or persists the index as a
	// whole; it only ever produces this one entry's bytes.
	SignedEntry json.RawMessage

	// Version is this catalog's new current file-lineage version after
	// this call (the version stamped on the file just published, or the
	// unchanged current version on a no-op/metadata-only change) --
	// distinct from EntryVersion below (NFH-014 §Versioning).
	Version int

	// EntryVersion is this catalog's new entry-level version -- bumped
	// whenever Changed is true, whether that's a content change (Mode
	// "baseline"/"change") or a metadata-only one (Mode "metadata").
	// Callers must carry this forward into the next call's
	// PriorState[catalogId].EntryVersion.
	EntryVersion int

	Changed bool // false = no-op: diffed against PriorState and found no changes at all, content or metadata
	Digest  string

	// Mode is "baseline" (fresh full-file publish, including a forced
	// compaction), "change" (a diffed delta was produced), "metadata" (no
	// file republished, but NetworkIds/SchemaTypes/CatalogType/IsActive/
	// Dependencies/CrawlHint changed, so EntryVersion still bumped), or
	// "unchanged". Content holds the new file's canonical (never
	// compressed) bytes for "baseline"/"change" and is nil otherwise --
	// digest/signature verification and any programmatic inspection always
	// use this, never ServedContent.
	Mode    string
	Content json.RawMessage

	// ServedContent is what a caller should actually write to storage for
	// Content above -- identical to Content when Compressed is false, or
	// its gzip-compressed bytes when Compressed is true (NFH-014 §10.1,
	// "Compression"). Nil whenever Content is nil.
	ServedContent []byte

	// Compressed reports whether ServedContent (and LatestServedContent
	// below) are gzip-compressed relative to Content/LatestContent --
	// mirrors Config.Gzip at the time of this Publish call. The index
	// entry's own file reference URLs already carry the matching ".gz"
	// extension; a caller writing ServedContent to a filename derived from
	// that URL needs no separate bookkeeping.
	Compressed bool

	// LatestContent/LatestServedContent/LatestDigest are set whenever
	// Config.PublishLatest is on (NFH-014 §Schema Changes, "latest"): a
	// full CatalogFile mirroring this catalog's current content,
	// regenerated on every call regardless of Mode -- a caller writes
	// LatestServedContent to the same fixed, overwritten-in-place URL
	// every time (never a new, versioned one like Content above). Nil/
	// empty when PublishLatest is off.
	LatestContent       json.RawMessage
	LatestServedContent []byte
	LatestDigest        string
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

// PublishResult is the output of a Publish call: per-catalog outcomes and
// errors, plus the two fields (NodeID/NextUpdate) a caller needs to stamp
// on the index document it assembles from those outcomes -- Publish
// itself never assembles or persists a catalog index as a whole (that is
// a storage layer's job, e.g. pkg/catalog/store.Store.Publish, which
// merges each outcome's SignedEntry into the index it already holds).
// There is no DeDi manifest here: the index's location is declared
// directly in the publisher's own DeDi registry record
// (meta.catalog_index_url, see IndexURL and
// core/module/handler/catalogPublishHandler.go's
// checkRegistryLinksCatalogIndex), not via a separate manifest document.
type PublishResult struct {
	PublishedAt time.Time

	// NodeID and NextUpdate are the index document's own top-level
	// fields (NFH-014's "nodeId"/"next_update") -- domain and freshness
	// window, resolved here from KeyManager's Keyset and Config.NextUpdateIn
	// respectively, so a caller assembling the index doesn't have to
	// duplicate that resolution itself.
	NodeID     string
	NextUpdate *time.Time

	Catalogs []CatalogPublishOutcome
	Errors   []PublishError

	// Retirements carries each retired catalogId's signed tombstone entry
	// -- separate from Catalogs, which reports outcomes for submitted
	// catalogs only; retirement is a different kind of event with no
	// Version/EntryVersion/Mode of its own to report there.
	Retirements []RetirementOutcome

	// RetiredLatest carries the final, self-signed CatalogFile write
	// CON-TBD-38 requires for each retired catalogId whose PriorState had
	// LatestPublished set: the same stable "latest" URL as before, now
	// carrying CatalogFile.retiredAt, so a consumer that only ever fetches
	// "latest" directly (never revisiting the index) can still learn the
	// catalog is gone.
	RetiredLatest []RetiredCatalogFile

	// Warnings carries non-fatal operational notices produced alongside an
	// otherwise-successful Publish call -- e.g. the registry catalog-index-
	// link check reporting that this node's DeDi record doesn't yet list
	// its catalog index. Distinct from Errors: an entry here is never a
	// per-catalog business failure, just something an operator should look
	// at.
	Warnings []string
}

// RetirementOutcome is one retired catalog's signed tombstone entry --
// ready to hand a catalog storage layer to merge into the index, same as
// CatalogPublishOutcome.SignedEntry for a submitted catalog.
type RetirementOutcome struct {
	CatalogID   string
	SignedEntry json.RawMessage
}

// RetiredCatalogFile is one retired catalog's final "latest" write (see
// PublishResult.RetiredLatest). Content is the canonical (uncompressed)
// signed bytes; ServedContent is what a caller should actually write to
// the "latest" URL -- identical to Content unless Compressed is true, same
// convention as CatalogPublishOutcome's Content/ServedContent pair.
type RetiredCatalogFile struct {
	CatalogID     string
	Content       json.RawMessage
	ServedContent []byte
	Compressed    bool
}

// CatalogPublisher turns a publisher's catalog submissions into a catalog
// index whose file entries carry their own signatures. It is the producing
// side of the chain definition.Crawler consumes and verifies.
//
// Publish now owns the full publish operation end to end, not just the
// diffing/signing math: it loads this catalog's prior state from its own
// configured CatalogBlobStore, delegates diffing/signing/versioning to
// pkg/catalog/publisher, persists the result back to that same storage
// backend, and -- if configured -- checks whether this node's DeDi registry
// record already links its catalog index, surfacing a miss as a
// PublishResult.Warnings entry rather than failing the call. This replaces
// the earlier design where Publish was a pure function of (submissions,
// prior state) -> result with no storage of its own, and a caller (the
// generic HTTP handler) owned loading/persisting state around it -- that
// responsibility has moved into the plugin so the handler can stay fully
// generic.
type CatalogPublisher interface {
	Publish(ctx context.Context, req PublishRequest) (PublishResult, error)

	// DecodeRequest parses and validates an inbound HTTP request into a
	// PublishRequest: method check, body read, wire-format JSON decoding,
	// referential/business validation, and any request-shape-specific
	// schema checks (e.g. NFH-014's schemaTypes). Any error here means the
	// request itself is malformed or invalid, surfaced by the generic
	// handler as a transport-level 400 -- never a partial/business failure
	// (that's what Publish's own PublishError/Warnings are for).
	DecodeRequest(ctx context.Context, r *http.Request) (PublishRequest, error)

	// IndexURL returns the public location this publisher's catalog index
	// is (or will be) reachable at -- callers use this to check the
	// publisher's own DeDi registry record for a matching
	// meta.catalog_index_url before publishing (see
	// pkg/plugin/implementation/catalogpublisher/registrylink.go), without
	// this package knowing anything about DeDi or registries itself.
	IndexURL() string
}

// CatalogPublisherProvider is the plugin constructor interface. blobStore
// is required (non-nil): Publish always persists what it produces
// somewhere, so a CatalogPublisher implementation cannot be constructed
// without one. registryMetadata is optional -- nil when the registry
// catalog-index-link check isn't configured (or the configured Registry
// plugin doesn't implement RegistryMetadataLookup); a non-nil value is only
// ever used to run that check.
type CatalogPublisherProvider interface {
	New(ctx context.Context, keyManager KeyManager, blobStore CatalogBlobStore, registryMetadata RegistryMetadataLookup, config map[string]string) (CatalogPublisher, func() error, error)
}
