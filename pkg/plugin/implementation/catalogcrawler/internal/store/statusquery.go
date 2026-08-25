package store

// statusquery.go — the read path backing the catalogCrawlStatus HTTP
// endpoint: a participant's crawler_catalog rows FULL OUTER JOINed against
// their (optional) in-flight crawler_queue row, plus their index's
// last-polled timestamp. The FULL OUTER JOIN (not a LEFT JOIN from
// crawler_catalog) matters: crawler_catalog is only ever written on a
// settled sync (upsertCatalog, via Complete), so a catalog queued for its
// very first sync has no crawler_catalog row yet at all -- a LEFT JOIN
// starting from crawler_catalog would silently omit it from every result
// instead of reporting it as queued. Ownership (participantID) falls back
// to crawler_index.participant_id (set by UpsertIndexCursor, independent
// of whether this catalog has ever settled) for exactly that case; when a
// crawler_catalog row does exist, its own participant_id is authoritative.
// Unlike every other file in this package, nothing here is ever written by
// the crawl pipeline itself -- this is purely a reporting query added for
// the status endpoint, not part of crawlmanager.Store.

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CatalogStatus is one catalog's row from the Status query -- see
// definition.CrawlStatus (catalogcrawler.go's Status method) for field
// semantics; this is that same shape before conversion, kept in this
// package only to avoid an internal/store -> pkg/plugin/definition import.
type CatalogStatus struct {
	CatalogID string
	IndexURL  string

	// EverSynced is false for a catalog queued for its first-ever sync --
	// Version/EntryVersion/Retired/Reason/UpdatedAt are all zero-valued in
	// that case, since crawler_catalog has no row for it yet.
	EverSynced   bool
	Version      int64
	EntryVersion int64
	Retired      bool
	Reason       string
	UpdatedAt    time.Time

	Queued        bool
	Attempts      int
	NextAttemptAt time.Time
	IndexPolledAt time.Time
}

// Status returns every catalog owned by participantID -- whether settled
// (crawler_catalog), only queued (crawler_queue, never yet settled), or
// both -- or just catalogID's if it's non-empty (and actually owned by
// participantID -- a catalogID belonging to someone else yields an empty
// result, not an error, same as one that doesn't exist at all).
func (s *Store) Status(ctx context.Context, participantID, catalogID string) ([]CatalogStatus, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(c.catalog_id, q.catalog_id), COALESCE(c.index_url, q.index_url),
		        c.version, c.entry_version, c.status, c.reason, c.updated_at,
		        q.attempts, q.next_attempt_at,
		        i.updated_at
		   FROM crawler_catalog c
		   FULL OUTER JOIN crawler_queue q ON q.catalog_id = c.catalog_id
		   LEFT JOIN crawler_index i ON i.index_url = COALESCE(c.index_url, q.index_url)
		  WHERE COALESCE(c.participant_id, i.participant_id) = $1
		    AND ($2 = '' OR COALESCE(c.catalog_id, q.catalog_id) = $2)
		  ORDER BY COALESCE(c.catalog_id, q.catalog_id)`,
		participantID, catalogID)
	if err != nil {
		return nil, fmt.Errorf("store: Status: %w", err)
	}
	defer rows.Close()

	var out []CatalogStatus
	for rows.Next() {
		var (
			cs                    CatalogStatus
			indexURL, status, rsn sql.NullString
			version, entryVersion sql.NullInt64
			updatedAt             sql.NullTime
			attempts              sql.NullInt64
			nextAttemptAt         sql.NullTime
			indexPolledAt         sql.NullTime
		)
		if err := rows.Scan(&cs.CatalogID, &indexURL, &version, &entryVersion, &status, &rsn, &updatedAt,
			&attempts, &nextAttemptAt, &indexPolledAt); err != nil {
			return nil, fmt.Errorf("store: Status: scanning row: %w", err)
		}
		cs.IndexURL = indexURL.String
		cs.EverSynced = version.Valid
		cs.Version = version.Int64
		cs.EntryVersion = entryVersion.Int64
		cs.Retired = status.String == "retired"
		cs.Reason = rsn.String
		cs.UpdatedAt = updatedAt.Time
		cs.Queued = attempts.Valid
		cs.Attempts = int(attempts.Int64)
		cs.NextAttemptAt = nextAttemptAt.Time
		cs.IndexPolledAt = indexPolledAt.Time
		out = append(out, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: Status: %w", err)
	}
	return out, nil
}
