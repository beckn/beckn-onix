// Package catalog is the decentralized-catalog file spec's shared document
// model: the index schema (index.go -- which files exist for a catalog, at
// which versions, with which digests/signatures) and the change-file
// application rule (this file -- a change file carries what changed between
// two consecutive versions, keyed by id never by position, as upserts
// (added or updated items, replaced by id) and removals (ids only)).
// Anything that needs to fold a change file onto a baseline to reconstruct
// a catalog's current content -- a storage backend replaying prior state,
// a crawler composing a catalog from its baseline plus every change since
// -- applies change files the same way, so the logic lives here once rather
// than being duplicated at each call site.
//
// fetch.go layers a fetch-verify-decode caller on top of pkg/crawler's
// content-agnostic primitives, understanding these document shapes so
// pkg/crawler itself doesn't have to.
package catalog

import (
	"encoding/json"
	"fmt"
)

// DiffBlock is one array's worth of upserts (added or updated items,
// applied by id) and removals (ids only).
type DiffBlock struct {
	Upserts  []json.RawMessage `json:"upserts,omitempty"`
	Removals []string          `json:"removals,omitempty"`
}

// IsEmpty reports whether this block carries no changes at all.
func (b DiffBlock) IsEmpty() bool { return len(b.Upserts) == 0 && len(b.Removals) == 0 }

// ChangeFileDoc is the change-file shape for one publish: resources and
// offers are diffed independently, and Catalog optionally carries
// catalog-level attribute changes (e.g. name, validity window) -- any
// top-level catalog field other than resources/offers, not a fixed list.
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
//
// catalog is parsed as a generic field map, not a fixed struct: a Beckn
// Catalog object can carry fields beyond id/descriptor/provider/resources/
// offers (e.g. a catalog-wide validity window), and those must survive a
// baseline+changes round trip unchanged even when nothing here touches
// them.
func Apply(catalog []byte, changeRaw []byte) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, fmt.Errorf("catalogfile: parsing catalog: %w", err)
	}
	var change ChangeFileDoc
	if err := json.Unmarshal(changeRaw, &change); err != nil {
		return nil, fmt.Errorf("catalogfile: parsing change file: %w", err)
	}

	resources, err := arrayField(doc, "resources")
	if err != nil {
		return nil, fmt.Errorf("catalogfile: parsing resources: %w", err)
	}
	resources, err = applyDiffBlock(resources, change.Resources)
	if err != nil {
		return nil, fmt.Errorf("catalogfile: applying resources: %w", err)
	}
	if doc["resources"], err = json.Marshal(resources); err != nil {
		return nil, fmt.Errorf("catalogfile: marshaling resources: %w", err)
	}

	// offers is optional -- only rewrite it if the catalog already has one
	// or this change actually touches it, so a catalog with no offers
	// field doesn't gain an empty "offers": [] out of nowhere.
	if _, hasOffers := doc["offers"]; hasOffers || !change.Offers.IsEmpty() {
		offers, err := arrayField(doc, "offers")
		if err != nil {
			return nil, fmt.Errorf("catalogfile: parsing offers: %w", err)
		}
		offers, err = applyDiffBlock(offers, change.Offers)
		if err != nil {
			return nil, fmt.Errorf("catalogfile: applying offers: %w", err)
		}
		if doc["offers"], err = json.Marshal(offers); err != nil {
			return nil, fmt.Errorf("catalogfile: marshaling offers: %w", err)
		}
	}

	if len(change.Catalog) > 0 {
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal(change.Catalog, &attrs); err != nil {
			return nil, fmt.Errorf("catalogfile: parsing catalog attribute changes: %w", err)
		}
		for field, v := range attrs {
			doc[field] = v
		}
	}

	return json.Marshal(doc)
}

// arrayField reads doc[field] as a json array, or nil if absent.
func arrayField(doc map[string]json.RawMessage, field string) ([]json.RawMessage, error) {
	raw, ok := doc[field]
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
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
			seen[id] = true
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
