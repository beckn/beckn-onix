package catalogcrawler

import (
	"encoding/json"
	"reflect"
	"testing"
)

func resourceIDs(t *testing.T, catalog []byte) []string {
	t.Helper()
	var doc struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(catalog, &doc); err != nil {
		t.Fatalf("unmarshal resolved catalog: %v", err)
	}
	ids := make([]string, 0, len(doc.Resources))
	for _, r := range doc.Resources {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestResolve(t *testing.T) {
	files := map[string][]byte{
		"base": []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"p"},"resources":[{"id":"r1"},{"id":"r2"}]}`),
		"v41":  []byte(`{"catalogId":"p/c","fromVersion":40,"toVersion":41,"resources":{"upserts":[{"id":"r2","descriptor":{"name":"R2new"}}]},"offers":{}}`),
		"v42":  []byte(`{"catalogId":"p/c","fromVersion":41,"toVersion":42,"resources":{"upserts":[{"id":"r3"}],"removals":["r1"]},"offers":{}}`),
	}
	entry := CatalogEntry{
		CatalogID: "p/c", Status: StatusActive,
		Baseline: FileEntry{Version: 40, URL: "base", Digest: "d"},
		Changes:  []FileEntry{{Version: 41, URL: "v41", Digest: "d"}, {Version: 42, URL: "v42", Digest: "d"}},
	}
	fetch := func(f FileEntry) ([]byte, error) { return files[f.URL], nil }

	t.Run("folds baseline + all changes to latest", func(t *testing.T) {
		got, err := Resolve(entry, 42, fetch)
		if err != nil {
			t.Fatal(err)
		}
		// r1 removed at v42, r2 updated at v41, r3 added at v42.
		if ids := resourceIDs(t, got); !reflect.DeepEqual(ids, []string{"r2", "r3"}) {
			t.Fatalf("resources = %v, want [r2 r3]", ids)
		}
	})

	t.Run("stops at toVersion", func(t *testing.T) {
		got, err := Resolve(entry, 41, fetch)
		if err != nil {
			t.Fatal(err)
		}
		// only v41 applied: r1 still present, r2 updated, no r3.
		if ids := resourceIDs(t, got); !reflect.DeepEqual(ids, []string{"r1", "r2"}) {
			t.Fatalf("resources = %v, want [r1 r2]", ids)
		}
	})

	t.Run("baseline only when no changes in range", func(t *testing.T) {
		got, err := Resolve(entry, 40, fetch)
		if err != nil {
			t.Fatal(err)
		}
		if ids := resourceIDs(t, got); !reflect.DeepEqual(ids, []string{"r1", "r2"}) {
			t.Fatalf("resources = %v, want [r1 r2]", ids)
		}
	})
}

func TestResolveWithChangeset(t *testing.T) {
	files := map[string][]byte{
		"base": []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"p"},"resources":[{"id":"r1"},{"id":"r2"}]}`),
		"v41":  []byte(`{"catalogId":"p/c","fromVersion":40,"toVersion":41,"resources":{"upserts":[{"id":"r2","descriptor":{"name":"R2new"}}]},"offers":{}}`),
		"v42":  []byte(`{"catalogId":"p/c","fromVersion":41,"toVersion":42,"resources":{"upserts":[{"id":"r3"}],"removals":["r1"]},"offers":{}}`),
	}
	entry := CatalogEntry{
		CatalogID: "p/c", Status: StatusActive,
		Baseline: FileEntry{Version: 40, URL: "base"},
		Changes:  []FileEntry{{Version: 41, URL: "v41"}, {Version: 42, URL: "v42"}},
	}
	fetch := func(f FileEntry) ([]byte, error) { return files[f.URL], nil }

	t.Run("incremental upsert-only", func(t *testing.T) {
		full, cs, err := ResolveWithChangeset(entry, 40, true, 41, fetch)
		if err != nil {
			t.Fatal(err)
		}
		if cs.FromBaseline || cs.HasRemovals {
			t.Fatalf("cs = %+v, want incremental upsert-only", cs)
		}
		if !cs.UpsertedResources["r2"] || len(cs.UpsertedResources) != 1 {
			t.Fatalf("upserted = %v, want {r2}", cs.UpsertedResources)
		}
		if ids := resourceIDs(t, full); !reflect.DeepEqual(ids, []string{"r1", "r2"}) {
			t.Fatalf("full = %v", ids)
		}
	})

	t.Run("change with removal", func(t *testing.T) {
		_, cs, err := ResolveWithChangeset(entry, 41, true, 42, fetch)
		if err != nil {
			t.Fatal(err)
		}
		if !cs.HasRemovals {
			t.Fatal("want HasRemovals true (v42 removes r1)")
		}
		if !cs.UpsertedResources["r3"] {
			t.Fatalf("upserted = %v, want r3", cs.UpsertedResources)
		}
	})

	t.Run("new catalog -> from baseline", func(t *testing.T) {
		if _, cs, err := ResolveWithChangeset(entry, 0, false, 42, fetch); err != nil {
			t.Fatal(err)
		} else if !cs.FromBaseline {
			t.Fatal("new catalog must be FromBaseline")
		}
	})

	t.Run("behind baseline -> from baseline", func(t *testing.T) {
		if _, cs, err := ResolveWithChangeset(entry, 30, true, 42, fetch); err != nil {
			t.Fatal(err)
		} else if !cs.FromBaseline {
			t.Fatal("cursor < baseline.version must be FromBaseline")
		}
	})
}

func TestFilterCatalog(t *testing.T) {
	catalog := []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"p"},"resources":[{"id":"r1"},{"id":"r2"},{"id":"r3"}]}`)
	out, err := filterCatalog(catalog, map[string]bool{"r2": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids := resourceIDs(t, out); !reflect.DeepEqual(ids, []string{"r2"}) {
		t.Fatalf("filtered = %v, want [r2]", ids)
	}
	var doc struct {
		Descriptor json.RawMessage `json:"descriptor"`
	}
	json.Unmarshal(out, &doc)
	if len(doc.Descriptor) == 0 {
		t.Fatal("descriptor (metadata) must be preserved")
	}
}
