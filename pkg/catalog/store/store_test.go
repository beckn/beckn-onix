package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalog/store"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// fakeBlobStore is an in-memory definition.CatalogBlobStore, standing in
// for any real backend (local disk, S3, GCS, ...) -- store.Store is
// tested entirely against this fake, proving its assembly logic never
// depends on backend specifics.
type fakeBlobStore struct{ data map[string][]byte }

func newFakeBlobStore() *fakeBlobStore { return &fakeBlobStore{data: map[string][]byte{}} }

func (f *fakeBlobStore) Get(ctx context.Context, path string) ([]byte, error) {
	b, ok := f.data[path]
	if !ok {
		return nil, definition.ErrBlobNotFound
	}
	return b, nil
}

func (f *fakeBlobStore) Put(ctx context.Context, path string, content []byte) error {
	f.data[path] = append([]byte{}, content...)
	return nil
}

func baselineFile(t *testing.T, catalogID string, catalog json.RawMessage, nextUpdate time.Time) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"catalogId":   catalogID,
		"version":     1,
		"next_update": nextUpdate,
		"catalog":     catalog,
	})
	if err != nil {
		t.Fatalf("marshaling baseline file: %v", err)
	}
	return raw
}

func TestPublishThenLoad_RoundTripsBaseline(t *testing.T) {
	blobs := newFakeBlobStore()
	s := store.New(blobs)
	ctx := context.Background()

	catalog := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	content := baselineFile(t, "example.test/CAT-1", catalog, time.Now().Add(24*time.Hour))
	entry := json.RawMessage(`{"catalogId":"example.test/CAT-1","entryVersion":1,"catalogType":"REGULAR","isActive":true,"baseline":{"version":1,"url":"https://example.test/catalogs/CAT-1.v1.json","size":1,"digest":"sha-256:x"}}`)

	err := s.Publish(ctx, store.PublishRequest{
		NodeID: "example.test",
		Updates: []store.CatalogUpdate{{
			CatalogID:   "example.test/CAT-1",
			SignedEntry: entry,
			Baseline:    &store.FileWrite{Version: 1, Content: content},
		}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	states, err := s.LoadCatalogs(ctx, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("LoadCatalogs: %v", err)
	}
	state, ok := states["example.test/CAT-1"]
	if !ok {
		t.Fatal("expected state for example.test/CAT-1")
	}
	if string(state.Catalog) != string(catalog) {
		t.Errorf("reconstructed catalog mismatch:\ngot:  %s\nwant: %s", state.Catalog, catalog)
	}
	if state.BaselineFile == nil || state.BaselineFile.Version != 1 {
		t.Errorf("unexpected baseline file ref: %+v", state.BaselineFile)
	}
	if state.EntryVersion != 1 {
		t.Errorf("EntryVersion = %d, want 1", state.EntryVersion)
	}
}

func TestLoad_NoPriorIndex_ReturnsEmptyResult(t *testing.T) {
	s := store.New(newFakeBlobStore())
	states, err := s.LoadCatalogs(context.Background(), []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("LoadCatalogs: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected empty result, got %+v", states)
	}
}

func TestLoad_RetiredEntry_HasNoPublishableState(t *testing.T) {
	blobs := newFakeBlobStore()
	s := store.New(blobs)
	ctx := context.Background()

	entry := json.RawMessage(`{"catalogId":"example.test/CAT-1","entryVersion":2,"catalogType":"REGULAR","retiredAt":"2026-01-01T00:00:00Z"}`)
	if err := s.Publish(ctx, store.PublishRequest{
		NodeID:      "example.test",
		Retirements: []store.CatalogUpdate{{CatalogID: "example.test/CAT-1", SignedEntry: entry}},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	states, err := s.LoadCatalogs(ctx, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("LoadCatalogs: %v", err)
	}
	if _, ok := states["example.test/CAT-1"]; ok {
		t.Error("expected no state for a retired catalog")
	}
}

func TestPublish_PreservesUntouchedEntries(t *testing.T) {
	blobs := newFakeBlobStore()
	s := store.New(blobs)
	ctx := context.Background()

	catalog1 := json.RawMessage(`{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}`)
	entry1 := json.RawMessage(`{"catalogId":"example.test/CAT-1","entryVersion":1,"catalogType":"REGULAR","isActive":true,"baseline":{"version":1,"url":"https://example.test/catalogs/CAT-1.v1.json","size":1,"digest":"sha-256:x"}}`)
	if err := s.Publish(ctx, store.PublishRequest{
		NodeID: "example.test",
		Updates: []store.CatalogUpdate{{
			CatalogID: "example.test/CAT-1", SignedEntry: entry1,
			Baseline: &store.FileWrite{Version: 1, Content: baselineFile(t, "example.test/CAT-1", catalog1, time.Now().Add(time.Hour))},
		}},
	}); err != nil {
		t.Fatalf("Publish CAT-1: %v", err)
	}

	catalog2 := json.RawMessage(`{"id":"CAT-2","descriptor":{},"provider":{},"resources":[]}`)
	entry2 := json.RawMessage(`{"catalogId":"example.test/CAT-2","entryVersion":1,"catalogType":"REGULAR","isActive":true,"baseline":{"version":1,"url":"https://example.test/catalogs/CAT-2.v1.json","size":1,"digest":"sha-256:y"}}`)
	if err := s.Publish(ctx, store.PublishRequest{
		NodeID: "example.test",
		Updates: []store.CatalogUpdate{{
			CatalogID: "example.test/CAT-2", SignedEntry: entry2,
			Baseline: &store.FileWrite{Version: 1, Content: baselineFile(t, "example.test/CAT-2", catalog2, time.Now().Add(time.Hour))},
		}},
	}); err != nil {
		t.Fatalf("Publish CAT-2: %v", err)
	}

	raw, err := blobs.Get(ctx, store.IndexPath())
	if err != nil {
		t.Fatalf("reading index: %v", err)
	}
	var doc struct {
		Catalogs []json.RawMessage `json:"catalogs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	if len(doc.Catalogs) != 2 {
		t.Fatalf("expected 2 entries after the second publish, got %d", len(doc.Catalogs))
	}

	states, err := s.LoadCatalogs(ctx, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("LoadCatalogs: %v", err)
	}
	if _, ok := states["example.test/CAT-1"]; !ok {
		t.Error("expected CAT-1's state to survive an unrelated publish for CAT-2")
	}
}

func TestLoad_AppliesChangeFileOntoBaseline(t *testing.T) {
	blobs := newFakeBlobStore()
	s := store.New(blobs)
	ctx := context.Background()

	catalog := json.RawMessage(`{"id":"CAT-1","descriptor":{},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	baseline := &store.FileWrite{Version: 1, Content: baselineFile(t, "example.test/CAT-1", catalog, time.Now().Add(24*time.Hour))}
	entry1 := json.RawMessage(`{"catalogId":"example.test/CAT-1","entryVersion":1,"catalogType":"REGULAR","isActive":true,"baseline":{"version":1,"url":"https://example.test/catalogs/CAT-1.v1.json","size":1,"digest":"sha-256:x"}}`)
	if err := s.Publish(ctx, store.PublishRequest{
		NodeID:  "example.test",
		Updates: []store.CatalogUpdate{{CatalogID: "example.test/CAT-1", SignedEntry: entry1, Baseline: baseline}},
	}); err != nil {
		t.Fatalf("Publish baseline: %v", err)
	}

	change := json.RawMessage(`{"catalogId":"example.test/CAT-1","fromVersion":1,"toVersion":2,"next_update":"2099-01-01T00:00:00Z","resources":{"upserts":[{"id":"ITEM-2","descriptor":{"name":"two"}}]},"offers":{}}`)
	entry2 := json.RawMessage(`{"catalogId":"example.test/CAT-1","entryVersion":2,"catalogType":"REGULAR","isActive":true,"baseline":{"version":1,"url":"https://example.test/catalogs/CAT-1.v1.json","size":1,"digest":"sha-256:x"},"changes":[{"fromVersion":1,"toVersion":2,"url":"https://example.test/catalogs/changes/CAT-1.v2.changes.json","size":1,"digest":"sha-256:y"}]}`)
	if err := s.Publish(ctx, store.PublishRequest{
		NodeID:  "example.test",
		Updates: []store.CatalogUpdate{{CatalogID: "example.test/CAT-1", SignedEntry: entry2, Change: &store.FileWrite{Version: 2, Content: change}}},
	}); err != nil {
		t.Fatalf("Publish change: %v", err)
	}

	states, err := s.LoadCatalogs(ctx, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("LoadCatalogs: %v", err)
	}
	state, ok := states["example.test/CAT-1"]
	if !ok {
		t.Fatal("expected state for example.test/CAT-1")
	}
	var effective struct {
		Resources []json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(state.Catalog, &effective); err != nil {
		t.Fatalf("parsing reconstructed catalog: %v", err)
	}
	if len(effective.Resources) != 2 {
		t.Fatalf("expected 2 resources after applying change, got %d", len(effective.Resources))
	}
	if len(state.ChangeFiles) != 1 || state.ChangeFiles[0].FromVersion != 1 || state.ChangeFiles[0].Version != 2 {
		t.Errorf("unexpected change file refs: %+v", state.ChangeFiles)
	}
}

func TestPublish_GzipSuffixAndServedContent(t *testing.T) {
	blobs := newFakeBlobStore()
	s := store.New(blobs)
	ctx := context.Background()

	served := []byte("compressed-bytes")
	err := s.Publish(ctx, store.PublishRequest{
		NodeID: "example.test",
		Updates: []store.CatalogUpdate{{
			CatalogID:   "example.test/CAT-1",
			SignedEntry: json.RawMessage(`{"catalogId":"example.test/CAT-1"}`),
			Baseline: &store.FileWrite{
				Version:       1,
				Content:       json.RawMessage(`{"catalogId":"example.test/CAT-1"}`),
				ServedContent: served,
				Compressed:    true,
			},
		}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	path := store.CatalogFilePath("example.test/CAT-1", 1, "json", true)
	got, err := blobs.Get(ctx, path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(got) != string(served) {
		t.Errorf("got %q, want %q", got, served)
	}
}

func TestPublish_ChangeMode_UsesChangesPath(t *testing.T) {
	blobs := newFakeBlobStore()
	s := store.New(blobs)
	ctx := context.Background()

	err := s.Publish(ctx, store.PublishRequest{
		NodeID: "example.test",
		Updates: []store.CatalogUpdate{{
			CatalogID:   "example.test/CAT-1",
			SignedEntry: json.RawMessage(`{"catalogId":"example.test/CAT-1"}`),
			Change:      &store.FileWrite{Version: 2, Content: json.RawMessage(`{"catalogId":"example.test/CAT-1"}`)},
		}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	path := store.CatalogFilePath("example.test/CAT-1", 2, "changes.json", false)
	if _, err := blobs.Get(ctx, path); err != nil {
		t.Errorf("expected change file at %s: %v", path, err)
	}
}

func TestPublish_RetiredLatest_WritesFinalTombstone(t *testing.T) {
	blobs := newFakeBlobStore()
	s := store.New(blobs)
	ctx := context.Background()

	err := s.Publish(ctx, store.PublishRequest{
		NodeID: "example.test",
		Retirements: []store.CatalogUpdate{{
			CatalogID:   "example.test/CAT-1",
			SignedEntry: json.RawMessage(`{"catalogId":"example.test/CAT-1","retiredAt":"2026-01-01T00:00:00Z"}`),
			Latest:      &store.FileWrite{Content: json.RawMessage(`{"catalogId":"example.test/CAT-1","retiredAt":"2026-01-01T00:00:00Z"}`)},
		}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	path := store.LatestFilePath("example.test/CAT-1", false)
	if _, err := blobs.Get(ctx, path); err != nil {
		t.Errorf("expected latest tombstone at %s: %v", path, err)
	}
}
