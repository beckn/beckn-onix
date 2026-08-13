package runner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/source"
)

// logEntry is one recorded fakeLogger call.
type logEntry struct {
	level string
	event string
	kv    []any
}

// kvString looks up a string value by key in a recorded call's kv pairs.
func (e logEntry) kvString(key string) (string, bool) {
	for i := 0; i+1 < len(e.kv); i += 2 {
		if k, ok := e.kv[i].(string); ok && k == key {
			if v, ok := e.kv[i+1].(string); ok {
				return v, true
			}
		}
	}
	return "", false
}

// fakeLogger is a recording Logger test double: no fake Logger existed yet in
// this package, so CrawlNow's on-demand logging had zero test coverage.
type fakeLogger struct {
	mu      sync.Mutex
	entries []logEntry
}

func (f *fakeLogger) record(level, event string, kv []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, logEntry{level: level, event: event, kv: kv})
}

func (f *fakeLogger) Debug(event string, kv ...any) { f.record("debug", event, kv) }
func (f *fakeLogger) Info(event string, kv ...any)  { f.record("info", event, kv) }
func (f *fakeLogger) Warn(event string, kv ...any)  { f.record("warn", event, kv) }
func (f *fakeLogger) Error(event string, kv ...any) { f.record("error", event, kv) }

// findByRunID looks up the recorded call at the given level whose event
// matches eventSubstr and whose run_id kv equals runID. The scheduled index
// pass emits its own "crawl finished" summary under a different run_id, so
// matching on event text alone would be ambiguous.
func (f *fakeLogger) findByRunID(level, eventSubstr, runID string) *logEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.entries {
		e := f.entries[i]
		if e.level != level || !strings.Contains(e.event, eventSubstr) {
			continue
		}
		if v, _ := e.kvString("run_id"); v == runID {
			return &f.entries[i]
		}
	}
	return nil
}

// An on-demand /crawl now takes a REGISTRY (not a raw index URL): CrawlRegistry
// asks the injected registry-source factory for the network's providers, then
// crawls every discovered index on the on_demand trigger — the same discovery
// the scheduled pass uses, so every crawl input is registry-based. This proves
// the factory receives the request's registry + networks and that BOTH
// discovered indexes are crawled.
func TestEngine_CrawlRegistry_DiscoversAndCrawls(t *testing.T) {
	s := openTestStore(t)
	url1 := "https://p1.example/i.json"
	url2 := "https://p2.example/i.json"
	indexFor := func(pid string) catalog.Index {
		return catalog.Index{NodeID: pid, Catalogs: []catalog.CatalogEntry{{
			CatalogID: pid + "/c", EntryVersion: 1,
			Baseline: catalog.FileEntry{Version: 1, URL: "base", Digest: "d"},
		}}}
	}

	var mu sync.Mutex
	fetched := map[string]bool{}
	var gotRegistryURL string
	var gotNetworks []string

	logger := &fakeLogger{}
	eng := New(EngineConfig{MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour}, Deps{
		Store: s,
		FetchIndex: func(_ context.Context, u string, _ catalog.IndexConditions) (catalog.IndexResult, error) {
			mu.Lock()
			fetched[u] = true
			mu.Unlock()
			pid := "p1"
			if u == url2 {
				pid = "p2"
			}
			return catalog.IndexResult{Index: indexFor(pid)}, nil
		},
		NewRegistrySource: func(registryURL string, networkIDs []string) source.Source {
			gotRegistryURL = registryURL
			gotNetworks = networkIDs
			// Stands in for a DeDi /query lookup that discovered two providers.
			return source.NewConfigSource([]string{url1, url2})
		},
		Metrics: &recMetrics{},
		Log:     logger,
		NewID:   func() string { return "run-reg" },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.mu.Lock()
	eng.ctx, eng.stop, eng.state = ctx, cancel, DaemonReady
	eng.mu.Unlock()

	runID, err := eng.CrawlRegistry(ctx, "https://registry.example/dedi", []string{"beckn.one/testnet"})
	if err != nil {
		t.Fatalf("CrawlRegistry: %v", err)
	}
	if runID == "" {
		t.Fatal("CrawlRegistry must return a non-empty run_id")
	}
	eng.wg.Wait()

	if gotRegistryURL != "https://registry.example/dedi" || len(gotNetworks) != 1 || gotNetworks[0] != "beckn.one/testnet" {
		t.Fatalf("factory got registry=%q networks=%v, want the request's registry + network", gotRegistryURL, gotNetworks)
	}
	mu.Lock()
	defer mu.Unlock()
	if !fetched[url1] || !fetched[url2] {
		t.Fatalf("both discovered indexes must be crawled; fetched=%v", fetched)
	}
	found := logger.findByRunID("info", "crawl finished", runID)
	if found == nil {
		t.Fatalf("no on-demand 'crawl finished' summary for run_id %q; entries: %+v", runID, logger.entries)
	}
	if v, _ := found.kvString("trigger"); v != "on_demand" {
		t.Fatalf("summary trigger = %q, want on_demand", v)
	}
}

// gateStore is a Store test double that parks every crawl inside GetIndex: it
// records the index URL, announces the entry on entered, then blocks until
// release is closed. Parking at the first store call makes the per-index lock
// observable — a second crawl of the same index cannot reach GetIndex while the
// first is still parked in it. Every other method is an inert zero-value stub;
// these tests never get past the fetch.
type gateStore struct {
	entered chan string   // one send per GetIndex entry (buffered, never blocks)
	release chan struct{} // closed to let every parked GetIndex return

	mu   sync.Mutex
	urls []string // index URLs GetIndex was entered with, in entry order
}

func newGateStore() *gateStore {
	return &gateStore{entered: make(chan string, 8), release: make(chan struct{})}
}

func (g *gateStore) GetIndex(ctx context.Context, indexURL string) (*catalog.IndexState, error) {
	g.mu.Lock()
	g.urls = append(g.urls, indexURL)
	g.mu.Unlock()
	g.entered <- indexURL
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	return nil, nil
}

func (g *gateStore) entries() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.urls...)
}

