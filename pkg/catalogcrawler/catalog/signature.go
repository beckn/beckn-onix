package catalog

// Signature is a per-file signed tuple binding
// {catalogId, version, url, digest, validUntil} (File Specifications: "the
// signed entry is a tuple, not a bare hash"). Verified in Phase 2; carried
// through Phase 1.
type Signature struct {
	KeyID      string `json:"keyId"`
	Value      string `json:"value"`
	ValidUntil string `json:"validUntil"`
}
