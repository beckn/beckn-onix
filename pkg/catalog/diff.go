package catalog

// diff.go — Apply's dual: given two versions of a catalog, compute the
// DiffBlock/attribute-overlay a change file carries between them. Pure
// content logic, no storage or signing concern -- pkg/catalog/publisher
// calls this to build a change file's contents; nothing here knows what a
// publisher or a store is.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// CatalogDiff is the result of comparing two catalog versions' "resources"
// and "offers" arrays by item id.
type CatalogDiff struct {
	Resources DiffBlock
	Offers    DiffBlock
}

// Diff compares prior and next by their top-level "resources" and "offers"
// arrays, matched by each item's "id" field, and separately detects
// catalog-level attribute changes: any top-level field other than "id"
// (identity, never diffed) and "resources"/"offers" (diffed separately
// above) -- not a fixed list, so it covers whatever a Catalog object
// carries beyond those (the file spec's own examples: "name, validity
// window") without having to special-case each one. changeCatalog is nil
// when no catalog-level attributes changed. The result feeds a change
// file's own shape (ChangeFileDoc's Resources/Offers/Catalog) directly.
func Diff(prior, next json.RawMessage) (CatalogDiff, json.RawMessage, error) {
	var priorFields, nextFields map[string]json.RawMessage
	if err := json.Unmarshal(prior, &priorFields); err != nil {
		return CatalogDiff{}, nil, fmt.Errorf("catalog: parsing prior catalog: %w", err)
	}
	if err := json.Unmarshal(next, &nextFields); err != nil {
		return CatalogDiff{}, nil, fmt.Errorf("catalog: parsing submitted catalog: %w", err)
	}

	resourcesDiff, err := diffArrayField(priorFields, nextFields, "resources")
	if err != nil {
		return CatalogDiff{}, nil, fmt.Errorf("catalog: diffing resources: %w", err)
	}
	offersDiff, err := diffArrayField(priorFields, nextFields, "offers")
	if err != nil {
		return CatalogDiff{}, nil, fmt.Errorf("catalog: diffing offers: %w", err)
	}
	changeCatalog := diffCatalogAttributes(priorFields, nextFields)
	return CatalogDiff{Resources: resourcesDiff, Offers: offersDiff}, changeCatalog, nil
}

// diffArrayField diffs priorFields[field] against nextFields[field] (each a
// json.RawMessage array, defaulting to empty when the field is absent),
// matched by item id, merging added+updated into one Upserts list.
func diffArrayField(priorFields, nextFields map[string]json.RawMessage, field string) (DiffBlock, error) {
	priorItems, _, err := itemsByIDOrdered(priorFields, field)
	if err != nil {
		return DiffBlock{}, fmt.Errorf("prior catalog: %w", err)
	}
	nextItems, nextIDs, err := itemsByIDOrdered(nextFields, field)
	if err != nil {
		return DiffBlock{}, fmt.Errorf("submitted catalog: %w", err)
	}

	var block DiffBlock
	for _, id := range nextIDs {
		item := nextItems[id]
		if old, ok := priorItems[id]; !ok || !jsonEqual(old, item) {
			block.Upserts = append(block.Upserts, item)
		}
	}
	for id := range priorItems {
		if _, ok := nextItems[id]; !ok {
			block.Removals = append(block.Removals, id)
		}
	}
	sort.Strings(block.Removals)
	return block, nil
}

// itemsByIDOrdered reads fields[field] as an array of {id, ...} items,
// returning them by id plus the ids in their original array order so diff
// output (Upserts) is deterministic rather than depending on Go's
// randomized map iteration order.
func itemsByIDOrdered(fields map[string]json.RawMessage, field string) (map[string]json.RawMessage, []string, error) {
	raw, ok := fields[field]
	if !ok || len(raw) == 0 {
		return map[string]json.RawMessage{}, nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", field, err)
	}
	m := make(map[string]json.RawMessage, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		id, err := ItemID(item)
		if err != nil {
			return nil, nil, err
		}
		// A later item with the same id overwrites the map entry (last
		// write wins), so ids must not gain a second entry for it too --
		// otherwise diffArrayField would emit the same upsert twice.
		if _, dup := m[id]; !dup {
			ids = append(ids, id)
		}
		m[id] = item
	}
	return m, ids, nil
}

// catalogAttributeFieldsToSkip are handled elsewhere and never belong in
// the change file's "catalog" overlay: "id" is the catalog's identity
// (never diffed), "resources"/"offers" are diffed separately as arrays
// keyed by item id, not as whole-field replacements.
var catalogAttributeFieldsToSkip = map[string]bool{"id": true, "resources": true, "offers": true}

// diffCatalogAttributes returns a non-nil json.RawMessage carrying every
// top-level catalog field (other than id/resources/offers) that changed or
// is new between priorFields and nextFields, or nil if none did.
func diffCatalogAttributes(priorFields, nextFields map[string]json.RawMessage) json.RawMessage {
	changed := map[string]json.RawMessage{}
	for field, nv := range nextFields {
		if catalogAttributeFieldsToSkip[field] {
			continue
		}
		if pv, ok := priorFields[field]; !ok || !jsonEqual(pv, nv) {
			changed[field] = nv
		}
	}
	if len(changed) == 0 {
		return nil
	}
	// changed only holds json.RawMessage values already produced by a
	// successful Unmarshal above, so marshaling map[string]json.RawMessage
	// back out cannot fail.
	raw, _ := json.Marshal(changed)
	return raw
}

// jsonEqual compares two JSON values semantically (decoded structure, not
// raw bytes) so whitespace/key-order differences don't register as an
// update.
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}
