package catalog

// signature.go — the per-file signed tuple carried in each index FileEntry, plus
// the accessors the fetch layer needs to verify it (expiry parsing and the
// catalog binding the wire format leaves implicit).

import (
	"fmt"
	"time"
)

// Signature is a per-file signed tuple binding
// {catalogId, version, url, digest, validUntil} (File Specifications: "the
// signed entry is a tuple, not a bare hash"). The fetch layer verifies it
// against trusted publisher keys before any fetched bytes are used.
type Signature struct {
	KeyID      string `json:"keyId"`
	Value      string `json:"value"`
	ValidUntil string `json:"validUntil"`

	// CatalogID is the enclosing catalog's id. It is not on the wire — the index
	// nests each file entry inside its catalog entry — but the signed tuple
	// covers catalogId and a FileEntry travels to the fetch layer on its own, so
	// the index decoder stamps it (StampCatalogIDs) right after parsing.
	CatalogID string `json:"-"`

	// ParticipantID is the enclosing index's participantId. Like CatalogID it is
	// not on this object's wire form and is stamped by StampCatalogIDs. It is
	// NOT part of the signed tuple: it is the registry subscriber id the fetch
	// layer resolves KeyID under, so a FileEntry that travels alone still knows
	// whose key to ask the registry for.
	ParticipantID string `json:"-"`
}

// ValidUntilTime parses the signature's expiry (RFC 3339). An absent or
// unparseable value is an error, never a zero time: the expiry check has to fail
// closed rather than read "no expiry given" as "never expires".
func (s Signature) ValidUntilTime() (time.Time, error) {
	if s.ValidUntil == "" {
		return time.Time{}, fmt.Errorf("signature has no validUntil")
	}
	t, err := time.Parse(time.RFC3339, s.ValidUntil)
	if err != nil {
		return time.Time{}, fmt.Errorf("signature validUntil %q: %w", s.ValidUntil, err)
	}
	return t, nil
}

// StampCatalogIDs binds every file entry in idx to its enclosing catalog's id
// and to the index's participantId, so a FileEntry alone carries everything the
// signature check needs. Both come from the index structure (the same document
// as the signature) — that is fine, because trust rides on the key, which the
// fetch layer resolves from the network registry and never takes from the file
// being checked. See fetch.RegistryKeys for why a forged participantId fails
// closed rather than granting trust.
func StampCatalogIDs(idx *Index) {
	for i := range idx.Catalogs {
		e := &idx.Catalogs[i]
		e.Baseline.Signature.CatalogID = e.CatalogID
		e.Baseline.Signature.ParticipantID = idx.ParticipantID
		for j := range e.Changes {
			e.Changes[j].Signature.CatalogID = e.CatalogID
			e.Changes[j].Signature.ParticipantID = idx.ParticipantID
		}
	}
}
