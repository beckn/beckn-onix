// Package sink implements crawlmanager.Sink: an HTTP push to a Discovery
// service. Ported from the catalog-crawler prototype's own publish package
// (request.go/batch.go/client.go), adapted to crawlmanager's simpler
// Send(ctx, entry, content) contract -- in particular, UpdateMode is always
// FULL here: crawlmanager always resolves a catalog's complete current
// content (via catalog.Resolve) rather than tracking an incremental
// Changeset the way the prototype's runner did, so there is no MERGE-vs-FULL
// decision left to make.
package sink

import (
	"encoding/json"
	"fmt"
)

// Discovery /push update modes (beckn-discovr publishDirectives.updateMode).
const (
	UpdateModeFull  = "FULL"  // replace: resources absent from the pushed doc are deleted
	UpdateModeMerge = "MERGE" // id-keyed upserts + removals
)

// PushMeta carries everything that varies per push call. IDs and timestamp
// are injected so the builder stays pure and testable.
type PushMeta struct {
	ParticipantID string   // publisher identity (a domain) -> context.bppId
	BppURI        string   // publisher URI -> context.bppUri
	MessageID     string   // per-call uuid
	TransactionID string   // per-call uuid
	Timestamp     string   // RFC3339
	UpdateMode    string   // UpdateModeFull | UpdateModeMerge
	CatalogType   string   // from the index entry -> publishDirective.catalogType (required)
	VisibleTo     []string // catalog networks; nil/empty => public (omitted)
	// SchemaContext mirrors the index entry's own schemaTypes into
	// context.schemaContext -- an array of JSON-LD context URLs. Discovery's
	// own schema-type resolution checks this FIRST, before falling back to
	// each resource's own resourceAttributes["@type"]. Omitted when empty.
	SchemaContext []string
}

// BuildPushBody builds the Discovery /push request body: a Beckn
// catalog/push context plus a CatalogPublishAction message
// (message.catalogs, min 1) and a matching message.publishDirectives entry
// carrying catalogType, updateMode, and visibleTo.
func BuildPushBody(meta PushMeta, catalog []byte) ([]byte, error) {
	var head struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(catalog, &head); err != nil {
		return nil, fmt.Errorf("catalogcrawler: reading catalog id: %w", err)
	}

	directive := map[string]any{
		"catalogId":   head.ID,
		"catalogType": meta.CatalogType,
		"updateMode":  meta.UpdateMode,
	}
	if len(meta.VisibleTo) > 0 {
		directive["visibleTo"] = meta.VisibleTo
	}

	context := map[string]any{
		"action":        "catalog/push",
		"bppId":         meta.ParticipantID,
		"bppUri":        meta.BppURI,
		"messageId":     meta.MessageID,
		"transactionId": meta.TransactionID,
		"timestamp":     meta.Timestamp,
		"version":       "2.0.0",
	}
	if len(meta.SchemaContext) > 0 {
		context["schemaContext"] = meta.SchemaContext
	}

	body := map[string]any{
		"context": context,
		"message": map[string]any{
			"catalogs":          []json.RawMessage{json.RawMessage(catalog)},
			"publishDirectives": []any{directive},
		},
	}
	return json.Marshal(body)
}
