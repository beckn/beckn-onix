package sink

// batch.go — splits a catalog into /push batches that each fit Discovery's
// payload cap (a lead batch carrying offers, remaining resources appended
// without offers), so a large catalog can be pushed across several
// requests. Operates on a generic map[string]json.RawMessage rather than a
// typed catalog document struct, so it needs no catalog-shape knowledge
// beyond the "resources"/"offers" fields it splits on -- everything else in
// the document round-trips through the map unchanged.

import (
	"encoding/json"
	"fmt"
)

// CatalogBatch is one push of a slice of a catalog's resources with the
// update mode Discovery should apply.
type CatalogBatch struct {
	Doc        []byte
	UpdateMode string
}

// BatchCatalog splits catalog into push batches by serialized BYTE size, so
// no batch's doc exceeds maxDocBytes. A catalog that already fits is one
// batch with baseMode. A larger one is a lead batch (baseMode, carrying the
// offers) then the rest MERGE (append, no offers) -- a FULL lead replaces
// once and the MERGE batches fill the rest, so re-push stays idempotent. A
// single resource larger than the budget unavoidably forms its own
// over-budget batch (it can't be split further); callers surface the
// resulting reject.
func BatchCatalog(catalog []byte, maxDocBytes int64, baseMode string) ([]CatalogBatch, error) {
	if maxDocBytes <= 0 || int64(len(catalog)) <= maxDocBytes {
		return []CatalogBatch{{Doc: catalog, UpdateMode: baseMode}}, nil
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, fmt.Errorf("catalogcrawler: reading catalog for batching: %w", err)
	}
	resources, err := rawArray(doc, "resources")
	if err != nil {
		return nil, err
	}
	offers := doc["offers"]

	var batches []CatalogBatch
	i, first := 0, true
	for i < len(resources) {
		base := cloneWithout(doc, "resources")
		if !first {
			delete(base, "offers")
		}
		baseBytes, err := json.Marshal(base)
		if err != nil {
			return nil, err
		}
		budget := maxDocBytes - int64(len(baseBytes))

		start := i
		var used int64
		for i < len(resources) {
			cost := int64(len(resources[i])) + 1 // + separator
			if i > start && used+cost > budget {
				break // spill to the next batch (always keep >=1 resource per batch)
			}
			used += cost
			i++
		}

		slice := cloneWithout(doc, "resources")
		resBytes, err := json.Marshal(resources[start:i])
		if err != nil {
			return nil, err
		}
		slice["resources"] = resBytes
		mode := UpdateModeMerge
		if first {
			mode = baseMode
			if len(offers) > 0 {
				slice["offers"] = offers
			}
		} else {
			delete(slice, "offers")
		}
		b, err := json.Marshal(slice)
		if err != nil {
			return nil, err
		}
		batches = append(batches, CatalogBatch{Doc: b, UpdateMode: mode})
		first = false
	}
	// A resource-less catalog (offers-only / empty) still needs one batch.
	if len(batches) == 0 {
		return []CatalogBatch{{Doc: catalog, UpdateMode: baseMode}}, nil
	}
	return batches, nil
}

// rawArray reads doc[field] as a json array, or nil if absent.
func rawArray(doc map[string]json.RawMessage, field string) ([]json.RawMessage, error) {
	raw, ok := doc[field]
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("catalogcrawler: reading %s for batching: %w", field, err)
	}
	return items, nil
}

// cloneWithout returns a shallow copy of doc with field removed -- the
// per-batch scaffolding (everything but the field being split).
func cloneWithout(doc map[string]json.RawMessage, field string) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(doc))
	for k, v := range doc {
		if k != field {
			out[k] = v
		}
	}
	return out
}

// DocCounts reports how many resources and offers a pushed catalog doc
// carries (best-effort -- a doc that won't parse counts as zero).
func DocCounts(b []byte) (resources, offers int) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(b, &doc) != nil {
		return 0, 0
	}
	r, _ := rawArray(doc, "resources")
	o, _ := rawArray(doc, "offers")
	return len(r), len(o)
}
