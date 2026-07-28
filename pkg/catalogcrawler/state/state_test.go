package state

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
	db, err := Open(withSchema(dsn, "cc_test_state"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS cc_test_state"); err != nil {
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

// TouchIndex is the 304-Not-Modified path: bump the crawl cadence without
// changing the version or the stored validators.
func TestTouchIndex(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	url := "https://x/index.json"
	must(t, s.UpsertIndex(ctx, url, "p", "config", 7, "ok", time.Now().Add(-time.Hour), "etag-7", "lm-7"))

	later := time.Now().Add(30 * time.Minute)
	must(t, s.TouchIndex(ctx, url, later))

	got, err := s.GetIndex(ctx, url)
	must(t, err)
	if got.IndexVersion != 7 || got.ETag != "etag-7" || got.LastModified != "lm-7" {
		t.Fatalf("TouchIndex must preserve version + validators, got %+v", got)
	}
	if !got.NextCrawlAt.After(time.Now()) {
		t.Fatalf("TouchIndex must advance next_crawl_at, got %v", got.NextCrawlAt)
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
		Version: 42, Status: "active", PushStatus: "pushed",
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
