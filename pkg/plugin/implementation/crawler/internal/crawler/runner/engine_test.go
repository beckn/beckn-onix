package runner

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/source"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/store"
)

// resourceIDs extracts resources[].id from a catalog doc (delta assertions).
func resourceIDs(t *testing.T, catalogDoc []byte) []string {
	t.Helper()
	var doc struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(catalogDoc, &doc); err != nil {
		t.Fatalf("unmarshal catalog: %v", err)
	}
	ids := make([]string, 0, len(doc.Resources))
	for _, r := range doc.Resources {
		ids = append(ids, r.ID)
	}
	return ids
}

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
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("CRAWLER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("set CRAWLER_TEST_DB_DSN to run engine tests")
	}
	db, err := store.Open(withSchema(dsn, "cc_test_engine"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS cc_test_engine"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, "TRUNCATE crawler_queue, crawler_catalog, crawler_index"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return store.New(db)
}

func TestEngine_IndexThenCatalogPass(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	entry := catalog.CatalogEntry{
		CatalogID: "p/c", Status: catalog.StatusActive, // public
		Baseline: catalog.FileEntry{Version: 1, URL: "base", Digest: "d"},
	}
	idx := catalog.Index{ParticipantID: "p", Version: 1, Catalogs: []catalog.CatalogEntry{entry}}
	files := map[string][]byte{"base": []byte(`{"id":"p/c","resources":[{"id":"r1"}]}`)}

	var pushed [][]byte
	rec := &recMetrics{}
	eng := New(EngineConfig{
		MaxAttempts:   3,
		IndexInterval: time.Hour, CatalogInterval: time.Hour,
	}, Deps{
		Store:  s,
		Source: source.NewConfigSource([]string{"https://x/i.json"}),
		FetchIndex: func(context.Context, string, catalog.IndexConditions) (catalog.IndexResult, error) {
			return catalog.IndexResult{Index: idx}, nil
		},
		FetchFile: func(_ context.Context, f catalog.FileEntry) ([]byte, error) { return files[f.URL], nil },
		Validate:  func(context.Context, []byte) error { return nil },
		Push: func(_ context.Context, body []byte) (publish.BatchOutcome, error) {
			pushed = append(pushed, body)
			return publish.BatchOutcome{Acked: true, HTTPStatus: 200}, nil
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

// recMetrics records engine metric calls for assertions. pushed/failed are
// derived from RecordSyncOutcome by outcome (pushed vs faulted/partial);
// passSuccess counts MarkPassSuccess per job (the liveness signal).
type recMetrics struct {
	pushed      int
	failed      int
	depth       int
	passSuccess map[string]int
}

func (m *recMetrics) RecordSyncOutcome(outcome, _ string) {
	switch outcome {
	case "pushed":
		m.pushed++
	case "faulted", "partial":
		m.failed++
	}
}
func (m *recMetrics) MarkPassSuccess(job string) {
	if m.passSuccess == nil {
		m.passSuccess = map[string]int{}
	}
	m.passSuccess[job]++
}
func (m *recMetrics) SetQueueDepth(n int)           { m.depth = n }
func (m *recMetrics) SetCatalogsParked(int)         {}
func (m *recMetrics) SetCatalogsTracked(int)        {}
func (m *recMetrics) ObservePushSeconds(float64)    {}
func (m *recMetrics) ObserveIndexSeconds(float64)   {}
func (m *recMetrics) ObserveSyncLagSeconds(float64) {}
func (m *recMetrics) RecordIndexPoll(string)        {}

// A store failure inside the sync loop must NOT mark the sync pass successful.
// MarkPassSuccess drives crawler_seconds_since_last_success, the only input to
// the "crawler wedged" alert — so if the queue can't even be claimed from, the
// liveness gauge has to go stale rather than reset every tick. indexPass gets
// this right by returning on a Source error; catalogPass must match.
//
// A drained queue is the opposite case and is covered by the passes above: an
// idle tick IS a successful tick, and must keep marking success.
func TestCatalogPass_ClaimFailure_WithholdsPassSuccess(t *testing.T) {
	dsn := os.Getenv("CRAWLER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("set CRAWLER_TEST_DB_DSN to run engine tests")
	}
	db, err := store.Open(withSchema(dsn, "cc_test_engine"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS cc_test_engine"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rec := &recMetrics{}
	eng := New(EngineConfig{MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour}, Deps{
		Store:   store.New(db),
		Source:  source.NewConfigSource(nil),
		Metrics: rec,
		NewID:   func() string { return "id" },
	})

	// Close the pool so the first ClaimNext fails the way a real DB outage does.
	db.Close()

	eng.catalogPass(ctx)

	if n := rec.passSuccess["sync"]; n != 0 {
		t.Fatalf("MarkPassSuccess(\"sync\") called %d time(s) after a ClaimNext failure; "+
			"a sync job that can't reach the queue must leave the liveness gauge stale", n)
	}
}

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
	eng := New(EngineConfig{
		MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour,
	}, Deps{
		Store:  s,
		Source: source.NewConfigSource([]string{url}),
		FetchIndex: func(_ context.Context, _ string, cond catalog.IndexConditions) (catalog.IndexResult, error) {
			sawCond = cond.ETag
			return catalog.IndexResult{NotModified: true}, nil
		},
		FetchFile: func(_ context.Context, f catalog.FileEntry) ([]byte, error) { return nil, nil },
		Push: func(context.Context, []byte) (publish.BatchOutcome, error) {
			return publish.BatchOutcome{Acked: true, HTTPStatus: 200}, nil
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

// MergeOnly incremental update: the push is a MERGE delta built straight from
// the change file (metadata envelope + upserts), and the baseline is NOT fetched.
func TestEngine_MergeOnly_DeltaPush(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	url := "https://x/i.json"

	entry := catalog.CatalogEntry{
		CatalogID: "p/c", Status: catalog.StatusActive,
		Baseline: catalog.FileEntry{Version: 1, URL: "base", Digest: "d"},
		Changes:  []catalog.FileEntry{{Version: 2, URL: "chg2", Digest: "d"}},
	}
	idx := catalog.Index{ParticipantID: "p", Version: 2, Catalogs: []catalog.CatalogEntry{entry}}
	files := map[string][]byte{
		"base": []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"},"resources":[{"id":"r1"}]}`),
		"chg2": []byte(`{"catalogId":"p/c","fromVersion":1,"toVersion":2,"catalog":{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"}},"resources":{"upserts":[{"id":"rNew","descriptor":{"name":"New"}}]},"offers":{}}`),
	}

	// Seed the cursor at the baseline version → the next pass is incremental.
	if err := s.UpsertCatalog(ctx, catalog.CatalogState{CatalogID: "p/c", IndexURL: url, Version: 1, Status: "active", Report: catalog.PassReport{Outcome: "pushed"}}); err != nil {
		t.Fatal(err)
	}

	var pushed [][]byte
	var fetched []string
	eng := New(EngineConfig{MergeOnly: true, MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour}, Deps{
		Store:  s,
		Source: source.NewConfigSource([]string{url}),
		FetchIndex: func(context.Context, string, catalog.IndexConditions) (catalog.IndexResult, error) {
			return catalog.IndexResult{Index: idx}, nil
		},
		FetchFile: func(_ context.Context, f catalog.FileEntry) ([]byte, error) {
			fetched = append(fetched, f.URL)
			return files[f.URL], nil
		},
		Push: func(_ context.Context, body []byte) (publish.BatchOutcome, error) {
			pushed = append(pushed, body)
			return publish.BatchOutcome{Acked: true, HTTPStatus: 200}, nil
		},
		Metrics: &recMetrics{},
		NewID:   func() string { return "id" },
	})

	eng.indexPass(ctx)
	eng.catalogPass(ctx)

	if len(pushed) != 1 {
		t.Fatalf("pushed %d times, want 1", len(pushed))
	}
	for _, u := range fetched {
		if u == "base" {
			t.Fatal("MergeOnly incremental must NOT fetch the baseline")
		}
	}
	var body struct {
		Message struct {
			Catalogs          []json.RawMessage `json:"catalogs"`
			PublishDirectives []struct {
				UpdateMode string `json:"updateMode"`
			} `json:"publishDirectives"`
		} `json:"message"`
	}
	json.Unmarshal(pushed[0], &body)
	if len(body.Message.PublishDirectives) != 1 || body.Message.PublishDirectives[0].UpdateMode != "MERGE" {
		t.Fatalf("directive = %+v, want MERGE", body.Message.PublishDirectives)
	}
	if ids := resourceIDs(t, body.Message.Catalogs[0]); len(ids) != 1 || ids[0] != "rNew" {
		t.Fatalf("pushed resources = %v, want [rNew] (delta only)", ids)
	}
	if v, _, _ := s.GetCatalogVersion(ctx, "p/c"); v != 2 {
		t.Fatalf("cursor = %d, want 2", v)
	}
}

// A /crawl'd index must join the recurring schedule: the scheduled index pass
// re-polls every index already recorded in crawler_index (incl. on-demand ones),
// not just the configured/registry refs — so a later change file is picked up
// with no second /crawl. Configured refs still crawl; persisted refs are added,
// deduped by URL.
func TestIndexPass_ReCrawlsPersistedIndexes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const configURL = "https://x/config-index.json"
	const onDemandURL = "https://y/on-demand-index.json"

	// A prior on-demand crawl left a persisted row, now due again (next_crawl_at in the past).
	if err := s.UpsertIndex(ctx, onDemandURL, "p2", source.KindOnDemand, 1, "ok",
		time.Now().Add(-time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	// The configured URL is ALSO persisted → it must be crawled once, not twice (deduped).
	if err := s.UpsertIndex(ctx, configURL, "p", source.KindConfig, 1, "ok",
		time.Now().Add(-time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}

	var fetched []string
	eng := New(EngineConfig{MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour}, Deps{
		Store:  s,
		Source: source.NewConfigSource([]string{configURL}),
		FetchIndex: func(_ context.Context, indexURL string, _ catalog.IndexConditions) (catalog.IndexResult, error) {
			fetched = append(fetched, indexURL)
			return catalog.IndexResult{Index: catalog.Index{ParticipantID: "p", Version: 1}}, nil
		},
		Metrics: &recMetrics{},
		NewID:   func() string { return "id" },
	})

	eng.indexPass(ctx)

	if !slices.Contains(fetched, configURL) {
		t.Errorf("configured index %q was not crawled; fetched=%v", configURL, fetched)
	}
	if !slices.Contains(fetched, onDemandURL) {
		t.Errorf("persisted on-demand index %q was not re-crawled by the scheduled pass; fetched=%v", onDemandURL, fetched)
	}
	if n := strings.Count(strings.Join(fetched, "\n"), configURL); n != 1 {
		t.Errorf("configured index crawled %d times, want 1 (must dedup against its persisted row); fetched=%v", n, fetched)
	}
}

// On an incremental update, a change file that omits the catalog metadata
// envelope is malformed — the crawler must NOT fall back to re-fetching the
// baseline. It is a permanent content fault: the baseline is not fetched,
// nothing is pushed, and the cursor does not advance (the item parks until the
// publisher republishes a compliant change file).
func TestEngine_MergeOnly_NoEnvelope_ParksNoBaselineFallback(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	url := "https://x/i.json"

	entry := catalog.CatalogEntry{
		CatalogID: "p/c", Status: catalog.StatusActive,
		Baseline: catalog.FileEntry{Version: 1, URL: "base", Digest: "d"},
		Changes:  []catalog.FileEntry{{Version: 2, URL: "chg2", Digest: "d"}},
	}
	idx := catalog.Index{ParticipantID: "p", Version: 2, Catalogs: []catalog.CatalogEntry{entry}}
	files := map[string][]byte{
		"base": []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"},"resources":[{"id":"r1"}]}`),
		// change file WITHOUT a "catalog" metadata envelope:
		"chg2": []byte(`{"catalogId":"p/c","fromVersion":1,"toVersion":2,"resources":{"upserts":[{"id":"rNew"}]},"offers":{}}`),
	}

	// Seed the cursor at the baseline version → the next pass is incremental.
	if err := s.UpsertCatalog(ctx, catalog.CatalogState{CatalogID: "p/c", IndexURL: url, Version: 1, Status: "active", Report: catalog.PassReport{Outcome: "pushed"}}); err != nil {
		t.Fatal(err)
	}

	var pushed [][]byte
	var fetched []string
	rec := &recMetrics{}
	eng := New(EngineConfig{MergeOnly: true, MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour}, Deps{
		Store:  s,
		Source: source.NewConfigSource([]string{url}),
		FetchIndex: func(context.Context, string, catalog.IndexConditions) (catalog.IndexResult, error) {
			return catalog.IndexResult{Index: idx}, nil
		},
		FetchFile: func(_ context.Context, f catalog.FileEntry) ([]byte, error) {
			fetched = append(fetched, f.URL)
			return files[f.URL], nil
		},
		Push: func(_ context.Context, body []byte) (publish.BatchOutcome, error) {
			pushed = append(pushed, body)
			return publish.BatchOutcome{Acked: true, HTTPStatus: 200}, nil
		},
		Metrics: rec,
		NewID:   func() string { return "id" },
	})

	eng.indexPass(ctx)
	eng.catalogPass(ctx)

	if slices.Contains(fetched, "base") {
		t.Errorf("must NOT fall back to the baseline when the change file lacks the envelope; fetched=%v", fetched)
	}
	if len(pushed) != 0 {
		t.Errorf("nothing should be pushed for a malformed change file; got %d push(es)", len(pushed))
	}
	if v, _, _ := s.GetCatalogVersion(ctx, "p/c"); v != 1 {
		t.Errorf("cursor must not advance on a permanent content fault; got %d, want 1", v)
	}
	if rec.failed != 1 {
		t.Errorf("expected 1 faulted outcome recorded, got %d", rec.failed)
	}
}
