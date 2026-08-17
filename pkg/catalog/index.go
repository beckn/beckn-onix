package catalog

// index.go — the decentralized-catalog index's own wire schema: which files
// exist for a catalog, at which versions, with which digests, and each
// entry's self-signature. This is the RFC-mandated document shape a crawler
// reads; fetch.go is what actually fetches and verifies one.

// FileEntry is one baseline, change, or latest file listed in an index
// entry: an immutable (except `latest`), versioned URL with its size and
// digest. It carries no signature of its own -- per the file spec, the file
// it points at self-signs its own content, and the enclosing CatalogEntry
// self-signs the whole set of file references together (see
// CatalogEntry.Signature).
//
// Version and FromVersion/ToVersion are mutually exclusive, mirroring the
// spec's two distinct shapes: baseline/latest carry a single `version`
// (Version); a changes[] entry instead carries the exact range it covers,
// `fromVersion`/`toVersion` (FromVersion/ToVersion), matching a change
// file's own fields -- letting a reader confirm the chain is contiguous
// from the index alone, before fetching anything. Use EffectiveVersion for
// "this file's content-lineage version" without caring which shape produced
// it.
type FileEntry struct {
	Version     int64  `json:"version,omitempty"`
	FromVersion int64  `json:"fromVersion,omitempty"`
	ToVersion   int64  `json:"toVersion,omitempty"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest"`
	// Encoding names the artifact packaging: "" / "json" = plain JSON, "gzip"
	// = gzipped JSON (and future codecs). Falls back to the URL suffix when
	// absent -- see pkg/crawler/decode.EncodingFor.
	Encoding string `json:"encoding,omitempty"`
}

// EffectiveVersion is this file's content-lineage version regardless of
// which shape carries it: ToVersion for a changes[] entry, Version for
// baseline/latest.
func (f FileEntry) EffectiveVersion() int64 {
	if f.ToVersion != 0 {
		return f.ToVersion
	}
	return f.Version
}

// EntrySignature is a self-signature's wire shape: which key signed, and the
// base64 Ed25519 signature value. Shared by a catalog index entry's own
// signature and a catalog/change file's embedded signature -- both sign
// "the document with this field removed" (see crawler.VerifySignature).
type EntrySignature struct {
	KeyID string `json:"keyId"`
	Value string `json:"value"`
}

// Dependencies lists a REGULAR catalog entry's declared MASTER dependencies
// (CON-TBD-30). Wrapped in an object (not a bare masters[] array at the top
// level) so a future dependency kind can be added without a breaking schema
// change.
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
// visibility, its baseline + change files, and the entry's own
// self-signature over all of the above ("each catalog entry signs itself").
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
	// Latest is the optional full-file pointer (CON-TBD-36/37): a file a
	// publishing node overwrites in place at a stable URL, for a
	// full-file-only consumer that never applies Changes -- an alternative
	// resolution strategy Fetcher's own baseline+changes path doesn't need
	// and doesn't currently read. Parsed here so it round-trips rather than
	// being silently dropped. Unlike Baseline/Changes, Latest.URL is
	// explicitly exempt from the immutable-URL rule -- a fetch landing
	// mid-overwrite MAY digest-mismatch as a transient publish race rather
	// than tampering. Dropped once RetiredAt is set, same as Baseline/Changes.
	Latest    *FileEntry     `json:"latest,omitempty"`
	RetiredAt string         `json:"retiredAt,omitempty"`
	CrawlHint string         `json:"crawlHint,omitempty"`
	Signature EntrySignature `json:"signature"`
}

// IsRetired reports whether this entry carries the one-way retiredAt
// tombstone. Retirement is a positive fact a caller verifies on the signed
// entry, never inferred from the entry's absence.
func (e CatalogEntry) IsRetired() bool { return e.RetiredAt != "" }

// IsPaused reports whether this entry is explicitly PAUSED (isActive:false,
// not retired). Freely reversible; a paused catalog stays fully indexed --
// this is informational for a caller that wants to distinguish it from
// ACTIVE, not a gate on whether that caller processes the entry.
func (e CatalogEntry) IsPaused() bool { return !e.IsRetired() && e.IsActive != nil && !*e.IsActive }

// LatestVersion is the highest version among the baseline and any change
// file that carries a URL. Placeholder entries without a URL (not yet
// published content) are ignored. This is the content-lineage version,
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

// Index is a publishing node's catalog index ("a plain Beckn file listing
// the catalogs"). There is deliberately no whole-index version field -- an
// index as a whole is unsigned, so a document-level counter would be
// forgeable; whether it changed at all is answered by conditional HTTP
// (ETag/If-Modified-Since) instead, one layer up in Fetcher.
type Index struct {
	NodeID     string         `json:"nodeId"`
	NextUpdate string         `json:"next_update"`
	Catalogs   []CatalogEntry `json:"catalogs"`
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

// DroppedEntry records one index entry FetchIndex declined to trust: its
// self-signature didn't verify (or it didn't parse at all), so it is
// reported rather than silently missing from the result.
type DroppedEntry struct {
	CatalogID string
	Reason    string
}

// IndexConditions are the conditional-GET validators a caller may have
// stored from a prior fetch of the same index URL.
type IndexConditions struct {
	ETag         string
	LastModified string
}

// IndexResult is one FetchIndex call's outcome: the parsed index (entries
// whose self-signature didn't verify are excluded and reported in Dropped),
// or NotModified if the conditions matched.
type IndexResult struct {
	Index        Index
	NotModified  bool
	ETag         string
	LastModified string
	Dropped      []DroppedEntry
}
