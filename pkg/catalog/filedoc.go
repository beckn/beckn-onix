package catalog

// filedoc.go — the self-signed catalog/change file document shapes
// (as opposed to index.go's index *entries*, which only point at these
// files by URL/size/digest). A publisher builds and signs these; Apply
// (catalogfile.go) reads a change file's content fields back out of one.

import (
	"encoding/json"
	"time"
)

// FileSignature is a catalog/change file's own embedded self-signature --
// per the file spec, {keyId, canonicalization, value}. Distinct from an
// index entry's own EntrySignature (index.go), which carries no
// canonicalization field -- the two are signed over differently-shaped
// documents and the spec gives each its own wire shape.
type FileSignature struct {
	KeyID            string `json:"keyId"`
	Canonicalization string `json:"canonicalization"`
	Value            string `json:"value"`
}

// CatalogFileDoc is a baseline (or "latest") file's self-signed envelope:
// the submitted Catalog object wrapped with the file-level identity/
// freshness fields (CatalogID/Version/NextUpdate) the schema requires as
// siblings of "catalog", so the file is independently verifiable without
// the index entry that points at it. RetiredAt is set only on the one-time
// final write to a retired catalog's "latest" URL -- an ordinary baseline
// is an immutable, versioned snapshot nobody expects to reflect events
// after its own publish time, so it never carries one.
type CatalogFileDoc struct {
	CatalogID  string          `json:"catalogId"`
	Version    int64           `json:"version"`
	NextUpdate time.Time       `json:"next_update"`
	Catalog    json.RawMessage `json:"catalog"`
	RetiredAt  *time.Time      `json:"retiredAt,omitempty"`
	Signature  FileSignature   `json:"signature"`
}
