package runner

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
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

// An on-demand /crawl trigger must emit the same INFO "crawl finished"
// summary a scheduled pass does, carrying the run_id CrawlNow returns to its
// caller and a trigger field marking it as on_demand — otherwise a
// successful on-demand crawl is silent and indistinguishable from a
// scheduled one.
func TestEngine_CrawlNow_LogsFinishedSummary(t *testing.T) {
	s := openTestStore(t)
	url := "https://x/i.json"

	entry := catalog.CatalogEntry{
		CatalogID: "p/c", Status: catalog.StatusActive, // public
		Baseline: catalog.FileEntry{Version: 1, URL: "base", Digest: "d"},
	}
	idx := catalog.Index{ParticipantID: "p", Version: 1, Catalogs: []catalog.CatalogEntry{entry}}

	logger := &fakeLogger{}
	eng := New(EngineConfig{
		MaxAttempts:     3,
		IndexInterval:   time.Hour,
		CatalogInterval: time.Hour,
	}, Deps{
		Store: s,
		FetchIndex: func(context.Context, string, catalog.IndexConditions) (catalog.IndexResult, error) {
			return catalog.IndexResult{Index: idx}, nil
		},
		Metrics: &recMetrics{},
		Log:     logger,
		NewID:   func() string { return "run-123" },
	})

	// Wire the engine to DaemonReady directly instead of via Start(), so only
	// CrawlNow's own tracked goroutine runs — no scheduled-pass goroutines to
	// race against, and no need to cancel a shared context to drain them
	// (Stop() cancels ctx before waiting, which would race CrawlNow's
	// in-flight DB call if it fired that soon after starting).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.mu.Lock()
	eng.ctx, eng.stop, eng.state = ctx, cancel, DaemonReady
	eng.mu.Unlock()

	runID, err := eng.CrawlNow(ctx, url)
	if err != nil {
		t.Fatalf("CrawlNow: %v", err)
	}
	if runID == "" {
		t.Fatal("CrawlNow must return a non-empty run_id")
	}
	eng.wg.Wait() // drains CrawlNow's goroutine

	found := logger.findByRunID("info", "crawl finished", runID)
	if found == nil {
		t.Fatalf("no INFO 'crawl finished' summary logged for run_id %q; entries: %+v", runID, logger.entries)
	}
	if v, _ := found.kvString("trigger"); v != "on_demand" {
		t.Fatalf("summary trigger = %q, want %q", v, "on_demand")
	}
	wantInt := map[string]int{"indexes": 1, "updated": 1, "queued": 1}
	for i := 0; i+1 < len(found.kv); i += 2 {
		key, ok := found.kv[i].(string)
		if !ok {
			continue
		}
		want, tracked := wantInt[key]
		if !tracked {
			continue
		}
		got, ok := found.kv[i+1].(int)
		if !ok || got != want {
			t.Fatalf("summary kv %q = %v, want %d", key, found.kv[i+1], want)
		}
	}
}