func (g *gateStore) GetCatalogVersion(context.Context, string) (int64, int64, bool, error) {
	return 0, 0, false, nil
}
func (g *gateStore) UpsertCatalog(context.Context, catalog.CatalogState) error { return nil }
func (g *gateStore) CountParked(context.Context) (int, error)                  { return 0, nil }
func (g *gateStore) CountTracked(context.Context) (int, error)                 { return 0, nil }
func (g *gateStore) GetCatalogReports(context.Context, string) ([]catalog.PassReport, error) {
	return nil, nil
}
func (g *gateStore) RecordFailure(context.Context, string, string, string, catalog.PassReport) error {
	return nil
}
func (g *gateStore) GetCatalogEnvelope(context.Context, string) ([]byte, []byte, string, string, bool, error) {
	return nil, nil, "", "", false, nil
}
func (g *gateStore) KnownIndexes(context.Context) ([]catalog.KnownIndex, error) { return nil, nil }
func (g *gateStore) UpsertIndex(context.Context, string, string, string, string, time.Time, string, string) error {
	return nil
}
func (g *gateStore) AdvanceIndexCadence(context.Context, string, time.Time) error { return nil }
func (g *gateStore) Enqueue(context.Context, catalog.QueueItem) error             { return nil }
func (g *gateStore) ClaimNext(context.Context) (*catalog.ClaimedItem, error)      { return nil, nil }
func (g *gateStore) RescheduleQueueItem(context.Context, string, string, time.Time) error {
	return nil
}
func (g *gateStore) ParkQueueItem(context.Context, string, string) error { return nil }
func (g *gateStore) Complete(context.Context, string, string, int64, int64, catalog.CatalogState) error {
	return nil
}
func (g *gateStore) QueueDepth(context.Context) (int, error) { return 0, nil }

