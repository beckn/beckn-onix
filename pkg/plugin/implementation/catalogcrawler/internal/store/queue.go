package store

// queue.go — the work queue: coalescing Enqueue (UNIQUE catalog_id), atomic
// ClaimNext (FOR UPDATE SKIP LOCKED) with a per-claim token + lease for crash
// recovery, Reschedule/Park on failure, and the transactional Complete that
// settles the catalog cursor and removes the queue row in one transaction.
// Ported from the catalog-crawler prototype's own queue.go; simplified where
// crawlmanager's QueueItem carries no version target (it re-fetches the
// index fresh at claim time, so there is nothing to detect as "superseded"
// the way the prototype's version-carrying queue rows needed to).

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
)

// claimLease bounds how long a claim is considered live; a claim older than
// this is reclaimable (crash/OOM recovery -- the orphaned-claim reaper).
const claimLease = 15 * time.Minute

// Enqueue upserts a work item. UNIQUE(catalog_id) coalesces repeated changes
// into one pending item. A row that is NOT in progress is reset to a fresh
// ready state; an in-progress row is left claimed, so a coalescing enqueue
// can never yank a row out from under the worker holding it.
func (s *Store) Enqueue(ctx context.Context, item crawlmanager.QueueItem) error {
	op := string(item.Op)
	if op == "" {
		op = string(crawlmanager.OpSync)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO crawler_queue
		   (catalog_id, index_url, from_version, to_version, entry_version, op, status, attempts, next_attempt_at, claimed_at, claim_id, enqueued_at)
		 VALUES ($1,$2,0,0,0,$3,'queued',0, now(), NULL, NULL, now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url    = EXCLUDED.index_url,
		   op           = EXCLUDED.op,
		   status       = CASE WHEN crawler_queue.claimed_at IS NULL THEN 'queued' ELSE crawler_queue.status END,
		   attempts     = CASE WHEN crawler_queue.claimed_at IS NULL THEN 0 ELSE crawler_queue.attempts END,
		   next_attempt_at = CASE WHEN crawler_queue.claimed_at IS NULL THEN now() ELSE crawler_queue.next_attempt_at END`,
		item.CatalogID, item.IndexURL, op)
	if err != nil {
		return fmt.Errorf("store: Enqueue: %w", err)
	}
	return nil
}

// ClaimNext atomically claims the next ready item -- unclaimed OR past its
// lease (reclaiming a crashed worker's row), and past its backoff -- using
// FOR UPDATE SKIP LOCKED, stamping a fresh claim_id.
func (s *Store) ClaimNext(ctx context.Context) (*crawlmanager.ClaimedItem, error) {
	var (
		it           crawlmanager.ClaimedItem
		pid, cid, op sql.NullString
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
		 RETURNING id, claim_id, catalog_id, index_url, op, attempts,
		   (SELECT participant_id FROM crawler_index WHERE crawler_index.index_url = crawler_queue.index_url)`,
		claimLease.String()).
		Scan(&it.ID, &cid, &it.CatalogID, &it.IndexURL, &op, &it.Attempts, &pid)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: ClaimNext: %w", err)
	}
	it.ClaimID, it.ParticipantID = cid.String, pid.String
	it.Op = crawlmanager.Op(op.String)
	return &it, nil
}

// Reschedule releases a claim (only the holder's, via claim_id), bumps
// attempts, and gates the next attempt behind nextAttemptAt (the backoff).
func (s *Store) Reschedule(ctx context.Context, id, claimID string, nextAttemptAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE crawler_queue
		    SET attempts = attempts + 1, next_attempt_at = $3, claimed_at = NULL, claim_id = NULL, status = 'queued'
		  WHERE id = $1 AND claim_id = $2`,
		id, claimID, nextAttemptAt)
	if err != nil {
		return fmt.Errorf("store: Reschedule: %w", err)
	}
	return nil
}

// Park releases and parks an item that failed permanently: status 'failed',
// next_attempt_at set to infinity so it is never re-claimed on its own. It
// stays parked until a fresh Enqueue (a coalescing Enqueue on a
// not-in-progress row resets it to ready) re-arms it, so a fixed/
// re-published catalog recovers automatically without hot-retrying a
// hopeless one.
func (s *Store) Park(ctx context.Context, id, claimID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE crawler_queue
		    SET attempts = attempts + 1, claimed_at = NULL, claim_id = NULL, status = 'failed', next_attempt_at = 'infinity'
		  WHERE id = $1 AND claim_id = $2`,
		id, claimID)
	if err != nil {
		return fmt.Errorf("store: Park: %w", err)
	}
	return nil
}

// Complete records the catalog's settled cursor and removes the queue row,
// in one transaction -- so a crash between the two never leaves the cursor
// advanced with the item still claimed, or vice versa. Matched on id AND
// claim_id only: unlike the prototype this schema came from, crawlmanager's
// QueueItem carries no target version for Complete to check against, since
// SyncNext always resolves against whatever the index currently declares --
// there is no "superseded by a newer target" case to detect here.
func (s *Store) Complete(ctx context.Context, id, claimID string, cursor crawlmanager.CatalogCursor) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: Complete begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort rollback if commit not reached

	if err := upsertCatalog(ctx, tx, cursor); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM crawler_queue WHERE id = $1 AND claim_id = $2`, id, claimID); err != nil {
		return fmt.Errorf("store: Complete delete: %w", err)
	}
	return tx.Commit()
}
