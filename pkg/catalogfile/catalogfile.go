// Package catalogfile implements the file spec's change-file application
// rule ("Catalog files and change files"): a change file carries what
// changed between two consecutive versions, keyed by id never by
// position, as upserts (added or updated items, replaced by id) and
// removals (ids only). Both catalogpublisher's CLI (reconstructing prior
// state to diff against) and catalogcrawler (composing a catalog's
// current content from its baseline plus every change file since) apply
// change files the same way, so the logic lives here once rather than
// being duplicated on both sides of that chain.
package catalogfile

import (
	"encoding/json"
	"fmt"
)

// Doc is the fixed top-level shape a Beckn Catalog carries (file spec:
// "the plain Beckn catalog JSON, exactly the schema used today"). Offers
// is optional -- not every catalog carries one.
type Doc struct {
	ID         json.RawMessage   `json:"id"`
	Descriptor json.RawMessage   `json:"descriptor"`
	Provider   json.RawMessage   `json:"provider"`
	Resources  []json.RawMessage `json:"resources"`
	Offers     []json.RawMessage `json:"offers,omitempty"`
}

// DiffBlock is one array's worth of upserts (added or updated items,
// applied by id) and removals (ids only).
type DiffBlock struct {
	Upserts  []json.RawMessage `json:"upserts,omitempty"`
	Removals []string          `json:"removals,omitempty"`
}

// IsEmpty reports whether this block carries no changes at all.
func (b DiffBlock) IsEmpty() bool { return len(b.Upserts) == 0 && len(b.Removals) == 0 }

// ChangeFileDoc is the change-file shape for one publish (file spec,
// "Catalog files and change files"): resources and offers are diffed
// independently, and Catalog optionally carries catalog-level attribute
// changes (currently: descriptor, provider -- see the "Deliberately not
// done" note in catalogpublisher's README for why this is a best-effort
// subset of that field, not a complete implementation of it).
type ChangeFileDoc struct {
	CatalogID   string          `json:"catalogId"`
	FromVersion int             `json:"fromVersion"`
	ToVersion   int             `json:"toVersion"`
	Resources   DiffBlock       `json:"resources"`
	Offers      DiffBlock       `json:"offers"`
	Catalog     json.RawMessage `json:"catalog,omitempty"`
}

// Apply folds one change file onto catalog's resources/offers arrays
// (upserts replace by id or append; removals drop by id) and overlays any
// catalog-level attribute changes, returning the resulting catalog bytes.
func Apply(catalog []byte, changeRaw []byte) ([]byte, error) {
	var doc Doc
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, fmt.Errorf("catalogfile: parsing catalog: %w", err)
	}
	var change ChangeFileDoc
	if err := json.Unmarshal(changeRaw, &change); err != nil {
		return nil, fmt.Errorf("catalogfile: parsing change file: %w", err)
	}

	resources, err := applyDiffBlock(doc.Resources, change.Resources)
	if err != nil {
		return nil, fmt.Errorf("catalogfile: applying resources: %w", err)
	}
	doc.Resources = resources

	offers, err := applyDiffBlock(doc.Offers, change.Offers)
	if err != nil {
		return nil, fmt.Errorf("catalogfile: applying offers: %w", err)
	}
	doc.Offers = offers

	if len(change.Catalog) > 0 {
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal(change.Catalog, &attrs); err != nil {
			return nil, fmt.Errorf("catalogfile: parsing catalog attribute changes: %w", err)
		}
		if v, ok := attrs["descriptor"]; ok {
			doc.Descriptor = v
		}
		if v, ok := attrs["provider"]; ok {
			doc.Provider = v
		}
	}

	return json.Marshal(doc)
}

// applyDiffBlock applies one DiffBlock (upserts by id, replacing existing
// or appending new; removals by id) to items.
func applyDiffBlock(items []json.RawMessage, block DiffBlock) ([]json.RawMessage, error) {
	removed := make(map[string]bool, len(block.Removals))
	for _, id := range block.Removals {
		removed[id] = true
	}
	upserts := make(map[string]json.RawMessage, len(block.Upserts))
	for _, u := range block.Upserts {
		id, err := ItemID(u)
		if err != nil {
			return nil, err
		}
		upserts[id] = u
	}

	next := make([]json.RawMessage, 0, len(items)+len(block.Upserts))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		id, err := ItemID(item)
		if err != nil {
			return nil, err
		}
		seen[id] = true
		if removed[id] {
			continue
		}
		if u, ok := upserts[id]; ok {
			next = append(next, u)
			continue
		}
		next = append(next, item)
	}
	for _, u := range block.Upserts {
		id, _ := ItemID(u) // already validated above
		if !seen[id] {
			next = append(next, u)
		}
	}
	return next, nil
}

// ItemID extracts the "id" field from a resource/offer item.
func ItemID(raw json.RawMessage) (string, error) {
	var withID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &withID); err != nil {
		return "", fmt.Errorf("catalogfile: parsing item: %w", err)
	}
	if withID.ID == "" {
		return "", fmt.Errorf("catalogfile: item missing id")
	}
	return withID.ID, nil
}