// Two crawls of the SAME index must serialise — the on-demand /crawl trigger
// and the scheduled ticker otherwise race on that index's version cursor and
// next_crawl_at (read-both, decide-both, write-both). Two crawls of DIFFERENT
// indexes must still overlap: the lock is per index, not a global engine lock,
// so one parked publisher must not stall the rest of the pass.
func TestEngine_CrawlIndex_LocksPerIndex(t *testing.T) {
	const (
		urlA = "https://x/a.json"
		urlB = "https://x/b.json"
	)
	tests := []struct {
		name           string
		second         string // index URL the second concurrent crawl targets
		wantConcurrent bool   // may the second crawl enter while the first is parked?
	}{
		{"same index serialises", urlA, false},
		{"different indexes run concurrently", urlB, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := newGateStore()
			eng := New(EngineConfig{IndexInterval: time.Hour}, Deps{
				Store: gate,
				FetchIndex: func(context.Context, string, catalog.IndexConditions) (catalog.IndexResult, error) {
					return catalog.IndexResult{Index: catalog.Index{NodeID: "p"}}, nil
				},
			})

			ctx := context.Background()
			var wg sync.WaitGroup
			crawl := func(url string) {
				wg.Add(1)
				go func() {
					defer wg.Done()
					eng.crawlIndex(ctx, source.IndexRef{IndexURL: url, Source: source.KindOnDemand}, onDemand, "run-1")
				}()
			}

			crawl(urlA)
			select {
			case got := <-gate.entered:
				if got != urlA {
					t.Fatalf("first crawl entered with %q, want %q", got, urlA)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("first crawl never reached GetIndex")
			}

			// The first crawl is now parked inside GetIndex holding urlA's lock.
			crawl(tt.second)
			gotConcurrent := false
			select {
			case <-gate.entered:
				gotConcurrent = true
			case <-time.After(200 * time.Millisecond):
			}
			if gotConcurrent != tt.wantConcurrent {
				t.Fatalf("second crawl of %q entered while first parked = %v, want %v", tt.second, gotConcurrent, tt.wantConcurrent)
			}

			close(gate.release)
			wg.Wait()

			// Serialised does not mean dropped: the blocked crawl must still run,
			// just after the first one let go.
			if got := gate.entries(); len(got) != 2 {
				t.Fatalf("GetIndex entries = %v, want both crawls to have run", got)
			}
			// Lock entries are reference-counted, so the table must be empty
			// again once every crawl is done (no unbounded growth).
			eng.mu.Lock()
			left := len(eng.indexLocks)
			eng.mu.Unlock()
			if left != 0 {
				t.Fatalf("indexLocks still holds %d entries after both crawls finished, want 0", left)
			}
		})
	}
}

// A crawl that is waiting on another crawl's index lock must abandon the wait
// when the engine's context ends, rather than running a full crawl (and its
// writes) after Stop() has already cancelled everything.
func TestEngine_CrawlIndex_CancelledWhileWaitingForLock(t *testing.T) {
	const url = "https://x/a.json"
	gate := newGateStore()
	eng := New(EngineConfig{IndexInterval: time.Hour}, Deps{
		Store: gate,
		FetchIndex: func(context.Context, string, catalog.IndexConditions) (catalog.IndexResult, error) {
			return catalog.IndexResult{Index: catalog.Index{NodeID: "p"}}, nil
		},
	})

	holder, holderCancel := context.WithCancel(context.Background())
	defer holderCancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		eng.crawlIndex(holder, source.IndexRef{IndexURL: url, Source: source.KindOnDemand}, onDemand, "run-1")
	}()
	<-gate.entered // first crawl is parked holding the lock

	waiter, waiterCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		eng.crawlIndex(waiter, source.IndexRef{IndexURL: url, Source: source.KindOnDemand}, scheduled, "run-2")
	}()
	waiterCancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waiting crawl did not return after its context was cancelled")
	}
	if got := gate.entries(); len(got) != 1 {
		t.Fatalf("GetIndex entries = %v, want only the lock holder's", got)
	}

	close(gate.release)
	wg.Wait()
	eng.mu.Lock()
	left := len(eng.indexLocks)
	eng.mu.Unlock()
	if left != 0 {
		t.Fatalf("indexLocks still holds %d entries, want 0 (the abandoned waiter must drop its ref)", left)
	}
}

// --- crawl-pass completeness ------------------------------------------------

