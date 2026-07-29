package store

// indexstate.go — per-index state: the change gate + crawl cadence, the
// conditional-GET validators (ETag / Last-Modified), and the 304-Not-Modified
// path (AdvanceIndexCadence) that bumps only the cadence.

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

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

// GetIndex returns the stored state for an index, or nil if never crawled.
func (s *Store) GetIndex(ctx context.Context, indexURL string) (*IndexState, error) {
	var (
		st   IndexState
		v    sql.NullInt64
		ss   sql.NullString
		nca  sql.NullTime
		etag sql.NullString
		lm   sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT index_version, sync_status, next_crawl_at, etag, last_modified FROM crawler_index WHERE index_url=$1`, indexURL).
		Scan(&v, &ss, &nca, &etag, &lm)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: GetIndex: %w", err)
	}
	st.IndexVersion, st.SyncStatus, st.NextCrawlAt = v.Int64, ss.String, nca.Time
	st.ETag, st.LastModified = etag.String, lm.String
	return &st, nil
}

// KnownIndex is a persisted index the crawler has crawled at least once — the
// unit the scheduled pass re-polls so an on-demand /crawl joins the schedule.
type KnownIndex struct {
	IndexURL      string
	ParticipantID string
	Source        string
}

// KnownIndexes lists every index recorded in crawler_index (any source),
// ordered by URL for determinism. The scheduled pass unions these with the
// configured/registry refs, so an index crawled once (incl. on-demand) keeps
// being re-polled on the normal cadence.
func (s *Store) KnownIndexes(ctx context.Context) ([]KnownIndex, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT index_url, participant_id, source FROM crawler_index ORDER BY index_url`)
	if err != nil {
		return nil, fmt.Errorf("store: KnownIndexes: %w", err)
	}
	defer rows.Close()
	var out []KnownIndex
	for rows.Next() {
		var (
			url string
			pid sql.NullString
			src sql.NullString
		)
		if err := rows.Scan(&url, &pid, &src); err != nil {
			return nil, fmt.Errorf("store: KnownIndexes scan: %w", err)
		}
		out = append(out, KnownIndex{IndexURL: url, ParticipantID: pid.String, Source: src.String})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: KnownIndexes rows: %w", err)
	}
	return out, nil
}

// UpsertIndex records an index's last-seen version + sync outcome, plus the
// conditional-GET validators (etag / lastModified) the host returned this fetch
// (empty string when it sent none).
func (s *Store) UpsertIndex(ctx context.Context, indexURL, participantID, source string, version int64, syncStatus string, nextCrawlAt time.Time, etag, lastModified string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO crawler_index
		   (index_url, participant_id, source, index_version, sync_status, last_crawled_at, next_crawl_at, etag, last_modified, updated_at)
		 VALUES ($1,$2,$3,$4,$5, now(), $6, $7, $8, now())
		 ON CONFLICT (index_url) DO UPDATE SET
		   participant_id = EXCLUDED.participant_id,
		   source         = EXCLUDED.source,
		   index_version  = EXCLUDED.index_version,
		   sync_status    = EXCLUDED.sync_status,
		   last_crawled_at = now(),
		   next_crawl_at  = EXCLUDED.next_crawl_at,
		   etag           = EXCLUDED.etag,
		   last_modified  = EXCLUDED.last_modified,
		   updated_at     = now()`,
		indexURL, nullStr(participantID), nullStr(source), version, nullStr(syncStatus), nullTime(nextCrawlAt), nullStr(etag), nullStr(lastModified))
	if err != nil {
		return fmt.Errorf("store: UpsertIndex: %w", err)
	}
	return nil
}

// AdvanceIndexCadence is the 304-Not-Modified path: the index is unchanged, so
// only the crawl cadence advances — version, participant, and validators are
// left as they were (no re-parse, no re-download).
func (s *Store) AdvanceIndexCadence(ctx context.Context, indexURL string, nextCrawlAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE crawler_index
		    SET last_crawled_at = now(), next_crawl_at = $2, updated_at = now()
		  WHERE index_url = $1`,
		indexURL, nullTime(nextCrawlAt))
	if err != nil {
		return fmt.Errorf("store: AdvanceIndexCadence: %w", err)
	}
	return nil
}
