package store

// indexstate.go — per-index conditional-GET validators (ETag / Last-Modified),
// crawlmanager's whole notion of index state. Unlike the prototype this
// schema came from, crawlmanager has no per-index crawl cadence of its own
// (PollIndexes polls every discovered index each tick) -- next_crawl_at,
// sync_status, and index_version are left unused rather than repurposed.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
)

// GetIndexCursor returns the stored cursor for an index, or nil if never
// polled.
func (s *Store) GetIndexCursor(ctx context.Context, indexURL string) (*crawlmanager.IndexCursor, error) {
	var (
		pid  sql.NullString
		etag sql.NullString
		lm   sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT participant_id, etag, last_modified FROM crawler_index WHERE index_url=$1`, indexURL).
		Scan(&pid, &etag, &lm)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: GetIndexCursor: %w", err)
	}
	return &crawlmanager.IndexCursor{IndexURL: indexURL, ParticipantID: pid.String, ETag: etag.String, LastModified: lm.String}, nil
}

// UpsertIndexCursor records an index's conditional-GET validators from its
// latest fetch (empty string when the host sent none).
func (s *Store) UpsertIndexCursor(ctx context.Context, cursor crawlmanager.IndexCursor) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO crawler_index (index_url, participant_id, etag, last_modified, updated_at)
		 VALUES ($1,$2,$3,$4, now())
		 ON CONFLICT (index_url) DO UPDATE SET
		   participant_id = EXCLUDED.participant_id,
		   etag           = EXCLUDED.etag,
		   last_modified  = EXCLUDED.last_modified,
		   updated_at     = now()`,
		cursor.IndexURL, nullStr(cursor.ParticipantID), nullStr(cursor.ETag), nullStr(cursor.LastModified))
	if err != nil {
		return fmt.Errorf("store: UpsertIndexCursor: %w", err)
	}
	return nil
}