// errStore is the injected store outage. Distinct values make the assertion
// messages say WHICH op was staged to fail.
var (
	errCursorRead = errors.New("read_cursor: connection reset")
	errEnqueue    = errors.New("enqueue: pool exhausted")
	errGetIndex   = errors.New("read_index: server closed the connection")
	errKnown      = errors.New("list_indexes: server closed the connection")
)

// indexUpsert is one recorded UpsertIndex call — the fields that decide
// whether the next poll gets a real GET or a 304: the sync status and the
// conditional-GET validators the next poll will echo.
type indexUpsert struct {
	status       string
	etag         string
	lastModified string
}

// memStore is a stateful in-memory Store: it keeps the single crawler_index row
// so a SECOND indexPass reads back what the first one wrote. That read-back is
// the whole point — a version recorded after a partial pass is exactly what
// makes the next pass call the index "unchanged" and lose the update. Per-op
// errors are injectable to stage a mid-pass store blip.
type memStore struct {
	mu sync.Mutex

	index    *catalog.IndexState // nil until the first UpsertIndex
	cursors  map[string]int64    // catalog id -> version already synced
	// enqueued mirrors the real store's coalescing Enqueue (UNIQUE catalog_id,
	// ON CONFLICT DO UPDATE) -- a naive append here would inflate a call-count
	// assertion every time the index job re-decides an already-queued catalog,
	// which it now does on every full fetch (there is no more index-level
	// version to skip re-deciding on).
	enqueued map[string]catalog.QueueItem
	upserts  []indexUpsert

	cursorErr   error
	enqueueErr  error
	getIndexErr error
	knownErr    error
	upsertErr   error
}

func newMemStore() *memStore {
	return &memStore{cursors: map[string]int64{}, enqueued: map[string]catalog.QueueItem{}}
}

// heal clears every injected error, so a test can run a second pass against a
// recovered store — the realistic sequence, since a blip is transient.
func (m *memStore) heal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cursorErr, m.enqueueErr, m.getIndexErr, m.knownErr, m.upsertErr = nil, nil, nil, nil, nil
}

func (m *memStore) seedIndex(s catalog.IndexState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.index = &s
}

func (m *memStore) lastUpsert() (indexUpsert, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.upserts) == 0 {
		return indexUpsert{}, false
	}
	return m.upserts[len(m.upserts)-1], true
}

func (m *memStore) enqueuedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.enqueued)
}

func (m *memStore) GetIndex(context.Context, string) (*catalog.IndexState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getIndexErr != nil {
		return nil, m.getIndexErr
	}
	if m.index == nil {
		return nil, nil
	}
	cp := *m.index
	return &cp, nil
}

func (m *memStore) UpsertIndex(_ context.Context, _, _, _ string, syncStatus string, nextCrawlAt time.Time, etag, lastModified string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upsertErr != nil {
		return m.upsertErr
	}
	m.upserts = append(m.upserts, indexUpsert{status: syncStatus, etag: etag, lastModified: lastModified})
	m.index = &catalog.IndexState{
		SyncStatus:  syncStatus,
		NextCrawlAt: nextCrawlAt, ETag: etag, LastModified: lastModified,
	}
	return nil
}

func (m *memStore) GetCatalogVersion(_ context.Context, catalogID string) (int64, int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cursorErr != nil {
		return 0, 0, false, m.cursorErr
	}
	v, seen := m.cursors[catalogID]
	return v, 0, seen, nil
}

func (m *memStore) Enqueue(_ context.Context, item catalog.QueueItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enqueueErr != nil {
		return m.enqueueErr
	}
	m.enqueued[item.CatalogID] = item
	return nil
}

func (m *memStore) KnownIndexes(context.Context) ([]catalog.KnownIndex, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.knownErr != nil {
		return nil, m.knownErr
	}
	return nil, nil
}

func (m *memStore) AdvanceIndexCadence(_ context.Context, _ string, nextCrawlAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upsertErr != nil {
		return m.upsertErr
	}
	if m.index != nil {
		m.index.NextCrawlAt = nextCrawlAt
	}
	return nil
}

