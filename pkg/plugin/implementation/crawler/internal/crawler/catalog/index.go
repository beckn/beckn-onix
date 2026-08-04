// Package catalog is the crawler's pure domain: the DeDi index/catalog model,
// change-detection and scope-resolution rules, baseline+change composition, and the
// shared lifecycle/fault vocabulary. It imports only stdlib + pkg/catalogfile
// (never any adapter or telemetry), so the same rules drive an onix plugin, a
// standalone binary, or tests alike. Types here follow the Decentralized
// Catalog "File Specifications" tab.
package catalog

// FileEntry is one baseline or change file listed in the index: an immutable,
// versioned URL with its size and digest. It carries no signature of its own
// -- per the v2 file spec, the file it points at self-signs its own content,
// and the enclosing CatalogEntry self-signs the whole set of file references
// together (see CatalogEntry.Signature).
type FileEntry struct {
	Version int64  `json:"version"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	Digest  string `json:"digest"`
	// Encoding names the artifact packaging: "" / "json" = plain JSON, "gzip"
	// = gzipped JSON (and future codecs). Falls back to the URL suffix when
	// absent. It is a lookup key into the decode registry.
	Encoding string `json:"encoding,omitempty"`
}

// AuthMethod describes how a restricted file's bytes are fetched (the spec's
// "signed-request" download gate). Phase 2; parsed but not yet exercised,
// since Phase 1 takes only openly-fetchable files.
type AuthMethod struct {
	Method           string   `json:"method"` // e.g. "signed-request"
	Header           string   `json:"header"` // e.g. "Authorization"
	SignedHeaders    []string `json:"signedHeaders"`
	FreshnessSeconds int      `json:"freshnessSeconds"`
}

// CatalogEntry is one catalog's record in the index: identity, status,
// visibility, its baseline + change files, and the entry's own self-signature
// over all of the above (file spec: "each catalog entry signs itself").
type CatalogEntry struct {
	CatalogID   string          `json:"catalogId"`
	CatalogType string          `json:"catalogType"`
	Status      string          `json:"status"` // ACTIVE | RETIRED (index/ION wire)
	SchemaTypes []string        `json:"schemaTypes"`
	NetworkIDs  []string        `json:"networkIds"` // absent/empty => public
	AuthMethods []AuthMethod    `json:"authMethods,omitempty"`
	Baseline    FileEntry       `json:"baseline"`
	Changes     []FileEntry     `json:"changes"`
	RetiredAt   string          `json:"retiredAt,omitempty"`
	Signature   EntrySignature  `json:"signature"`
}

// Index is a publisher's catalog index (File Specifications: "a plain Beckn
// file listing the catalogs; DeDi never ingests it"). NodeID is the
// publishing node's domain identity (file spec's nodeId collapse -- this
// prototype's former participantId).
type Index struct {
	NodeID     string         `json:"nodeId"`
	Version    int64          `json:"version"`
	NextUpdate string         `json:"next_update"`
	Catalogs   []CatalogEntry `json:"catalogs"`
}

// Index entry status values (ION wire — distinct from the stored CatalogStatus
// in status.go, which is lowercase "active"/"retired").
const (
	StatusActive  = "ACTIVE"
	StatusRetired = "RETIRED"
)

// LatestVersion is the highest version among the baseline and any change file
// that carries a URL. Placeholder entries without a URL (not yet published
// content) are ignored.
func (e CatalogEntry) LatestVersion() int64 {
	v := e.Baseline.Version
	for _, c := range e.Changes {
		if c.URL != "" && c.Version > v {
			v = c.Version
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
