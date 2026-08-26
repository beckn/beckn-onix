package catalogcrawler

// scheduler_test.go — covers Scheduler's lifecycle and cadence against fakes
// for crawlmanager's ports; this is about scheduling mechanics, not
// fetch/verify/resolve, so no real catalog.Fetcher is needed here.

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
)

// countingSource counts Discover calls, so a test can assert the poll loop
// actually ticked more than once within a short interval.
type countingSource struct{ n atomic.Int64 }

func (c *countingSource) Discover(context.Context) ([]crawlmanager.IndexRef, error) {
	c.n.Add(1)
	return nil, nil
}

// noopFetcher is a real (non-nil) *catalog.Fetcher these tests wire in
// wherever Params needs one just to avoid a nil-pointer panic inside
// SyncNext's syncOne -- it will fail every fetch (queue items here carry no
// real IndexURL), which is fine: these tests are about scheduling mechanics
// (does it tick, does it drain the queue), not about a fetch succeeding.
func noopFetcher() *catalog.Fetcher {
	client := crawler.NewClient(time.Second, 1024, false)
	return catalog.NewFetcher(client, crawler.StaticKeys(nil), 1024)
}

// fakeStore is the minimal crawlmanager.Store a scheduling test needs: an
// in-memory queue, enough to prove drain behavior without a real database.
type fakeStore struct {
	mu    sync.Mutex
	queue []crawlmanager.ClaimedItem

	// parkSweeps, if non-nil, counts RequeueOrAbandonParked calls -- so a
	// test can assert the park-sweep loop actually ticked.
	parkSweeps *atomic.Int64
}