func (m *memStore) UpsertCatalog(context.Context, catalog.CatalogState) error { return nil }
func (m *memStore) CountParked(context.Context) (int, error)                  { return 0, nil }
func (m *memStore) CountTracked(context.Context) (int, error)                 { return 0, nil }
func (m *memStore) GetCatalogReports(context.Context, string) ([]catalog.PassReport, error) {
	return nil, nil
}
func (m *memStore) RecordFailure(context.Context, string, string, string, catalog.PassReport) error {
	return nil
}
func (m *memStore) GetCatalogEnvelope(context.Context, string) ([]byte, []byte, string, string, bool, error) {
	return nil, nil, "", "", false, nil
}
func (m *memStore) ClaimNext(context.Context) (*catalog.ClaimedItem, error) { return nil, nil }
func (m *memStore) RescheduleQueueItem(context.Context, string, string, time.Time) error {
	return nil
}
func (m *memStore) ParkQueueItem(context.Context, string, string) error { return nil }
func (m *memStore) Complete(context.Context, string, string, int64, int64, catalog.CatalogState) error {
	return nil
}
func (m *memStore) QueueDepth(context.Context) (int, error) { return 0, nil }

// crawlHarness wires an engine over a memStore against one index URL holding one
// public catalog at version 5, with a movable clock so a second pass can be run
// past the first one's next_crawl_at.
type crawlHarness struct {
	eng      *Engine
	store    *memStore
	metrics  *recMetrics
	now      time.Time
	lastCond catalog.IndexConditions // validators the last poll sent
}

const crawlTestURL = "https://x/i.json"

func newCrawlHarness(t *testing.T, urls []string, idx catalog.Index, fetchErr error) *crawlHarness {
	t.Helper()
	h := &crawlHarness{store: newMemStore(), metrics: &recMetrics{}, now: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)}
	h.eng = New(EngineConfig{
		MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour,
	}, Deps{
		Store:  h.store,
		Source: source.NewConfigSource(urls),
		FetchIndex: func(_ context.Context, _ string, cond catalog.IndexConditions) (catalog.IndexResult, error) {
			h.lastCond = cond
			if fetchErr != nil {
				return catalog.IndexResult{}, fetchErr
			}
			return catalog.IndexResult{Index: idx, ETag: "e5", LastModified: "lm5"}, nil
		},
		Metrics: h.metrics,
		Now:     func() time.Time { return h.now },
		NewID:   func() string { return "run-1" },
	})
	return h
}

// oneCatalogIndex is a publisher index carrying one public catalog at
// entryVersion 1, content version 5.
func oneCatalogIndex() catalog.Index {
	return catalog.Index{
		NodeID: "p",
		Catalogs: []catalog.CatalogEntry{{
			CatalogID: "p/c", EntryVersion: 1, // public
			Baseline: catalog.FileEntry{Version: 5, URL: "base", Digest: "d"},
		}},
	}
}

// indexPass must trace both the moment it starts crawling an index (before
// the fetch even completes) and, per catalog, what the index declared versus
// what's stored locally -- otherwise diagnosing why one specific catalog
// didn't move means inferring it from whichever terminal log happened to
// fire, if any did at all.
func TestIndexPass_TracesCrawlingIndexAndCatalogEvaluation(t *testing.T) {
	h := newCrawlHarness(t, []string{crawlTestURL}, oneCatalogIndex(), nil)
	logger := &fakeLogger{}
	h.eng.deps.Log = logger

	h.eng.indexPass(context.Background())

	var gotCrawling, gotEvaluated bool
	for _, e := range logger.entries {
		if e.level == "debug" && e.event == "crawling index" {
			gotCrawling = true
			if v, _ := e.kvString("index_url"); v != crawlTestURL {
				t.Errorf("crawling index log index_url = %q, want %q", v, crawlTestURL)
			}
		}
		if e.level == "debug" && strings.Contains(e.event, "catalog evaluated") {
			gotEvaluated = true
			if v, _ := e.kvString("catalog_id"); v != "p/c" {
				t.Errorf("catalog evaluated log catalog_id = %q, want p/c", v)
			}
		}
	}
	if !gotCrawling {
		t.Fatalf("no 'crawling index' DEBUG trace found; entries: %+v", logger.entries)
	}
	if !gotEvaluated {
		t.Fatalf("no 'catalog evaluated' DEBUG trace found; entries: %+v", logger.entries)
	}
}

