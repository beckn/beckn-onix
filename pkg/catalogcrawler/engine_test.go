package catalogcrawler

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/state"
)

// withSchema scopes a DSN to a dedicated Postgres schema (via search_path) so
// parallel test packages don't share the same tables.
func withSchema(dsn, schema string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "options=-c%20search_path%3D" + schema
}

// openTestStore connects to CRAWLER_TEST_DB_DSN in an engine-only schema,
// migrates, and truncates. Engine tests skip when no DSN is set.
func openTestStore(t *testing.T) *state.Store {
	t.Helper()
	dsn := os.Getenv("CRAWLER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("set CRAWLER_TEST_DB_DSN to run engine tests")
	}
	db, err := state.Open(withSchema(dsn, "cc_test_engine"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS cc_test_engine"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := state.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, "TRUNCATE crawler_queue, crawler_catalog, crawler_index"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return state.New(db)
}

func TestEngine_IndexThenCatalogPass(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	entry := CatalogEntry{
		CatalogID: "p/c", Status: StatusActive, // public
		Baseline: FileEntry{Version: 1, URL: "base", Digest: "d"},
	}
	idx := Index{ParticipantID: "p", Version: 1, Catalogs: []CatalogEntry{entry}}
	files := map[string][]byte{"base": []byte(`{"id":"p/c","resources":[{"id":"r1"}]}`)}

	var pushed [][]byte
	rec := &recMetrics{}
	eng := New(Config{
		MaxAttempts:   3,
		IndexInterval: time.Hour, CatalogInterval: time.Hour,
	}, Deps{
		Store:      s,
		Source:     NewConfigSource([]string{"https://x/i.json"}),
		FetchIndex: func(context.Context, string, IndexCond) (IndexResult, error) { return IndexResult{Index: idx}, nil },
		FetchFile:  func(_ context.Context, f FileEntry) ([]byte, error) { return files[f.URL], nil },
		Validate:   func(context.Context, []byte) error { return nil },
		Push: func(_ context.Context, body []byte) (PartOutcome, error) {
			pushed = append(pushed, body)
			return PartOutcome{Acked: true, HTTPStatus: 200}, nil
		},
		Metrics: rec,
		NewID:   func() string { return "id" },
	})

	// Index pass enqueues the (new) catalog.
	eng.indexPass(ctx)
	if d, _ := s.QueueDepth(ctx); d != 1 {
		t.Fatalf("queue depth after index pass = %d, want 1", d)
	}

	// Catalog pass resolves, pushes, and settles.
	eng.catalogPass(ctx)
	if len(pushed) != 1 {
		t.Fatalf("pushed %d times, want 1", len(pushed))
	}
	if rec.pushed != 1 {
		t.Fatalf("metrics CatalogPushed = %d, want 1", rec.pushed)
	}
	if d, _ := s.QueueDepth(ctx); d != 0 {
		t.Fatalf("queue depth after catalog pass = %d, want 0", d)
	}
	v, seen, _ := s.GetCatalogVersion(ctx, "p/c")
	if !seen || v != 1 {
		t.Fatalf("cursor = %d seen=%v, want 1 true", v, seen)
	}

	// A detailed pass report was appended: pushed, FULL, 1 resource.
	reports, err := s.GetCatalogReports(ctx, "p/c")
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("want 1 pass report, got %d", len(reports))
	}
	if r := reports[0]; r.Outcome != "pushed" || r.Mode != "FULL" || r.Resources != 1 || r.BatchesTotal < 1 {
		t.Fatalf("pass report = %+v, want pushed/FULL/1 resource", r)
	}

	// Pushed body must carry updateMode FULL.
	var body struct {
		Message struct {
			PublishDirectives []struct {
				UpdateMode string `json:"updateMode"`
			} `json:"publishDirectives"`
		} `json:"message"`
	}
	json.Unmarshal(pushed[0], &body)
	if len(body.Message.PublishDirectives) != 1 || body.Message.PublishDirectives[0].UpdateMode != "FULL" {
		t.Fatalf("pushed directive = %+v, want updateMode FULL", body.Message.PublishDirectives)
	}

	// Second index pass: unchanged version -> nothing new enqueued.
	eng.indexPass(ctx)
	if d, _ := s.QueueDepth(ctx); d != 0 {
		t.Fatalf("unchanged index re-enqueued work: depth %d, want 0", d)
	}
}

// recMetrics records engine metric calls for assertions.
type recMetrics struct {
	pushed int
	failed int
	depth  int
}

func (m *recMetrics) CatalogPushed()              { m.pushed++ }
func (m *recMetrics) CatalogFailed(string)        { m.failed++ }
func (m *recMetrics) SetQueueDepth(n int)         { m.depth = n }
func (m *recMetrics) ObservePushSeconds(float64)  {}
func (m *recMetrics) ObserveIndexSeconds(float64) {}

// A 304 Not Modified from the index host: the engine echoes the stored ETag,
// enqueues nothing, keeps the version, and only advances the crawl cadence.
func TestCrawlIndex_NotModified_TouchesCadence(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	url := "https://x/i.json"

	// Seed a prior crawl so there is a stored validator to send and the row is due.
	if err := s.UpsertIndex(ctx, url, "p", "config", 5, "ok", time.Now().Add(-time.Hour), "etag5", ""); err != nil {
		t.Fatal(err)
	}

	sawCond := ""
	eng := New(Config{
		MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour,
	}, Deps{
		Store:  s,
		Source: NewConfigSource([]string{url}),
		FetchIndex: func(_ context.Context, _ string, cond IndexCond) (IndexResult, error) {
			sawCond = cond.ETag
			return IndexResult{NotModified: true}, nil
		},
		FetchFile: func(_ context.Context, f FileEntry) ([]byte, error) { return nil, nil },
		Push: func(context.Context, []byte) (PartOutcome, error) {
			return PartOutcome{Acked: true, HTTPStatus: 200}, nil
		},
		Metrics: &recMetrics{},
		NewID:   func() string { return "id" },
	})

	eng.indexPass(ctx)

	if sawCond != "etag5" {
		t.Fatalf("engine must send the stored ETag as the conditional validator, got %q", sawCond)
	}
	if d, _ := s.QueueDepth(ctx); d != 0 {
		t.Fatalf("304 must enqueue nothing, got queue depth %d", d)
	}
	got, err := s.GetIndex(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if got.IndexVersion != 5 {
		t.Fatalf("version must stay 5 on 304, got %d", got.IndexVersion)
	}
	if !got.NextCrawlAt.After(time.Now()) {
		t.Fatalf("304 must advance next_crawl_at, got %v", got.NextCrawlAt)
	}
}
