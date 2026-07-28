package catalogcrawler

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
