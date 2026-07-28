package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// QueueItem is a unit of work the index job enqueues: sync a catalog to a
// target version (the catalog job reads the index for the actual files).
type QueueItem struct {
	CatalogID   string
	IndexURL    string
	FromVersion int64 // 0 => baseline / new
	ToVersion   int64
	Op          string // "sync" | "retire" (defaults to "sync")
}

// ClaimedItem is a queue row a worker has claimed for processing.
type ClaimedItem struct {
	ID          string
	CatalogID   string
	IndexURL    string
	FromVersion int64
	ToVersion   int64
	Op          string
	Attempts    int
}

// Enqueue upserts a work item. UNIQUE(catalog_id) coalesces repeated
// changes into one pending item (latest to_version wins) and resets the
// retry state so fresh content is attempted immediately.
func (s *Store) Enqueue(ctx context.Context, item QueueItem) error {
	op := item.Op
	if op == "" {
		op = "sync"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO crawler_queue
		   (catalog_id, index_url, from_version, to_version, op, status, attempts, next_attempt_at, claimed_at, enqueued_at)
		 VALUES ($1,$2,$3,$4,$5,'queued',0, now(), NULL, now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url       = EXCLUDED.index_url,
		   from_version    = EXCLUDED.from_version,
		   to_version      = EXCLUDED.to_version,
		   op              = EXCLUDED.op,
		   status          = 'queued',
		   attempts        = 0,
		   next_attempt_at = now(),
		   claimed_at      = NULL`,
		item.CatalogID, item.IndexURL, nullInt64Zero(item.FromVersion), item.ToVersion, op)
	if err != nil {
		return fmt.Errorf("state: Enqueue: %w", err)
	}
	return nil
}

// ClaimNext atomically claims the next ready item (unclaimed, past its
// backoff) using FOR UPDATE SKIP LOCKED. Returns nil when nothing is ready.
func (s *Store) ClaimNext(ctx context.Context) (*ClaimedItem, error) {
	var (
		it ClaimedItem
		fv sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`UPDATE crawler_queue SET claimed_at = now(), status = 'in_progress'
		 WHERE id = (
		   SELECT id FROM crawler_queue
		   WHERE claimed_at IS NULL AND next_attempt_at <= now()
		   ORDER BY next_attempt_at
		   FOR UPDATE SKIP LOCKED
		   LIMIT 1)
		 RETURNING id, catalog_id, index_url, from_version, to_version, op, attempts`).
		Scan(&it.ID, &it.CatalogID, &it.IndexURL, &fv, &it.ToVersion, &it.Op, &it.Attempts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: ClaimNext: %w", err)
	}
	it.FromVersion = fv.Int64
	return &it, nil
}

// FailQueueItem releases a claim, bumps attempts, and gates the next
// attempt behind nextAttemptAt (the backoff).
func (s *Store) FailQueueItem(ctx context.Context, id string, nextAttemptAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE crawler_queue
		   SET attempts = attempts + 1, next_attempt_at = $2, claimed_at = NULL, status = 'queued'
		 WHERE id = $1`,
		id, nextAttemptAt)
	if err != nil {
		return fmt.Errorf("state: FailQueueItem: %w", err)
	}
	return nil
}

// Complete atomically records the catalog's settled state and removes the
// queue row (the "purged on success" step).
func (s *Store) Complete(ctx context.Context, id string, c CatalogState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: Complete begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort rollback if commit not reached
	if err := upsertCatalog(ctx, tx, c); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM crawler_queue WHERE id = $1`, id); err != nil {
		return fmt.Errorf("state: Complete delete: %w", err)
	}
	return tx.Commit()
}

// QueueDepth returns the number of pending work items (backlog gauge).
func (s *Store) QueueDepth(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM crawler_queue`).Scan(&n); err != nil {
		return 0, fmt.Errorf("state: QueueDepth: %w", err)
	}
	return n, nil
}

func nullInt64Zero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
