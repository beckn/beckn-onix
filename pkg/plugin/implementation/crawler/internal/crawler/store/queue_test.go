package store

// queue_test.go — Postgres-backed tests for the work queue: coalescing by
// catalog_id, one-claim-per-ready-row, fail/backoff + wrong-claim-token no-ops,
// park + version-bump recovery, Complete, superseded-version handling, and
// stale-lease reclaim. Skips when CRAWLER_TEST_DB_DSN is unset.

import (
	"context"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
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

	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 42, Op: "sync"}))
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 43, Op: "sync"}))

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
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/a", IndexURL: "i", ToVersion: 1}))
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/b", IndexURL: "i", ToVersion: 1}))

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
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 1}))

	it, err := s.ClaimNext(ctx)
	must(t, err)
	if it == nil || it.Attempts != 0 {
		t.Fatalf("first claim = %+v, want attempts 0", it)
	}

	// Fail with a future backoff -> not claimable yet.
	must(t, s.RescheduleQueueItem(ctx, it.ID, it.ClaimID, time.Now().Add(time.Hour)))
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
	must(t, s.RescheduleQueueItem(ctx, it2.ID, "00000000-0000-0000-0000-000000000000", time.Now().Add(-time.Second)))
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
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 1}))

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
	// The row survives (CountParked sees it) but it is NOT pending work, so it
	// must not sit in the backlog gauge forever.
	if parked, err := s.CountParked(ctx); err != nil {
		t.Fatal(err)
	} else if parked != 1 {
		t.Fatalf("parked = %d, want 1 (parked, not deleted)", parked)
	}
	if depth, err := s.QueueDepth(ctx); err != nil {
		t.Fatal(err)
	} else if depth != 0 {
		t.Fatalf("depth = %d, want 0 (parked is not pending work)", depth)
	}
	if got, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatalf("parked item must not be re-claimed on its own, got %+v", got)
	}

	// A version bump re-activates the parked row (fresh, ready, attempts reset).
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 2}))
	rec, err := s.ClaimNext(ctx)
	must(t, err)
	if rec == nil || rec.ToVersion != 2 || rec.Attempts != 0 {
		t.Fatalf("recovered = %+v, want ToVersion 2 attempts 0", rec)
	}
}

func TestComplete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 42}))

	it, err := s.ClaimNext(ctx)
	must(t, err)
	must(t, s.Complete(ctx, it.ID, it.ClaimID, it.ToVersion, it.EntryVersion, catalog.CatalogState{
		CatalogID: "p/c", IndexURL: "i", Version: 42, Status: "active", Report: catalog.PassReport{Outcome: "pushed"},
	}))

	if depth, err := s.QueueDepth(ctx); err != nil {
		t.Fatal(err)
	} else if depth != 0 {
		t.Fatalf("depth = %d, want 0 after complete", depth)
	}
	v, _, seen, err := s.GetCatalogVersion(ctx, "p/c")
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
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 5}))

	it, err := s.ClaimNext(ctx)
	must(t, err)
	if it == nil {
		t.Fatal("expected claim")
	}

	// Newer version arrives while v5 is being processed.
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 7}))
	if got, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatal("in-progress row was re-claimable after a coalescing enqueue")
	}

	// Completing v5 advances the cursor but must NOT delete the v7 work.
	must(t, s.Complete(ctx, it.ID, it.ClaimID, 5, it.EntryVersion, catalog.CatalogState{
		CatalogID: "p/c", IndexURL: "i", Version: 5, Status: "active", Report: catalog.PassReport{Outcome: "pushed"},
	}))
	if v, _, seen, _ := s.GetCatalogVersion(ctx, "p/c"); !seen || v != 5 {
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

// A coalescing enqueue can bump entry_version ALONE, with to_version unchanged
// (a metadata-only index edit: isActive toggled, networkIds edited -- no new
// content). Complete's optimistic-concurrency check must catch that too, not
// just a to_version bump: a worker completing with its own now-stale
// entry_version must be released for reclaim, never allowed to delete the row
// out from under a fresher entry_version -- otherwise the catalog settles at
// an entry cursor that doesn't match what actually happened, and the next
// crawl tick sees its stored entryCursor already equal to the index's real
// entryVersion and reports ActionSkipUnchanged forever: a catalog silently
// stuck as "up to date" when its content sync never actually completed at
// that entry version.
func TestComplete_EntryVersionBumpAloneIsDetectedAsSuperseded(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 1, EntryVersion: 1}))

	it, err := s.ClaimNext(ctx)
	must(t, err)
	if it == nil || it.EntryVersion != 1 {
		t.Fatalf("claimed = %+v, want EntryVersion 1", it)
	}

	// A metadata-only edit lands while v1 is being processed: entry_version
	// bumps to 2, to_version stays 1 (no new content).
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 1, EntryVersion: 2}))

	// The original worker completes with its OWN stale claimed EntryVersion (1).
	must(t, s.Complete(ctx, it.ID, it.ClaimID, it.ToVersion, it.EntryVersion, catalog.CatalogState{
		CatalogID: "p/c", IndexURL: "i", Version: 1, EntryVersion: 1, Status: "active",
		Report: catalog.PassReport{Outcome: "pushed"},
	}))

	// The row must survive (released, not deleted) so entry_version 2 gets
	// reprocessed -- losing it here is exactly the stuck-forever bug.
	if d, _ := s.QueueDepth(ctx); d != 1 {
		t.Fatalf("depth = %d, want 1 (entry_version 2 still queued, not lost)", d)
	}
	reclaimed, err := s.ClaimNext(ctx)
	must(t, err)
	if reclaimed == nil || reclaimed.EntryVersion != 2 {
		t.Fatalf("reclaim = %+v, want EntryVersion 2 preserved", reclaimed)
	}
}

