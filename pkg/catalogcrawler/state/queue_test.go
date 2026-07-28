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

	depth, err := s.QueueDepth(ctx)
	must(t, err)
	if depth != 1 {
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
	must(t, s.FailQueueItem(ctx, it.ID, time.Now().Add(time.Hour)))
	if got, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatal("item should be gated by backoff")
	}

	// Fail again with a past backoff -> claimable, attempts incremented.
	must(t, s.FailQueueItem(ctx, it.ID, time.Now().Add(-time.Second)))
	it2, err := s.ClaimNext(ctx)
	must(t, err)
	if it2 == nil || it2.Attempts != 2 {
		t.Fatalf("reclaim = %+v, want attempts 2", it2)
	}
}

func TestComplete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Enqueue(ctx, QueueItem{CatalogID: "p/c", IndexURL: "i", ToVersion: 42}))

	it, err := s.ClaimNext(ctx)
	must(t, err)
	must(t, s.Complete(ctx, it.ID, CatalogState{
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