// A content-lineage regression (the index's latest version is behind our
// stored cursor) must log at ERROR, not WARN: it is an operator-actionable
// anomaly (a republished old snapshot, a rolled-back publish pipeline, a
// misbehaving index) that will not resolve itself and is otherwise invisible.
func TestDecideCatalog_RollbackLogsError(t *testing.T) {
	store := newMemStore()
	store.cursors["p/c"] = 10 // ahead of the index's declared content version (5)
	logger := &fakeLogger{}
	eng := New(EngineConfig{}, Deps{
		Store: store, Log: logger, Metrics: &recMetrics{},
		Now: time.Now, NewID: func() string { return "run-1" },
	})
	entry := catalog.CatalogEntry{
		CatalogID: "p/c", EntryVersion: 1,
		Baseline: catalog.FileEntry{Version: 5, URL: "base", Digest: "d"},
	}

	queued, failed := eng.decideCatalog(context.Background(), source.IndexRef{IndexURL: crawlTestURL}, scheduled, entry, "run-1")

	if queued || failed {
		t.Fatalf("queued=%v failed=%v, want false,false (a rollback queues nothing)", queued, failed)
	}
	var found *logEntry
	for i := range logger.entries {
		if logger.entries[i].level == "error" && strings.Contains(logger.entries[i].event, "went backwards") {
			found = &logger.entries[i]
		}
	}
	if found == nil {
		t.Fatalf("no ERROR-level rollback log found; entries: %+v", logger.entries)
	}
	if v, _ := found.kvString("catalog_id"); v != "p/c" {
		t.Fatalf("rollback log catalog_id = %q, want p/c", v)
	}
}

// A crawl pass that could not decide or enqueue EVERY catalog in the index
// must not record the conditional-GET validators.
//
// This is the silent-loss bug. decideCatalog logs a failed cursor read or a
// failed Enqueue and moves on; if the ETag is then written anyway, the next
// pass echoes it, the host answers 304, and decideCatalogs never runs again —
// the catalog whose Enqueue failed sits at its old version until the
// publisher republishes, possibly days.
//
// So each case asserts the whole loop: what pass 1 recorded, what validators
// pass 2 sends, and whether pass 2 (against a healed store) actually re-decides
// and enqueues the work pass 1 lost. The clean pass is the control — it DOES
// record the validators, and pass 2 correctly finds nothing new to do (the one
// item pass 1 already queued).
func TestIndexPass_PartialFailure_WithholdsValidators(t *testing.T) {
	tests := []struct {
		name string
		// stage installs the store failure for pass 1.
		stage      func(*memStore)
		wantStatus string
		wantETag   string
		// validators pass 2 must send, and work it must (re-)enqueue.
		wantPass2ETag     string
		wantPass2Enqueued int
	}{
		{
			name:              "enqueue fails mid-pass",
			stage:             func(m *memStore) { m.enqueueErr = errEnqueue },
			wantStatus:        publish.SyncFailed,
			wantETag:          "",
			wantPass2ETag:     "",
			wantPass2Enqueued: 1,
		},
		{
			name:              "cursor read fails mid-pass",
			stage:             func(m *memStore) { m.cursorErr = errCursorRead },
			wantStatus:        publish.SyncFailed,
			wantETag:          "",
			wantPass2ETag:     "",
			wantPass2Enqueued: 1,
		},
		{
			name:              "fully successful pass",
			stage:             func(*memStore) {},
			wantStatus:        publish.SyncOK,
			wantETag:          "e5",
			wantPass2ETag:     "e5",
			wantPass2Enqueued: 1, // the one pass 1 already queued; pass 2 adds none
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCrawlHarness(t, []string{crawlTestURL}, oneCatalogIndex(), nil)
			tt.stage(h.store)

			h.eng.indexPass(context.Background())

			got, ok := h.store.lastUpsert()
			if !ok {
				t.Fatal("pass 1 recorded no index row at all; the failed pass must still be persisted (with a non-OK status)")
			}
			if got.status != tt.wantStatus {
				t.Fatalf("pass 1 recorded sync_status = %q, want %q", got.status, tt.wantStatus)
			}
			if got.etag != tt.wantETag {
				t.Fatalf("pass 1 recorded etag = %q, want %q; a stored validator turns every later poll into a 304, which never re-decides", got.etag, tt.wantETag)
			}

			// The store recovers and the next scheduled tick fires.
			h.store.heal()
			h.now = h.now.Add(2 * time.Hour)
			h.eng.indexPass(context.Background())

			if h.lastCond.ETag != tt.wantPass2ETag {
				t.Fatalf("pass 2 sent If-None-Match %q, want %q", h.lastCond.ETag, tt.wantPass2ETag)
			}
			if n := h.store.enqueuedCount(); n != tt.wantPass2Enqueued {
				t.Fatalf("after pass 2 the queue holds %d item(s), want %d; a catalog update dropped by a store blip must be picked up on the next pass, not lost until the publisher bumps the version", n, tt.wantPass2Enqueued)
			}
		})
	}
}

