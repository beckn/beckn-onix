package state

import (
	"context"
	"testing"
	"time"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueueCoalesce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 42, Op: "sync"}))
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 43, Op: "sync"}))

	if depth, err := s.QueueDepth(ctx); err != nil {
		t.Fatal(err)
	} else if depth != 1 {
		t.Fatalf("depth = %d, want 1 (coalesced by catalog_id)", depth)
	}
	it, err := s.ClaimNext(ctx)
	must(t, err)
	if it == nil || it.ToVersion != 43 {
		t.Fatalf("claimed = %+v, want ToVersion 43 (latest wins)", it)
	}
}

func TestClaimNext_OnePerReadyRow(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/a", IndexURL: "i", ToVersion: 1}))
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/b", IndexURL: "i", ToVersion: 1}))

	c1, err := s.ClaimNext(ctx)
	must(t, err)
	c2, err := s.ClaimNext(ctx)
	must(t, err)
	c3, err := s.ClaimNext(ctx)
	must(t, err)
	if c1 == nil || c2 == nil {
		t.Fatal("expected two claims")
	}
	if c1.CatalogID == c2.CatalogID {
		t.Fatalf("claimed same row twice: %s", c1.CatalogID)
	}
	if c3 != nil {
		t.Fatalf("third claim should be nil, got %+v", c3)
	}
}

func TestFailAndRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 1}))

	it, err := s.ClaimNext(ctx)
	must(t, err)
	if it == nil || it.Attempts != 0 {
		t.Fatalf("first claim = %+v, want attempts 0", it)
	}

	// Fail with a future backoff -> not claimable yet.
	must(t, s.FailQueueItem(ctx, it.ID, it.ClaimID, time.Now().Add(time.Hour)))
	if got, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatal("item should be gated by backoff")
	}

	// Past the backoff -> re-claim; attempts incremented, fresh claim_id.
	if _, err := s.db.ExecContext(ctx, "UPDATE crawler_queue SET next_attempt_at = now() - interval '1 second' WHERE id=$1", it.ID); err != nil {
		t.Fatal(err)
	}
	it2, err := s.ClaimNext(ctx)
	must(t, err)
	if it2 == nil || it2.Attempts != 1 {
		t.Fatalf("reclaim = %+v, want attempts 1", it2)
	}
	if it2.ClaimID == it.ClaimID {
		t.Fatal("reclaim must carry a fresh claim_id")
	}

	// A fail with the wrong claim_id must be a no-op (row stays claimed).
	must(t, s.FailQueueItem(ctx, it2.ID, "00000000-0000-0000-0000-000000000000", time.Now().Add(-time.Second)))
	if got, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatal("fail with wrong claim_id should not release the row")
	}
}

// A permanently-failed item is parked (status 'failed', next_attempt_at =
// infinity): the row survives but is never re-claimed on its own, and a
// version bump (coalescing Enqueue) re-activates it so a re-published catalog
// recovers automatically.
func TestParkAndRecover(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 1}))

	it, err := s.ClaimNext(ctx)
	must(t, err)

	// A park with the wrong claim_id is a no-op (row stays claimed by us).
	must(t, s.ParkQueueItem(ctx, it.ID, "00000000-0000-0000-0000-000000000000"))
	if got, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatal("park with wrong claim_id must not release the row")
	}

	// Park with the holder's claim_id: row survives but is never re-claimable.
	must(t, s.ParkQueueItem(ctx, it.ID, it.ClaimID))
	if depth, err := s.QueueDepth(ctx); err != nil {
		t.Fatal(err)
	} else if depth != 1 {
		t.Fatalf("depth = %d, want 1 (parked, not deleted)", depth)
	}
	if got, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatalf("parked item must not be re-claimed on its own, got %+v", got)
	}

	// A version bump re-activates the parked row (fresh, ready, attempts reset).
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 2}))
	rec, err := s.ClaimNext(ctx)
	must(t, err)
	if rec == nil || rec.ToVersion != 2 || rec.Attempts != 0 {
		t.Fatalf("recovered = %+v, want ToVersion 2 attempts 0", rec)
	}
}

func TestComplete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 42}))

	it, err := s.ClaimNext(ctx)
	must(t, err)
	must(t, s.Complete(ctx, it.ID, it.ClaimID, it.ToVersion, CatalogState{
		CatalogID: "p/c", IndexURL: "i", Version: 42, Status: "active", PushStatus: "pushed",
	}))

	if depth, err := s.QueueDepth(ctx); err != nil {
		t.Fatal(err)
	} else if depth != 0 {
		t.Fatalf("depth = %d, want 0 after complete", depth)
	}
	v, seen, err := s.GetCatalogVersion(ctx, "p/c")
	must(t, err)
	if !seen || v != 42 {
		t.Fatalf("cursor = %d seen=%v, want 42 true", v, seen)
	}
}

// A coalescing enqueue that lands while a row is in progress must not un-claim
// it, and completing the version actually processed must not drop the newer
// work (no lost update, no double-push).
func TestEnqueue_PreservesInProgressClaim(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 5}))

	it, err := s.ClaimNext(ctx)
	must(t, err)
	if it == nil {
		t.Fatal("expected claim")
	}

	// Newer version arrives while v5 is being processed.
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 7}))
	if got, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatal("in-progress row was re-claimable after a coalescing enqueue")
	}

	// Completing v5 advances the cursor but must NOT delete the v7 work.
	must(t, s.Complete(ctx, it.ID, it.ClaimID, 5, CatalogState{
		CatalogID: "p/c", IndexURL: "i", Version: 5, Status: "active", PushStatus: "pushed",
	}))
	if v, seen, _ := s.GetCatalogVersion(ctx, "p/c"); !seen || v != 5 {
		t.Fatalf("cursor = %d seen=%v, want 5", v, seen)
	}
	if d, _ := s.QueueDepth(ctx); d != 1 {
		t.Fatalf("depth = %d, want 1 (v7 still queued)", d)
	}
	it2, err := s.ClaimNext(ctx)
	must(t, err)
	if it2 == nil || it2.ToVersion != 7 {
		t.Fatalf("reclaim = %+v, want ToVersion 7", it2)
	}
}

// A claim older than the lease is reclaimable (crash/OOM recovery).
func TestClaimLease_ReclaimsStale(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 1}))

	it, err := s.ClaimNext(ctx)
	must(t, err)
	if it == nil {
		t.Fatal("expected claim")
	}
	if got, _ := s.ClaimNext(ctx); got != nil {
		t.Fatal("a fresh claim should not be reclaimable")
	}

	// Simulate a crashed worker: backdate the claim beyond the lease.
	if _, err := s.db.ExecContext(ctx, "UPDATE crawler_queue SET claimed_at = now() - interval '1 hour' WHERE id=$1", it.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ClaimNext(ctx)
	must(t, err)
	if got == nil {
		t.Fatal("a stale claim should be reclaimable after the lease")
	}
	if got.ClaimID == it.ClaimID {
		t.Fatal("reclaim should carry a fresh claim_id")
	}
}
