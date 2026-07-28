// Package catalogcrawler is the framework-agnostic crawler engine: it
// reads a publisher's catalog index, decides what changed, resolves each
// catalog from its baseline + change files, and pushes the result to a
// Discovery service.
//
// It has no onix (core/module, pkg/plugin) imports, so the same engine can
// be driven by an onix plugin, a standalone binary, or tests alike. Types
// here follow the Decentralized Catalog "File Specifications" tab.
package catalogcrawler

// Signature is a per-file signed tuple binding
// {catalogId, version, url, digest, validUntil} (File Specifications: "the
// signed entry is a tuple, not a bare hash"). Verified in Phase 2; carried
// through Phase 1.
type Signature struct {
	KeyID      string `json:"keyId"`
	Value      string `json:"value"`
	ValidUntil string `json:"validUntil"`
}

// FileEntry is one baseline or change file listed in the index: an
// immutable, versioned URL with its size, digest, and signed tuple.
type FileEntry struct {
	Version int64  `json:"version"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	Digest  string `json:"digest"`
	// Encoding names the artifact packaging: "" / "json" = plain JSON, "gzip"
	// = gzipped JSON (and future codecs). Falls back to the URL suffix when
	// absent. It is a lookup key into the decode codec registry.
	Encoding  string    `json:"encoding,omitempty"`
	Signature Signature `json:"signature"`
}

// AuthMethod describes how a restricted file's bytes are fetched (the
// spec's "signed-request" download gate). Phase 2; parsed but not yet
// exercised, since Phase 1 takes only openly-fetchable files.
type AuthMethod struct {
	Method           string   `json:"method"` // e.g. "signed-request"
	Header           string   `json:"header"` // e.g. "Authorization"
	SignedHeaders    []string `json:"signedHeaders"`
	FreshnessSeconds int      `json:"freshnessSeconds"`
}

// CatalogEntry is one catalog's record in the index: identity, status,
// visibility, and its baseline + change files.
type CatalogEntry struct {
	CatalogID   string       `json:"catalogId"`
	CatalogType string       `json:"catalogType"`
	Status      string       `json:"status"` // ACTIVE | RETIRED
	SchemaTypes []string     `json:"schemaTypes"`
	NetworkIDs  []string     `json:"networkIds"` // absent/empty => public
	AuthMethods []AuthMethod `json:"authMethods,omitempty"`
	Baseline    FileEntry    `json:"baseline"`
	Changes     []FileEntry  `json:"changes"`
	RetiredAt   string       `json:"retiredAt,omitempty"`
}

// Index is a publisher's catalog index (File Specifications: "a plain
// Beckn file listing the catalogs; DeDi never ingests it").
type Index struct {
	ParticipantID string         `json:"participantId"`
	Version       int64          `json:"version"`
	NextUpdate    string         `json:"next_update"`
	Catalogs      []CatalogEntry `json:"catalogs"`
}

// Catalog status values (File Specifications).
const (
	StatusActive  = "ACTIVE"
	StatusRetired = "RETIRED"
)

// IsPublic reports whether the catalog is visible to everyone — no
// networkIds scope it (File Specifications: "empty or absent means public").
func (e CatalogEntry) IsPublic() bool { return len(e.NetworkIDs) == 0 }

// LatestVersion is the highest version among the baseline and any change
// file that carries a URL. Placeholder entries without a URL (not yet
// published content) are ignored.
func (e CatalogEntry) LatestVersion() int64 {
	v := e.Baseline.Version
	for _, c := range e.Changes {
		if c.URL != "" && c.Version > v {
			v = c.Version
		}
	}
	return v
}
