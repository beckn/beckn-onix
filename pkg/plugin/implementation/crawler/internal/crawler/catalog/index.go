// Package catalog is the crawler's pure domain: the DeDi index/catalog model,
// change-detection and scope-resolution rules, baseline+change composition, and the
// shared lifecycle/fault vocabulary. It imports only stdlib + pkg/catalogfile
// (never any adapter or telemetry), so the same rules drive an onix plugin, a
// standalone binary, or tests alike. Types here follow RFC NFH-014
// (Decentralized Catalog Publishing and Discovery).
package catalog

// FileEntry is one baseline, change, or latest file listed in the index: an
// immutable (except `latest`), versioned URL with its size and digest. It
// carries no signature of its own -- per NFH-014, the file it points at
// self-signs its own content, and the enclosing CatalogEntry self-signs the
// whole set of file references together (see CatalogEntry.Signature).
//
// Version and FromVersion/ToVersion are mutually exclusive, mirroring the RFC's
// two distinct shapes: baseline/latest carry a single `version` (Version);
// a changes[] entry instead carries the exact range it covers, `fromVersion`/
// `toVersion` (FromVersion/ToVersion), matching CatalogChangeFile's own
// fields -- letting a DS confirm the chain is contiguous from the index
// alone, before fetching anything. Use EffectiveVersion for "this file's
// content-lineage version" without caring which shape produced it.
type FileEntry struct {
	Version     int64  `json:"version,omitempty"`
	FromVersion int64  `json:"fromVersion,omitempty"`
	ToVersion   int64  `json:"toVersion,omitempty"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest"`
	// Encoding names the artifact packaging: "" / "json" = plain JSON, "gzip"
	// = gzipped JSON (and future codecs). Falls back to the URL suffix when
	// absent. It is a lookup key into the decode registry.
	Encoding string `json:"encoding,omitempty"`
}

// EffectiveVersion is this file's content-lineage version regardless of which
// shape carries it: ToVersion for a changes[] entry, Version for baseline/
// latest.
func (f FileEntry) EffectiveVersion() int64 {
	if f.ToVersion != 0 {
		return f.ToVersion
	}
	return f.Version
}

// Dependencies lists a REGULAR catalog entry's declared MASTER dependencies
// (NFH-014 §10.3, CON-TBD-30). Wrapped in an object (not a bare masters[]
// array at the top level) so a future dependency kind can be added without a
// breaking schema change.
type Dependencies struct {
	Masters []MasterDependency `json:"masters,omitempty"`
}

// MasterDependency is one MASTER catalog a REGULAR catalog's resources
// extend. IndexURL is an unauthenticated locator hint only (CON-TBD-31) --
// anything fetched from it MUST be verified exactly as via ordinary
// discovery, never trusted on the strength of this hint alone.
type MasterDependency struct {
	CatalogID string `json:"catalogId"`
	IndexURL  string `json:"indexUrl"`
}

// CatalogEntry is one catalog's record in the index: identity, lifecycle,
// visibility, its baseline + change files, and the entry's own self-signature
// over all of the above (NFH-014: "each catalog entry signs itself").
//
// IsActive is a pointer because its absence (nil) and an explicit false are
// different facts on the wire: a RETIRED entry omits it entirely (there is
// nothing to be active or paused about once retired), while a PAUSED entry
// carries it as an explicit false. Collapsing those to a plain bool would
// make "never set" and "explicitly paused" indistinguishable.
type CatalogEntry struct {
	CatalogID    string        `json:"catalogId"`
	EntryVersion int64         `json:"entryVersion"`
	CatalogType  string        `json:"catalogType"`
	Dependencies *Dependencies `json:"dependencies,omitempty"`
	SchemaTypes  []string      `json:"schemaTypes"`
	NetworkIDs   []string      `json:"networkIds"` // absent/empty => public
	IsActive     *bool         `json:"isActive,omitempty"`
	Baseline     FileEntry     `json:"baseline"`
	Changes      []FileEntry   `json:"changes"`
	// Latest is the optional full-file pointer (NFH-014 CON-TBD-36/37): a
	// CatalogFile a PN overwrites in place at a stable URL, for a full-file-
	// only consumer that never applies Changes -- an alternative resolution
	// strategy this crawler's own incremental (baseline+changes+entryVersion
	// cursor) pipeline doesn't need and doesn't currently read. Parsed here so
	// it round-trips rather than being silently dropped. Unlike Baseline/
	// Changes, Latest.URL is explicitly exempt from the immutable-URL rule --
	// a fetch landing mid-overwrite MAY digest-mismatch as a transient publish
	// race rather than tampering. Dropped once RetiredAt is set, same as
	// Baseline/Changes.
	Latest    *FileEntry     `json:"latest,omitempty"`
	RetiredAt string         `json:"retiredAt,omitempty"`
	CrawlHint string         `json:"crawlHint,omitempty"`
	Signature EntrySignature `json:"signature"`
}

// Index is a publisher's catalog index (NFH-014: "a plain Beckn file listing
// the catalogs; DeDi never ingests it"). There is deliberately no whole-index
// version field (§Versioning, "There is no whole-index version field") -- an
// index as a whole is unsigned, so a document-level counter would be
// forgeable; whether it changed at all is answered by conditional HTTP
// (ETag/If-Modified-Since) instead, one layer up in the fetch client.
type Index struct {
	NodeID     string         `json:"nodeId"`
	NextUpdate string         `json:"next_update"`
	Catalogs   []CatalogEntry `json:"catalogs"`
}

// IsRetired reports whether this entry carries the one-way retiredAt
// tombstone (NFH-014 §10.4). Retirement is a positive fact a DS verifies on
// the signed entry, never inferred from the entry's absence.
func (e CatalogEntry) IsRetired() bool { return e.RetiredAt != "" }

// IsPaused reports whether this entry is explicitly PAUSED (isActive:false,
// not retired). Freely reversible; a paused catalog stays fully indexed
// (§10.4) -- this is informational for callers that want to distinguish it
// from ACTIVE, not a gate on whether the crawler processes the entry.
func (e CatalogEntry) IsPaused() bool { return !e.IsRetired() && e.IsActive != nil && !*e.IsActive }

// LatestVersion is the highest version among the baseline and any change file
// that carries a URL. Placeholder entries without a URL (not yet published
// content) are ignored. This is the content-lineage version (§Versioning),
// distinct from EntryVersion.
func (e CatalogEntry) LatestVersion() int64 {
	v := e.Baseline.Version
	for _, c := range e.Changes {
		if c.URL != "" && c.EffectiveVersion() > v {
			v = c.EffectiveVersion()
		}
	}
	return v
}

// FindCatalog returns the index entry for catalogID.
func FindCatalog(idx Index, catalogID string) (CatalogEntry, bool) {
	for _, c := range idx.Catalogs {
		if c.CatalogID == catalogID {
			return c, true
		}
	}
	return CatalogEntry{}, false
}