// MarkPassSuccess("crawl") drives crawler_seconds_since_last_success. A pass
// that could not reach the crawler's own store did NOT do its job — every
// crawlIndex fails at GetIndex, nothing is polled and nothing is queued — so
// marking it successful keeps the liveness gauge fresh while the crawler is
// wedged and nothing pages. This is the same asymmetry catalogPass already
// fixed for a failed ClaimNext.
//
// The distinction that must survive: an IDLE tick is a SUCCESSFUL tick. No
// configured sources, or every index still inside its cadence gate, means there
// was nothing to do, not that the crawler is stuck. A publisher we could not
// reach is also not a wedge — that is the remote's fault, it has its own
// RecordIndexPoll("unreachable") signal, and blanking liveness on it would page
// on someone else's outage.
func TestIndexPass_MarkPassSuccess(t *testing.T) {
	future := catalog.IndexState{NextCrawlAt: time.Date(2026, 7, 29, 23, 0, 0, 0, time.UTC), ETag: "e5"}

	tests := []struct {
		name     string
		urls     []string
		fetchErr error
		stage    func(*memStore)
		want     int
	}{
		{
			name:  "healthy pass marks success",
			urls:  []string{crawlTestURL},
			stage: func(*memStore) {},
			want:  1,
		},
		{
			name:  "idle tick with no configured sources marks success",
			urls:  nil,
			stage: func(*memStore) {},
			want:  1,
		},
		{
			name:  "idle tick with nothing due marks success",
			urls:  []string{crawlTestURL},
			stage: func(m *memStore) { m.seedIndex(future) },
			want:  1,
		},
		{
			name:     "unreachable publisher still marks success",
			urls:     []string{crawlTestURL},
			fetchErr: errors.New("dial tcp: connection refused"),
			stage:    func(*memStore) {},
			want:     1,
		},
		{
			name:  "store unreachable withholds success",
			urls:  []string{crawlTestURL},
			stage: func(m *memStore) { m.getIndexErr = errGetIndex },
			want:  0,
		},
		{
			name:  "known-index listing failure withholds success",
			urls:  []string{crawlTestURL},
			stage: func(m *memStore) { m.knownErr = errKnown },
			want:  0,
		},
		{
			name:  "enqueue failure withholds success",
			urls:  []string{crawlTestURL},
			stage: func(m *memStore) { m.enqueueErr = errEnqueue },
			want:  0,
		},
		{
			name:  "cursor read failure withholds success",
			urls:  []string{crawlTestURL},
			stage: func(m *memStore) { m.cursorErr = errCursorRead },
			want:  0,
		},
		{
			name:  "index write failure withholds success",
			urls:  []string{crawlTestURL},
			stage: func(m *memStore) { m.upsertErr = errors.New("upsert_index: deadlock detected") },
			want:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCrawlHarness(t, tt.urls, oneCatalogIndex(), tt.fetchErr)
			tt.stage(h.store)

			h.eng.indexPass(context.Background())

			if got := h.metrics.passSuccess["crawl"]; got != tt.want {
				t.Fatalf("MarkPassSuccess(\"crawl\") called %d time(s), want %d", got, tt.want)
			}
		})
	}
}
