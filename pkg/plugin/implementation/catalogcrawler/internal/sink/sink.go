package sink

// sink.go — DiscoverySink: crawlmanager.Sink backed by an HTTP push to a
// Discovery service. Batches the resolved catalog if it exceeds MaxDocBytes,
// pushes each batch, and rolls the outcomes up into one SinkOutcome.

import (
	"context"
	"fmt"
	"time"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
	"github.com/google/uuid"
)

// DiscoverySink pushes a resolved catalog's current content to a Discovery
// endpoint as one or more FULL-mode /push requests.
//
// UpdateMode is always FULL: unlike the catalog-crawler prototype's runner,
// crawlmanager never tracks an incremental Changeset (upserts/removals since
// a cursor) -- catalog.Resolve always folds a catalog's COMPLETE current
// content, so a FULL replace is the only mode that matches what SyncNext
// actually resolved. A batch after the first still omits offers (Discovery's
// existing MERGE semantics for the spillover batches of one push), even
// though it's still conceptually "the same full push" split across requests.
type DiscoverySink struct {
	Endpoint      string // Discovery's /push URL
	ParticipantID string // this deployment's bppId
	BppURI        string // this deployment's bppUri
	MaxDocBytes   int64  // 0 => no batching
	Client        *Client
	Now           func() time.Time // nil => time.Now
}

// NewDiscoverySink builds a DiscoverySink. timeout bounds each batch's push.
func NewDiscoverySink(endpoint, participantID, bppURI string, maxDocBytes int64, timeout time.Duration) *DiscoverySink {
	return &DiscoverySink{Endpoint: endpoint, ParticipantID: participantID, BppURI: bppURI, MaxDocBytes: maxDocBytes, Client: NewClient(timeout)}
}

func (d *DiscoverySink) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Send implements crawlmanager.Sink.
func (d *DiscoverySink) Send(ctx context.Context, entry catalog.CatalogEntry, content []byte) (crawlmanager.SinkOutcome, error) {
	batches, err := BatchCatalog(content, d.MaxDocBytes, UpdateModeFull)
	if err != nil {
		return crawlmanager.SinkOutcome{}, fmt.Errorf("catalogcrawler: batching %s: %w", entry.CatalogID, err)
	}

	var outcomes []BatchOutcome
	for _, batch := range batches {
		meta := PushMeta{
			ParticipantID: d.ParticipantID,
			BppURI:        d.BppURI,
			MessageID:     uuid.NewString(),
			TransactionID: uuid.NewString(),
			Timestamp:     d.now().UTC().Format(time.RFC3339),
			UpdateMode:    batch.UpdateMode,
			CatalogType:   entry.CatalogType,
			VisibleTo:     entry.NetworkIDs,
			SchemaContext: entry.SchemaTypes,
		}
		body, err := BuildPushBody(meta, batch.Doc)
		if err != nil {
			return crawlmanager.SinkOutcome{}, fmt.Errorf("catalogcrawler: building push body for %s: %w", entry.CatalogID, err)
		}
		outcome, err := d.Client.Push(ctx, d.Endpoint, body)
		if err != nil {
			return crawlmanager.SinkOutcome{}, fmt.Errorf("catalogcrawler: pushing %s: %w", entry.CatalogID, err)
		}
		outcomes = append(outcomes, outcome)
	}

	accepted, reason := Rollup(outcomes)
	return crawlmanager.SinkOutcome{Accepted: accepted, Reason: reason}, nil
}
