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
