package store

// statusquery.go — the read path backing the catalogCrawlStatus HTTP
// endpoint: a straight join of a participant's crawler_catalog rows against
// their (optional) in-flight crawler_queue row and their index's last-polled
// timestamp. Unlike every other file in this package, nothing here is ever
// written by the crawl pipeline itself -- this is purely a reporting query
// added for the status endpoint, not part of crawlmanager.Store.

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
	CatalogID     string
	IndexURL      string
	Version       int64
	EntryVersion  int64
	Retired       bool
	Reason        string
	UpdatedAt     time.Time
	Queued        bool
	Attempts      int
	NextAttemptAt time.Time
	IndexPolledAt time.Time
}

// Status returns every crawler_catalog row owned by participantID, or just
// catalogID's if it's non-empty (and actually owned by participantID -- a
// catalogID belonging to someone else yields an empty result, not an error,
// same as one that doesn't exist at all).
func (s *Store) Status(ctx context.Context, participantID, catalogID string) ([]CatalogStatus, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.catalog_id, c.index_url, c.version, c.entry_version, c.status, c.reason, c.updated_at,
		        q.attempts, q.next_attempt_at,
		        i.updated_at
		   FROM crawler_catalog c
		   LEFT JOIN crawler_queue q ON q.catalog_id = c.catalog_id
		   LEFT JOIN crawler_index i ON i.index_url = c.index_url
		  WHERE c.participant_id = $1 AND ($2 = '' OR c.catalog_id = $2)
		  ORDER BY c.catalog_id`,
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
			updatedAt             sql.NullTime
			attempts              sql.NullInt64
			nextAttemptAt         sql.NullTime
			indexPolledAt         sql.NullTime
		)
		if err := rows.Scan(&cs.CatalogID, &indexURL, &cs.Version, &cs.EntryVersion, &status, &rsn, &updatedAt,
			&attempts, &nextAttemptAt, &indexPolledAt); err != nil {
			return nil, fmt.Errorf("store: Status: scanning row: %w", err)
		}
		cs.IndexURL = indexURL.String
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
