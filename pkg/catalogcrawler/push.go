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

// BatchCatalog splits a resolved catalog into push batches by resource
// count. A catalog that fits in batchSize is one FULL batch (replace). A
// larger catalog is the first batch FULL (descriptor/provider/offers +
// the first resource slice) then the rest MERGE (id + resource slice),
// which must be applied in order (see the ordering caveat in the design).
func BatchCatalog(catalog []byte, batchSize int) ([]CatalogBatch, error) {
	var doc catalogfile.Doc
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, fmt.Errorf("catalogcrawler: reading catalog for batching: %w", err)
	}
	if batchSize <= 0 || len(doc.Resources) <= batchSize {
		return []CatalogBatch{{Doc: catalog, UpdateMode: UpdateModeFull}}, nil
	}

	var batches []CatalogBatch
	for start := 0; start < len(doc.Resources); start += batchSize {
		end := start + batchSize
		if end > len(doc.Resources) {
			end = len(doc.Resources)
		}
		chunk := doc.Resources[start:end]

		if start == 0 {
			first := doc
			first.Resources = chunk
			b, err := json.Marshal(first)
			if err != nil {
				return nil, err
			}
			batches = append(batches, CatalogBatch{Doc: b, UpdateMode: UpdateModeFull})
			continue
		}
		b, err := json.Marshal(struct {
			ID        json.RawMessage   `json:"id"`
			Resources []json.RawMessage `json:"resources"`
		}{ID: doc.ID, Resources: chunk})
		if err != nil {
			return nil, err
		}
		batches = append(batches, CatalogBatch{Doc: b, UpdateMode: UpdateModeMerge})
	}
	return batches, nil
}
