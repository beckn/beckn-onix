package catalogcrawler

import (
	"encoding/json"
	"fmt"

	"github.com/beckn-one/beckn-onix/pkg/catalogfile"
)

// Discovery /push update modes (beckn-discovr publishDirectives.updateMode).
const (
	UpdateModeFull  = "FULL"  // replace: resources absent from the pushed doc are deleted
	UpdateModeMerge = "MERGE" // id-keyed upserts + removals
)

// PushMeta carries everything that varies per push call. IDs and timestamp
// are injected so the builder stays pure and testable; the engine supplies
// real values.
type PushMeta struct {
	ParticipantID string   // publisher identity (a domain) -> context.bppId
	BppURI        string   // publisher URI -> context.bppUri
	MessageID     string   // per-call uuid
	TransactionID string   // per-call uuid
	Timestamp     string   // RFC3339
	UpdateMode    string   // UpdateModeFull | UpdateModeMerge
	VisibleTo     []string // catalog networks; nil/empty => public (omitted)
}

// BuildPushBody builds the Discovery /push request body: a Beckn
// catalog/publish context plus message.catalogs and a matching
// message.publishDirectives entry carrying updateMode and visibleTo.
func BuildPushBody(meta PushMeta, catalog []byte) ([]byte, error) {
	var head struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(catalog, &head); err != nil {
		return nil, fmt.Errorf("catalogcrawler: reading catalog id: %w", err)
	}

	directive := map[string]any{
		"catalogId":  head.ID,
		"updateMode": meta.UpdateMode,
	}
	if len(meta.VisibleTo) > 0 {
		directive["visibleTo"] = meta.VisibleTo
	}

	body := map[string]any{
		"context": map[string]any{
			"action":        "catalog/publish",
			"bppId":         meta.ParticipantID,
			"bppUri":        meta.BppURI,
			"messageId":     meta.MessageID,
			"transactionId": meta.TransactionID,
			"timestamp":     meta.Timestamp,
			"version":       "2.0.0",
		},
		"message": map[string]any{
			"catalogs":          []json.RawMessage{json.RawMessage(catalog)},
			"publishDirectives": []any{directive},
		},
	}
	return json.Marshal(body)
}

// CatalogBatch is one push of a slice of a catalog's resources with the
// update mode Discovery should apply.
type CatalogBatch struct {
	Doc        []byte
	UpdateMode string
}

// BatchCatalog splits a catalog into push batches by serialized BYTE size, so
// no batch's doc exceeds maxDocBytes (and thus no /push body exceeds Discovery's
// payload cap once the small envelope is added). A catalog that already fits is
// one batch with baseMode. A larger one is a lead batch (baseMode, carrying the
// offers) then the rest MERGE (append, no offers) — a FULL lead replaces once
// and the MERGE batches fill the rest, so re-push stays idempotent. A single
// resource larger than the budget unavoidably forms its own over-budget batch
// (it can't be split further); callers surface the resulting reject.
func BatchCatalog(catalog []byte, maxDocBytes int64, baseMode string) ([]CatalogBatch, error) {
	var doc catalogfile.Doc
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, fmt.Errorf("catalogcrawler: reading catalog for batching: %w", err)
	}
	// Fast path: the whole doc already fits (or no budget set).
	if maxDocBytes <= 0 || int64(len(catalog)) <= maxDocBytes {
		return []CatalogBatch{{Doc: catalog, UpdateMode: baseMode}}, nil
	}

	resources := doc.Resources
	var batches []CatalogBatch
	i, first := 0, true
	for i < len(resources) {
		// Scaffolding (everything but the resource slice) sets the per-batch base
		// cost. Only the lead batch carries offers; the rest drop them.
		base := doc
		base.Resources = nil
		if !first {
			base.Offers = nil
		}
		baseBytes, err := json.Marshal(base)
		if err != nil {
			return nil, err
		}
		budget := maxDocBytes - int64(len(baseBytes))

		start := i
		var used int64
		for i < len(resources) {
			rb, err := json.Marshal(resources[i])
			if err != nil {
				return nil, err
			}
			cost := int64(len(rb)) + 1 // + separator
			if i > start && used+cost > budget {
				break // spill to the next batch (always keep ≥1 resource per batch)
			}
			used += cost
			i++
		}

		slice := doc
		slice.Resources = resources[start:i]
		mode := UpdateModeMerge
		if first {
			mode = baseMode
		} else {
			slice.Offers = nil
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
