package store

// queue.go — the work queue: coalescing Enqueue (UNIQUE catalog_id, latest
// to_version wins), atomic ClaimNext (FOR UPDATE SKIP LOCKED) with a per-claim
// token + lease for crash recovery, Reschedule/Park on failure, and the
// transactional Complete that settles the catalog cursor and removes the queue
// row in one transaction.

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// claimLease bounds how long a claim is considered live; a claim older than
// this is reclaimable (crash/OOM recovery — the orphaned-claim reaper).
const claimLease = 15 * time.Minute

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

// Enqueue upserts a work item. UNIQUE(catalog_id) coalesces repeated changes
// into one pending item — latest to_version wins. A row that is NOT in progress
// is reset to a fresh ready state; an in-progress row is left claimed (only its
// target version advances), so a coalescing enqueue can never yank a row out
// from under the worker holding it.
func (s *Store) Enqueue(ctx context.Context, item QueueItem) error {
	op := item.Op
	if op == "" {
		op = "sync"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO crawler_queue
		   (catalog_id, index_url, from_version, to_version, op, status, attempts, next_attempt_at, claimed_at, claim_id, enqueued_at)
		 VALUES ($1,$2,$3,$4,$5,'queued',0, now(), NULL, NULL, now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url    = EXCLUDED.index_url,
		   to_version   = GREATEST(crawler_queue.to_version, EXCLUDED.to_version),
		   op           = EXCLUDED.op,
		   status       = CASE WHEN crawler_queue.claimed_at IS NULL THEN 'queued' ELSE crawler_queue.status END,
		   attempts     = CASE WHEN crawler_queue.claimed_at IS NULL THEN 0 ELSE crawler_queue.attempts END,
		   next_attempt_at = CASE WHEN crawler_queue.claimed_at IS NULL THEN now() ELSE crawler_queue.next_attempt_at END`,
		item.CatalogID, item.IndexURL, nullInt64Zero(item.FromVersion), item.ToVersion, op)
	if err != nil {
		return fmt.Errorf("store: Enqueue: %w", err)
	}
	return nil
}

// ClaimNext atomically claims the next ready item — unclaimed OR past its lease
// (reclaiming a crashed worker's row), and past its backoff — using FOR UPDATE
// SKIP LOCKED, stamping a fresh claim_id.
func (s *Store) ClaimNext(ctx context.Context) (*ClaimedItem, error) {
	var (
		it  ClaimedItem
		fv  sql.NullInt64
		cid sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`UPDATE crawler_queue
		    SET claimed_at = now(), status = 'in_progress', claim_id = gen_random_uuid()
		  WHERE id = (
		    SELECT id FROM crawler_queue
		     WHERE (claimed_at IS NULL OR claimed_at < now() - $1::interval)
		       AND next_attempt_at <= now()
		     ORDER BY next_attempt_at
		     FOR UPDATE SKIP LOCKED
		     LIMIT 1)
		 RETURNING id, claim_id, catalog_id, index_url, from_version, to_version, op, attempts, enqueued_at`,
		claimLease.String()).
		Scan(&it.ID, &cid, &it.CatalogID, &it.IndexURL, &fv, &it.ToVersion, &it.Op, &it.Attempts, &it.EnqueuedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: ClaimNext: %w", err)
	}
	it.FromVersion, it.ClaimID = fv.Int64, cid.String
	return &it, nil
}

// RescheduleQueueItem releases a claim (only the holder's, via claim_id), bumps
// attempts, and gates the next attempt behind nextAttemptAt (the backoff).
func (s *Store) RescheduleQueueItem(ctx context.Context, id, claimID string, nextAttemptAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE crawler_queue
		    SET attempts = attempts + 1, next_attempt_at = $3, claimed_at = NULL, claim_id = NULL, status = 'queued'
		  WHERE id = $1 AND claim_id = $2`,
		id, claimID, nextAttemptAt)
	if err != nil {
		return fmt.Errorf("store: RescheduleQueueItem: %w", err)
	}
	return nil
}

// ParkQueueItem releases and parks an item that failed permanently (bad
// encoding, decompression bomb, corrupt/oversize artifact): status 'failed',
// next_attempt_at set to infinity so it is never re-claimed on its own. It
// stays parked until a version bump re-enqueues it (a coalescing Enqueue on a
// not-in-progress row resets it to ready), so a fixed/re-published catalog
// recovers automatically without hot-retrying a hopeless one.
func (s *Store) ParkQueueItem(ctx context.Context, id, claimID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE crawler_queue
		    SET attempts = attempts + 1, claimed_at = NULL, claim_id = NULL, status = 'failed', next_attempt_at = 'infinity'
		  WHERE id = $1 AND claim_id = $2`,
		id, claimID)
	if err != nil {
		return fmt.Errorf("store: ParkQueueItem: %w", err)
	}
	return nil
}

// Complete records the catalog's settled state (advancing the cursor to the
// version this worker actually pushed) and removes the queue row — but only if
// the row hasn't been superseded by a newer to_version while we worked. If it
// was superseded, the cursor still advances (real progress) and the row is
// released so the newer version is re-processed, rather than deleted.
func (s *Store) Complete(ctx context.Context, id, claimID string, toVersion int64, c CatalogState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: Complete begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort rollback if commit not reached

	if err := upsertCatalog(ctx, tx, c); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx,
		`DELETE FROM crawler_queue WHERE id = $1 AND claim_id = $2 AND to_version = $3`,
		id, claimID, toVersion)
	if err != nil {
		return fmt.Errorf("store: Complete delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Superseded (a newer to_version arrived) or claim lost: don't delete;
		// release so the newer target is re-claimed.
		if _, err := tx.ExecContext(ctx,
			`UPDATE crawler_queue SET claimed_at = NULL, claim_id = NULL, status = 'queued', next_attempt_at = now()
			  WHERE id = $1 AND claim_id = $2`, id, claimID); err != nil {
			return fmt.Errorf("store: Complete release: %w", err)
		}
	}
	return tx.Commit()
}

// QueueDepth returns the number of pending work items (backlog gauge).
func (s *Store) QueueDepth(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM crawler_queue`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: QueueDepth: %w", err)
	}
	return n, nil
}