func (s *fakeStore) GetCatalogCursor(context.Context, string) (crawlmanager.CatalogCursor, bool, error) {
	return crawlmanager.CatalogCursor{}, false, nil
}
func (s *fakeStore) RecordFailure(context.Context, crawlmanager.PassReport) error { return nil }
func (s *fakeStore) GetCatalogEnvelope(context.Context, string) (descriptor, provider json.RawMessage, catalogType, participantID string, ok bool, err error) {
	return nil, nil, "", "", false, nil
}
func (s *fakeStore) GetIndexCursor(context.Context, string) (*crawlmanager.IndexCursor, error) {
	return nil, nil
}
func (s *fakeStore) UpsertIndexCursor(context.Context, crawlmanager.IndexCursor) error { return nil }
func (s *fakeStore) Enqueue(_ context.Context, item crawlmanager.QueueItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, crawlmanager.ClaimedItem{QueueItem: item, ID: "id", ClaimID: "claim"})
	return nil
}
func (s *fakeStore) ClaimNext(context.Context) (*crawlmanager.ClaimedItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil, nil
	}
	item := s.queue[0]
	s.queue = s.queue[1:]
	return &item, nil
}
func (s *fakeStore) Complete(context.Context, string, string, crawlmanager.CatalogCursor) error {
	return nil
}
func (s *fakeStore) Reschedule(context.Context, string, string, time.Time) error { return nil }
func (s *fakeStore) Park(context.Context, string, string) error                  { return nil }
func (s *fakeStore) RequeueOrAbandonParked(context.Context, time.Duration, int) (int, int, error) {
	if s.parkSweeps != nil {
		s.parkSweeps.Add(1)
	}
	return 0, 0, nil
}
func (s *fakeStore) ListAbandoned(context.Context) ([]crawlmanager.AbandonedCatalog, error) {
	return nil, nil
}
func (s *fakeStore) queueLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func TestScheduler_RunsFirstTickImmediately(t *testing.T) {
	src := &countingSource{}
	sched := NewScheduler(
		crawlmanager.Params{Source: src, Store: &fakeStore{}},
		SchedulerConfig{IndexInterval: time.Hour, CatalogInterval: time.Hour}, nil,
	)
	sched.Start(context.Background())
	defer sched.Stop()

	deadline := time.After(time.Second)
	for src.n.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("expected at least one poll tick to run immediately on Start")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestScheduler_TicksRepeatedlyOnShortInterval(t *testing.T) {
	src := &countingSource{}
	sched := NewScheduler(
		crawlmanager.Params{Source: src, Store: &fakeStore{}},
		SchedulerConfig{IndexInterval: 10 * time.Millisecond, CatalogInterval: time.Hour}, nil,
	)
	sched.Start(context.Background())
	defer sched.Stop()

	deadline := time.After(2 * time.Second)
	for src.n.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("expected at least 3 ticks, got %d", src.n.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestScheduler_StopWaitsForInFlightTickAndStopsTicking(t *testing.T) {
	src := &countingSource{}
	sched := NewScheduler(
		crawlmanager.Params{Source: src, Store: &fakeStore{}},
		SchedulerConfig{IndexInterval: 5 * time.Millisecond, CatalogInterval: time.Hour}, nil,
	)
	sched.Start(context.Background())
	time.Sleep(50 * time.Millisecond) // let a few ticks happen
	sched.Stop()

	n := src.n.Load()
	time.Sleep(50 * time.Millisecond) // long enough for another tick, if it were still running
	if src.n.Load() != n {
		t.Fatalf("ticks after Stop: before=%d after=%d, want no further ticks", n, src.n.Load())
	}
}

func TestScheduler_SyncTickDrainsWholeQueueInOneTick(t *testing.T) {
	store := &fakeStore{}
	ctx := context.Background()
	for _, id := range []string{"p/a", "p/b", "p/c"} {
		if err := store.Enqueue(ctx, crawlmanager.QueueItem{CatalogID: id}); err != nil {
			t.Fatal(err)
		}
	}
	sched := NewScheduler(
		crawlmanager.Params{Source: &countingSource{}, Store: store, Fetcher: noopFetcher()},
		SchedulerConfig{IndexInterval: time.Hour, CatalogInterval: time.Hour}, nil,
	)
	sched.Start(ctx)
	defer sched.Stop()

	deadline := time.After(2 * time.Second)
	for store.queueLen() != 0 {
		select {
		case <-deadline:
			t.Fatalf("queue still has %d items, want drained to 0", store.queueLen())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestScheduler_RunOnceNeverOrphansAcrossConcurrentStop stresses RunOnce
// racing against Stop: every RunOnce call that reports true must actually
// be observed by Stop's Wait (never orphaned after Stop has returned), and
// every call after Stop must report false. Run with -race to catch any
// reintroduction of the check-then-Add/cancel-then-Wait race this guards
// against.
func TestScheduler_RunOnceNeverOrphansAcrossConcurrentStop(t *testing.T) {
	for i := 0; i < 200; i++ {
		sched := NewScheduler(
			crawlmanager.Params{Source: &countingSource{}, Store: &fakeStore{}},
			SchedulerConfig{IndexInterval: time.Hour, CatalogInterval: time.Hour}, nil,
		)
		sched.Start(context.Background())

		var ran atomic.Bool
		done := make(chan bool, 1)
		go func() {
			done <- sched.RunOnce(func(context.Context) {
				time.Sleep(time.Millisecond)
				ran.Store(true)
			})
		}()

		sched.Stop()

		if started := <-done; started && !ran.Load() {
			t.Fatalf("iteration %d: RunOnce reported started, but Stop returned before its goroutine ran -- orphaned", i)
		}
	}
}

func TestScheduler_ParkSweepTicksOnItsOwnIndependentCadence(t *testing.T) {
	var parkSweeps atomic.Int64
	fs := &fakeStore{parkSweeps: &parkSweeps}
	sched := NewScheduler(
		crawlmanager.Params{Source: &countingSource{}, Store: fs},
		SchedulerConfig{IndexInterval: time.Hour, CatalogInterval: time.Hour, ParkSweepInterval: 10 * time.Millisecond}, nil,
	)
	sched.Start(context.Background())
	defer sched.Stop()

	deadline := time.After(2 * time.Second)
	for parkSweeps.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("expected at least 3 park sweeps, got %d", parkSweeps.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
}