// QueueDepth is the backlog gauge, and the plugin README hangs a "backlog
// growing / not draining" alert off it. So it must count ONLY rows still
// waiting to be worked. A claimed row is throughput, and a parked row is never
// re-claimed on its own — counting either pins the gauge above zero forever and
// false-fires that alert. Parked rows are reported by CountParked instead, so
// each case here asserts both gauges and neither double-counts a row.
func TestQueueDepth_CountsOnlyPendingWork(t *testing.T) {
	tests := []struct {
		name string
		// arrange leaves the queue in the state under test; it gets a store with
		// exactly one enqueued item, already claimed, plus that claim.
		arrange    func(t *testing.T, s *Store, ctx context.Context, it *catalog.ClaimedItem)
		wantDepth  int
		wantParked int
	}{
		{
			name:       "in progress is not backlog",
			arrange:    func(*testing.T, *Store, context.Context, *catalog.ClaimedItem) {},
			wantDepth:  0,
			wantParked: 0,
		},
		{
			name: "released by reschedule is backlog again",
			arrange: func(t *testing.T, s *Store, ctx context.Context, it *catalog.ClaimedItem) {
				must(t, s.RescheduleQueueItem(ctx, it.ID, it.ClaimID, time.Now().Add(-time.Second)))
			},
			wantDepth:  1,
			wantParked: 0,
		},
		{
			name: "waiting behind a backoff is still backlog",
			arrange: func(t *testing.T, s *Store, ctx context.Context, it *catalog.ClaimedItem) {
				must(t, s.RescheduleQueueItem(ctx, it.ID, it.ClaimID, time.Now().Add(time.Hour)))
			},
			wantDepth:  1,
			wantParked: 0,
		},
		{
			name: "parked is counted by CountParked, never by QueueDepth",
			arrange: func(t *testing.T, s *Store, ctx context.Context, it *catalog.ClaimedItem) {
				must(t, s.ParkQueueItem(ctx, it.ID, it.ClaimID))
			},
			wantDepth:  0,
			wantParked: 1,
		},
		{
			name: "a version bump on a parked row puts it back in the backlog",
			arrange: func(t *testing.T, s *Store, ctx context.Context, it *catalog.ClaimedItem) {
				must(t, s.ParkQueueItem(ctx, it.ID, it.ClaimID))
				must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 9}))
			},
			wantDepth:  1,
			wantParked: 0,
		},
		{
			name: "completed rows leave the queue entirely",
			arrange: func(t *testing.T, s *Store, ctx context.Context, it *catalog.ClaimedItem) {
				must(t, s.Complete(ctx, it.ID, it.ClaimID, it.ToVersion, it.EntryVersion, catalog.CatalogState{
					CatalogID: "p/c", IndexURL: "i", Version: it.ToVersion, Status: "active",
					Report: catalog.PassReport{Outcome: "pushed"},
				}))
			},
			wantDepth:  0,
			wantParked: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 1}))
			it, err := s.ClaimNext(ctx)
			must(t, err)
			if it == nil {
				t.Fatal("expected a claim to set up the case")
			}
			tt.arrange(t, s, ctx, it)

			depth, err := s.QueueDepth(ctx)
			must(t, err)
			if depth != tt.wantDepth {
				t.Errorf("QueueDepth = %d, want %d", depth, tt.wantDepth)
			}
			parked, err := s.CountParked(ctx)
			must(t, err)
			if parked != tt.wantParked {
				t.Errorf("CountParked = %d, want %d", parked, tt.wantParked)
			}
		})
	}
}

// A claim older than the lease is reclaimable (crash/OOM recovery).
func TestClaimLease_ReclaimsStale(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, catalog.QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 1}))

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
