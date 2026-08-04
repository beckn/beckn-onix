package catalog

// signature.go — the two self-signature shapes the v2 file spec defines:
// EntrySignature (a catalog index entry signing itself, {catalogId,
// catalogType, status, networkIds, schemaTypes, baseline, changes} together)
// and the identically-shaped signature embedded in a fetched catalog
// file/change file's own body (signing that document's own content). Both
// are verified the same way: JCS-canonicalize the enclosing JSON with
// "signature" removed, Ed25519-verify against the publisher's registered
// key. See fetch.verifyEntrySignature / fetch.verifyFileSignature.

// EntrySignature is the plain Ed25519 signature a catalog index entry (or a
// catalog file/change file) carries over its own JCS-canonicalized content
// with this field removed. There is no per-signature expiry in this model --
// unlike the superseded per-file tuple, freshness is bounded by the
// enclosing index's own next_update, not by anything carried in the
// signature itself.
type EntrySignature struct {
	KeyID string `json:"keyId"`
	Value string `json:"value"`
}
