package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
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

// testStore connects to CRAWLER_TEST_DB_DSN in a state-only schema, migrates,
// and truncates for a clean slate. Tests skip when no DSN is set.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("CRAWLER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("set CRAWLER_TEST_DB_DSN to run state tests")
	}
	db, err := Open(withSchema(dsn, "cc_test_store"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS cc_test_store"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, "TRUNCATE crawler_queue, crawler_catalog, crawler_index"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return New(db)
}

func TestIndexState(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	got, err := s.GetIndex(ctx, "https://x/index.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("absent index should be nil, got %+v", got)
	}

	if err := s.UpsertIndex(ctx, "https://x/index.json", "p", "config", 42, "ok", time.Now().Add(time.Minute), `W/"42"`, "Wed, 21 Oct 2026 07:28:00 GMT"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetIndex(ctx, "https://x/index.json")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.IndexVersion != 42 || got.SyncStatus != "ok" {
		t.Fatalf("GetIndex = %+v, want {42 ok}", got)
	}
	if got.ETag != `W/"42"` || got.LastModified != "Wed, 21 Oct 2026 07:28:00 GMT" {
		t.Fatalf("GetIndex validators = %q / %q, want the stored ETag/Last-Modified", got.ETag, got.LastModified)
	}

	if err := s.UpsertIndex(ctx, "https://x/index.json", "p", "config", 43, "partial", time.Now(), `W/"43"`, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetIndex(ctx, "https://x/index.json")
	if got.IndexVersion != 43 || got.SyncStatus != "partial" || got.ETag != `W/"43"` {
		t.Fatalf("after re-upsert = %+v, want {43 partial W/\"43\"}", got)
	}
}

// AdvanceIndexCadence is the 304-Not-Modified path: bump the crawl cadence without
// changing the version or the stored validators.
func TestAdvanceIndexCadence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	url := "https://x/index.json"
	must(t, s.UpsertIndex(ctx, url, "p", "config", 7, "ok", time.Now().Add(-time.Hour), "etag-7", "lm-7"))

	later := time.Now().Add(30 * time.Minute)
	must(t, s.AdvanceIndexCadence(ctx, url, later))

	got, err := s.GetIndex(ctx, url)
	must(t, err)
	if got.IndexVersion != 7 || got.ETag != "etag-7" || got.LastModified != "lm-7" {
		t.Fatalf("AdvanceIndexCadence must preserve version + validators, got %+v", got)
	}
	if !got.NextCrawlAt.After(time.Now()) {
		t.Fatalf("AdvanceIndexCadence must advance next_crawl_at, got %v", got.NextCrawlAt)
	}
}

// push_status is a bounded, chronological history: each settle appends one
// pass record; only the most recent passHistoryCap survive.
func TestPassReportHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	cid := "p/hist"

	for i := 1; i <= passHistoryCap+5; i++ {
		must(t, s.UpsertCatalog(ctx, CatalogState{
			CatalogID: cid, IndexURL: "i", Version: int64(i), Status: "active",
			Report: PassReport{ToVersion: int64(i), Mode: "FULL", Resources: i, Outcome: "pushed"},
		}))
	}

	reports, err := s.GetCatalogReports(ctx, cid)
	must(t, err)
	if len(reports) != passHistoryCap {
		t.Fatalf("history len = %d, want %d (capped)", len(reports), passHistoryCap)
	}
	// Oldest surviving is pass 6 (25-20+1); newest is 25; chronological order.
	if reports[0].ToVersion != 6 || reports[len(reports)-1].ToVersion != int64(passHistoryCap+5) {
		t.Fatalf("window = [%d..%d], want [6..%d]", reports[0].ToVersion, reports[len(reports)-1].ToVersion, passHistoryCap+5)
	}
	if newest := reports[len(reports)-1]; newest.Resources != passHistoryCap+5 || newest.Mode != "FULL" {
		t.Fatalf("newest = %+v, want resources %d mode FULL", newest, passHistoryCap+5)
	}
}

// RecordFailure appends a pass record too, preserving partial detail
// (batchesAcked < batchesTotal) without advancing the version cursor.
func TestPassReportRecordsPartial(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	cid := "p/partial"

	must(t, s.RecordFailure(ctx, cid, "i", "", PassReport{
		ToVersion: 3, Mode: "FULL", Resources: 10, Offers: 2,
		BatchesAcked: 1, BatchesTotal: 3, Outcome: "partial",
		HTTPStatus: 500, Reason: "push: boom",
	}))

	reports, err := s.GetCatalogReports(ctx, cid)
	must(t, err)
	if len(reports) != 1 {
		t.Fatalf("want 1 report, got %d", len(reports))
	}
	if r := reports[0]; r.Outcome != "partial" || r.BatchesAcked != 1 || r.BatchesTotal != 3 || r.Resources != 10 {
		t.Fatalf("partial report = %+v, want partial 1/3 resources 10", r)
	}
	// Failure must NOT advance the cursor.
	if _, seen, _ := s.GetCatalogVersion(ctx, cid); !seen {
		t.Fatal("row should exist after RecordFailure")
	}
	if v, _, _ := s.GetCatalogVersion(ctx, cid); v != 0 {
		t.Fatalf("version = %d, want 0 (failure must not advance cursor)", v)
	}
}

func TestCatalogCursor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if _, seen, err := s.GetCatalogVersion(ctx, "p/c"); err != nil {
		t.Fatal(err)
	} else if seen {
		t.Fatal("unseen catalog reported as seen")
	}

	if err := s.UpsertCatalog(ctx, CatalogState{
		CatalogID: "p/c", IndexURL: "https://x/index.json", ParticipantID: "p",
		Version: 42, Status: "active", Report: PassReport{Outcome: "pushed"},
	}); err != nil {
		t.Fatal(err)
	}

	v, seen, err := s.GetCatalogVersion(ctx, "p/c")
	if err != nil {
		t.Fatal(err)
	}
	if !seen || v != 42 {
		t.Fatalf("cursor = %d seen=%v, want 42 true", v, seen)
	}
}
