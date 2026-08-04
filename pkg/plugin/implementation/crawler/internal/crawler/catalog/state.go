package catalog

// state.go — the persisted state vocabulary of a Catalog Sync: the queue item a
// worker claims, the per-pass report, and the settled per-catalog / per-index
// rows. These live in `catalog` (the bottom of the import graph, §6b) rather
// than in a storage package, so the Store port can be declared purely in domain
// terms and any backend can satisfy it without a driver-shaped type leaking
// into the runner's signatures.

import "time"

// PassReport is one settled pass's detailed outcome, appended to a catalog's
// push_status history array. Counts are what this pass actually pushed; on a
// partial/faulted push, BatchesAcked < BatchesTotal tells the story. Outcome/Mode
// carry the Catalog Sync's wire values (see SyncOutcome).
type PassReport struct {
	At           time.Time `json:"ts"`
	FromVersion  int64     `json:"from"`
	ToVersion    int64     `json:"to"`
	Mode         string    `json:"mode,omitempty"` // FULL | MERGE ("" for retire/skip)
	Resources    int       `json:"resources"`      // resources pushed this pass
	Offers       int       `json:"offers"`         // offers pushed this pass
	Removals     int       `json:"removals"`       // resources+offers removed this pass
	BatchesAcked int       `json:"batchesAcked"`
	BatchesTotal int       `json:"batchesTotal"`
	Outcome      string    `json:"outcome"` // pushed | partial | skipped | dropped | retired | faulted
	HTTPStatus   int       `json:"httpStatus,omitempty"`
	Reason       string    `json:"reason,omitempty"`
}

// CatalogState is the settled per-catalog outcome to persist. Report is
// appended to the push_status history array; Reason/HTTPStatus mirror the
// latest pass for cheap top-level queries.
type CatalogState struct {
	CatalogID     string
	IndexURL      string
	ParticipantID string
	Version       int64
	Status        string // active | retired (CatalogStatus wire value)
	Report        PassReport
}

// IndexState is the stored state for one index (the change gate + cadence).
// ETag / LastModified are the last conditional-GET validators the host gave us
// (empty if it sends none) — echoed back to try for a 304 next time.
type IndexState struct {
	IndexVersion int64
	SyncStatus   string
	NextCrawlAt  time.Time
	ETag         string
	LastModified string
}

// KnownIndex is a persisted index the crawler has crawled at least once — the
// unit the scheduled pass re-polls so an on-demand /crawl joins the schedule.
type KnownIndex struct {
	IndexURL      string
	ParticipantID string
	Source        string
}

// QueueItem is a unit of work the index job enqueues: sync a catalog to a
// target version (the catalog job reads the index for the actual files).
type QueueItem struct {
	CatalogID   string
	IndexURL    string
	FromVersion int64 // 0 => baseline / new
	ToVersion   int64
	Op          string // "sync" | "retire" (defaults to "sync")
}

// ClaimedItem is a queue row a worker has claimed for processing. ClaimID is
// the token that authorises this worker to settle the row.
type ClaimedItem struct {
	ID          string
	ClaimID     string
	CatalogID   string
	IndexURL    string
	FromVersion int64
	ToVersion   int64
	Op          string
	Attempts    int
	EnqueuedAt  time.Time
}
