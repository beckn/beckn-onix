package catalog

// resolve_test.go — tests for the composition logic: Resolve,
// ResolveWithChangeset, ResolveDelta, and FilterCatalog.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// ResolveDelta builds a MERGE payload from the change files alone (catalog
// metadata envelope + upserts), never fetching the baseline; removals are
// recorded but not applied.
func TestResolveDelta(t *testing.T) {
	env := `"catalog":{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"}}`
	files := map[string][]byte{
		"base": []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"},"resources":[{"id":"r1"},{"id":"r2"}]}`),
		"v2":   []byte(`{"catalogId":"p/c","fromVersion":1,"toVersion":2,` + env + `,"resources":{"upserts":[{"id":"r2","descriptor":{"name":"R2a"}}]},"offers":{}}`),
		"v3":   []byte(`{"catalogId":"p/c","fromVersion":2,"toVersion":3,` + env + `,"resources":{"upserts":[{"id":"r3"},{"id":"r2","descriptor":{"name":"R2b"}}],"removals":["r9"]},"offers":{}}`),
	}
	entry := CatalogEntry{
		CatalogID: "p/c",
		Baseline: FileEntry{Version: 1, URL: "base"},
		Changes:  []FileEntry{{FromVersion: 1, ToVersion: 2, URL: "v2"}, {FromVersion: 2, ToVersion: 3, URL: "v3"}},
	}
	var fetched []string
	fetch := func(f FileEntry) ([]byte, error) { fetched = append(fetched, f.URL); return files[f.URL], nil }

	doc, cs, ok, err := ResolveDelta(entry, 1, 3, fetch)
	if err != nil || !ok {
		t.Fatalf("ResolveDelta ok=%v err=%v", ok, err)
	}
	for _, u := range fetched {
		if u == "base" {
			t.Fatal("delta must NOT fetch the baseline")
		}
	}
	// Union of upserts, latest per id: r2 (from v3) + r3; r1 untouched (absent).
	if ids := resourceIDs(t, doc); !reflect.DeepEqual(ids, []string{"r2", "r3"}) {
		t.Fatalf("delta resources = %v, want [r2 r3]", ids)
	}
	if !bytes.Contains(doc, []byte("R2b")) || bytes.Contains(doc, []byte("R2a")) {
		t.Fatalf("delta must carry the latest r2 (R2b), got %s", doc)
	}
	if !bytes.Contains(doc, []byte(`"provider"`)) {
		t.Fatalf("delta must carry provider metadata, got %s", doc)
	}
	// Removal recorded in the changeset but NOT applied to the payload.
	if !cs.HasRemovals || cs.RemovedResources != 1 {
		t.Fatalf("cs removals = %+v, want HasRemovals + 1 removed", cs)
	}

	// No metadata envelope in the change file -> falls back to a one-time
	// baseline fetch for id/descriptor/provider only; the baseline's own
	// resources (r1, r2) must NOT appear, only the change file's upsert (r5).
	files["nometa"] = []byte(`{"catalogId":"p/c","fromVersion":1,"toVersion":2,"resources":{"upserts":[{"id":"r5"}]},"offers":{}}`)
	noMeta := CatalogEntry{Baseline: FileEntry{Version: 1, URL: "base"}, Changes: []FileEntry{{FromVersion: 1, ToVersion: 2, URL: "nometa"}}}
	doc2, _, ok2, err := ResolveDelta(noMeta, 1, 2, fetch)
	if err != nil || !ok2 {
		t.Fatalf("no-envelope fallback should succeed, got ok=%v err=%v", ok2, err)
	}
	if ids := resourceIDs(t, doc2); !reflect.DeepEqual(ids, []string{"r5"}) {
		t.Fatalf("no-envelope fallback resources = %v, want [r5] (not the baseline's r1/r2)", ids)
	}
	if !bytes.Contains(doc2, []byte(`"provider"`)) || !bytes.Contains(doc2, []byte(`"prov"`)) {
		t.Fatalf("no-envelope fallback must carry the baseline's provider metadata, got %s", doc2)
	}
}

// A change file legitimately carries zero resource/offer upserts while still
// patching a catalog-level attribute (e.g. isActive) via its "catalog" block
// -- a publisher toggling isActive alone produces exactly this minimal delta.
// The changeset must flag that as a real change (HasAttributeChange), not
// look identical to "nothing happened": that flag is what stops verifyContent
// from silently skipping the push and dropping the attribute change.
func TestResolveDelta_AttributeOnlyChangeIsFlagged(t *testing.T) {
	files := map[string][]byte{
		"base": []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"},"isActive":true,"resources":[{"id":"r1"}],"offers":[{"id":"o1"}]}`),
		"v5":   []byte(`{"catalogId":"p/c","fromVersion":4,"toVersion":5,"resources":{},"offers":{},"catalog":{"isActive":false}}`),
	}
	entry := CatalogEntry{
		CatalogID: "p/c",
		Baseline:  FileEntry{Version: 4, URL: "base"},
		Changes:   []FileEntry{{FromVersion: 4, ToVersion: 5, URL: "v5"}},
	}
	fetch := func(f FileEntry) ([]byte, error) { return files[f.URL], nil }

	doc, cs, ok, err := ResolveDelta(entry, 4, 5, fetch)
	if err != nil || !ok {
		t.Fatalf("ResolveDelta ok=%v err=%v", ok, err)
	}
	if !cs.HasAttributeChange {
		t.Fatal("expected HasAttributeChange=true for a catalog-only patch with zero upserts")
	}
	if len(cs.UpsertedResources) != 0 || len(cs.UpsertedOffers) != 0 {
		t.Fatalf("expected zero upserts, got resources=%v offers=%v", cs.UpsertedResources, cs.UpsertedOffers)
	}
	var got struct {
		IsActive *bool `json:"isActive"`
	}
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatal(err)
	}
	if got.IsActive == nil || *got.IsActive {
		t.Fatalf("isActive = %v, want false (from the change file's catalog block)", got.IsActive)
	}
}

// A change file's catalog-attribute envelope legitimately carries ONLY what
// changed (a publisher toggling isActive alone has no reason to repeat id/
// descriptor/provider), and this must NOT leave those fields null on the
// wire: id always comes from entry.CatalogID, and descriptor/provider must
// still be backfilled from the baseline when the envelope didn't supply them.
// This reproduces the exact reported failure: discovery-publish-job rejected
// a push with "Required field 'id' is missing or empty", catalogId=null.
func TestResolveDelta_AttributeOnlyEnvelopeStillCarriesID(t *testing.T) {
	files := map[string][]byte{
		"base": []byte(`{"id":"staging.p-node.fabric.nfh.global/CAT-GENERIC-001","descriptor":{"name":"Generic"},"provider":{"id":"prov"},"resources":[{"id":"r1"}],"offers":[{"id":"o1"}]}`),
		"v3":   []byte(`{"catalogId":"staging.p-node.fabric.nfh.global/CAT-GENERIC-001","fromVersion":2,"toVersion":3,"resources":{},"offers":{},"catalog":{"isActive":false}}`),
	}
	entry := CatalogEntry{
		CatalogID: "staging.p-node.fabric.nfh.global/CAT-GENERIC-001",
		Baseline:  FileEntry{Version: 2, URL: "base"},
		Changes:   []FileEntry{{FromVersion: 2, ToVersion: 3, URL: "v3"}},
	}
	fetch := func(f FileEntry) ([]byte, error) { return files[f.URL], nil }

	doc, _, ok, err := ResolveDelta(entry, 2, 3, fetch)
	if err != nil || !ok {
		t.Fatalf("ResolveDelta ok=%v err=%v", ok, err)
	}
	var got struct {
		ID         string          `json:"id"`
		Descriptor json.RawMessage `json:"descriptor"`
		Provider   json.RawMessage `json:"provider"`
		IsActive   *bool           `json:"isActive"`
	}
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != entry.CatalogID {
		t.Fatalf("id = %q, want %q (doc = %s)", got.ID, entry.CatalogID, doc)
	}
	if len(got.Descriptor) == 0 || string(got.Descriptor) == "null" {
		t.Fatalf("descriptor missing/null, want the baseline's, doc = %s", doc)
	}
	if len(got.Provider) == 0 || string(got.Provider) == "null" {
		t.Fatalf("provider missing/null, want the baseline's, doc = %s", doc)
	}
	if got.IsActive == nil || *got.IsActive {
		t.Fatalf("isActive = %v, want false (from the change file's envelope)", got.IsActive)
	}
}

// ResolveDelta must not lose an arbitrary catalog-level attribute (anything
// beyond descriptor/provider/isActive) named in a change file's envelope --
// catalogfile.Doc's Extra round-trip carries it through with no crawler-side
// change needed.
func TestResolveDelta_PreservesArbitraryEnvelopeField(t *testing.T) {
	files := map[string][]byte{
		"base": []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"},"resources":[{"id":"r1"}]}`),
		"v5": []byte(`{"catalogId":"p/c","fromVersion":4,"toVersion":5,"resources":{},"offers":{},` +
			`"catalog":{"validity":{"endDate":"2027-01-01T00:00:00Z"}}}`),
	}
	entry := CatalogEntry{
		CatalogID: "p/c",
		Baseline:  FileEntry{Version: 4, URL: "base"},
		Changes:   []FileEntry{{FromVersion: 4, ToVersion: 5, URL: "v5"}},
	}
	fetch := func(f FileEntry) ([]byte, error) { return files[f.URL], nil }

	doc, cs, ok, err := ResolveDelta(entry, 4, 5, fetch)
	if err != nil || !ok {
		t.Fatalf("ResolveDelta ok=%v err=%v", ok, err)
	}
	if !cs.HasAttributeChange {
		t.Fatal("expected HasAttributeChange=true")
	}
	var got struct {
		Validity struct {
			EndDate string `json:"endDate"`
		} `json:"validity"`
	}
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatal(err)
	}
	if got.Validity.EndDate != "2027-01-01T00:00:00Z" {
		t.Fatalf("validity.endDate = %q, want 2027-01-01T00:00:00Z (doc = %s)", got.Validity.EndDate, doc)
	}
}

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
		CatalogID: "p/c",
		Baseline: FileEntry{Version: 40, URL: "base", Digest: "d"},
		Changes:  []FileEntry{{FromVersion: 40, ToVersion: 41, URL: "v41", Digest: "d"}, {FromVersion: 41, ToVersion: 42, URL: "v42", Digest: "d"}},
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
		CatalogID: "p/c",
		Baseline: FileEntry{Version: 40, URL: "base"},
		Changes:  []FileEntry{{FromVersion: 40, ToVersion: 41, URL: "v41"}, {FromVersion: 41, ToVersion: 42, URL: "v42"}},
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

// ValidateCatalogDoc has to draw one line precisely: corrupt content (which
// cannot be counted, so it must never settle) versus a legitimately empty
// catalog (a real published state that settles as a clean skip).
func TestValidateCatalogDoc(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		// Valid, including the empty-catalog cases.
		{"catalog with resources", `{"id":"p/c","resources":[{"id":"r1"}]}`, false},
		{"empty resources array is a real empty catalog", `{"id":"p/c","resources":[]}`, false},
		{"null resources is an empty catalog", `{"id":"p/c","resources":null}`, false},
		{"offers-only catalog", `{"id":"p/c","offers":[{"id":"o1"}]}`, false},
		{"empty offers array with no resources key", `{"id":"p/c","offers":[]}`, false},
		{"metadata alongside an empty catalog", `{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"p"},"resources":[]}`, false},

		// Corrupt: cannot be counted, must park.
		{"not json at all", `{not json`, true},
		{"truncated json", `{"id":"p/c","resources":[{"id":`, true},
		{"top-level array is not a catalog", `[{"id":"r1"}]`, true},
		{"top-level string is not a catalog", `"catalog"`, true},
		{"empty bytes", ``, true},
		{"no resources and no offers container", `{"id":"p/c","descriptor":{"name":"C"}}`, true},
		{"resources is an object, not an array", `{"id":"p/c","resources":{"r1":{"id":"r1"}}}`, true},
		{"resources is a string", `{"id":"p/c","resources":"none"}`, true},
		{"offers is an object", `{"id":"p/c","resources":[],"offers":{"o1":{}}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCatalogDoc([]byte(tt.doc))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateCatalogDoc(%s) error = %v, wantErr = %v", tt.doc, err, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}
			if !IsPermanent(err) {
				t.Errorf("corrupt content must be a permanent fault, got %v", err)
			}
			if got := PermanentClass(err); got != FaultContentInvalid {
				t.Errorf("fault class = %q, want %q", got, FaultContentInvalid)
			}
		})
	}
}

// The baseline is the one file that can reach the push doc without ever being
// parsed (a first sync with no applicable change files returns it verbatim), so
// ResolveWithChangeset must validate it. A corrupt baseline is a permanent
// content_invalid fault; an empty one is a real state and resolves cleanly.
func TestResolveWithChangeset_BaselineShape(t *testing.T) {
	change := `{"catalogId":"p/c","fromVersion":40,"toVersion":41,"resources":{"upserts":[{"id":"r2"}]},"offers":{}}`
	tests := []struct {
		name       string
		baseline   string
		toVersion  int64
		wantFault  FaultClass // "" => must resolve without error
		wantResIDs []string
	}{
		{
			name: "valid baseline, no changes in range",
			baseline: `{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"p"},` +
				`"resources":[{"id":"r1"}]}`,
			toVersion: 40, wantResIDs: []string{"r1"},
		},
		{
			name:      "empty baseline resolves cleanly (a real empty catalog)",
			baseline:  `{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"p"},"resources":[]}`,
			toVersion: 40, wantResIDs: []string{},
		},
		{
			name:      "malformed json baseline parks as content_invalid",
			baseline:  `{"id":"p/c","resources":[{"id":"r1"}`,
			toVersion: 40, wantFault: FaultContentInvalid,
		},
		{
			name:      "baseline with a differently shaped resources field parks",
			baseline:  `{"id":"p/c","resources":{"r1":{"id":"r1"}}}`,
			toVersion: 40, wantFault: FaultContentInvalid,
		},
		{
			name:      "baseline with no resources or offers container parks",
			baseline:  `{"id":"p/c","descriptor":{"name":"C"}}`,
			toVersion: 40, wantFault: FaultContentInvalid,
		},
		{
			name:      "corrupt baseline parks even when a change file would be folded on top",
			baseline:  `{"id":"p/c","resources":[{"id":"r1"}`,
			toVersion: 41, wantFault: FaultContentInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string][]byte{"base": []byte(tt.baseline), "v41": []byte(change)}
			entry := CatalogEntry{
				CatalogID: "p/c",
				Baseline: FileEntry{Version: 40, URL: "base"},
				Changes:  []FileEntry{{FromVersion: 40, ToVersion: 41, URL: "v41"}},
			}
			fetch := func(f FileEntry) ([]byte, error) { return files[f.URL], nil }

			got, _, err := ResolveWithChangeset(entry, 0, false, tt.toVersion, fetch)
			if tt.wantFault == "" {
				if err != nil {
					t.Fatalf("ResolveWithChangeset error = %v, want nil", err)
				}
				if ids := resourceIDs(t, got); !reflect.DeepEqual(ids, tt.wantResIDs) {
					t.Fatalf("resources = %v, want %v", ids, tt.wantResIDs)
				}
				return
			}
			if err == nil {
				t.Fatalf("corrupt baseline resolved without error, doc = %s", got)
			}
			if !IsPermanent(err) {
				t.Fatalf("corrupt baseline must be permanent (park, not retry), got %v", err)
			}
			if c := PermanentClass(err); c != tt.wantFault {
				t.Fatalf("fault class = %q, want %q (err: %v)", c, tt.wantFault, err)
			}
		})
	}
}

// ResolveDelta composes from the change files alone, so it must enforce the same
// continuity ResolveWithChangeset enforces. Without it a missing intermediate
// version is silently dropped from the delta: the push succeeds, the cursor
// advances, and Discovery is divergent with nothing to signal it.
func TestResolveDelta_Continuity(t *testing.T) {
	env := `"catalog":{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"}}`
	files := map[string][]byte{
		"base": []byte(`{"id":"p/c","resources":[{"id":"r1"}]}`),
		"v6":   []byte(`{"catalogId":"p/c","fromVersion":5,"toVersion":6,` + env + `,"resources":{"upserts":[{"id":"r6"}]},"offers":{}}`),
		"v7":   []byte(`{"catalogId":"p/c","fromVersion":6,"toVersion":7,` + env + `,"resources":{"upserts":[{"id":"r7"}]},"offers":{}}`),
		"v8":   []byte(`{"catalogId":"p/c","fromVersion":7,"toVersion":8,` + env + `,"resources":{"upserts":[{"id":"r8"}]},"offers":{}}`),
		// One file covering 5..7 in a single step: contiguous with the cursor, so it
		// is legal even though no separate v6 file exists.
		"v7wide": []byte(`{"catalogId":"p/c","fromVersion":5,"toVersion":7,` + env + `,"resources":{"upserts":[{"id":"r7"}]},"offers":{}}`),
		// Starts before the cursor: the versions it claims to carry do not line up
		// with where we are.
		"v6early": []byte(`{"catalogId":"p/c","fromVersion":4,"toVersion":6,` + env + `,"resources":{"upserts":[{"id":"r6"}]},"offers":{}}`),
	}
	tests := []struct {
		name       string
		changes    []FileEntry
		wantFault  FaultClass // "" => must compose
		wantResIDs []string
	}{
		{
			name:       "contiguous delta composes every version in the range",
			changes:    []FileEntry{{FromVersion: 5, ToVersion: 6, URL: "v6"}, {FromVersion: 6, ToVersion: 7, URL: "v7"}, {FromVersion: 7, ToVersion: 8, URL: "v8"}},
			wantResIDs: []string{"r6", "r7", "r8"},
		},
		{
			name:       "a placeholder outside the range is ignored, not a gap",
			changes:    []FileEntry{{FromVersion: 5, ToVersion: 6, URL: "v6"}, {FromVersion: 6, ToVersion: 7, URL: "v7"}, {FromVersion: 7, ToVersion: 8, URL: "v8"}, {FromVersion: 8, ToVersion: 9, URL: ""}},
			wantResIDs: []string{"r6", "r7", "r8"},
		},
		{
			name:      "a missing intermediate version parks as a gap",
			changes:   []FileEntry{{FromVersion: 6, ToVersion: 7, URL: "v7"}, {FromVersion: 7, ToVersion: 8, URL: "v8"}},
			wantFault: FaultGap,
		},
		{
			name:      "a url-less placeholder inside the range is a gap, not a skip",
			changes:   []FileEntry{{FromVersion: 5, ToVersion: 6, URL: ""}, {FromVersion: 6, ToVersion: 7, URL: "v7"}, {FromVersion: 7, ToVersion: 8, URL: "v8"}},
			wantFault: FaultGap,
		},
		{
			name:       "one wide change file that starts at the cursor is contiguous",
			changes:    []FileEntry{{FromVersion: 5, ToVersion: 7, URL: "v7wide"}, {FromVersion: 7, ToVersion: 8, URL: "v8"}},
			wantResIDs: []string{"r7", "r8"},
		},
		{
			name:      "a first change file that starts before the cursor is a gap",
			changes:   []FileEntry{{FromVersion: 4, ToVersion: 6, URL: "v6early"}, {FromVersion: 6, ToVersion: 7, URL: "v7"}, {FromVersion: 7, ToVersion: 8, URL: "v8"}},
			wantFault: FaultGap,
		},
		{
			name:      "a hole in the middle of the fold is a gap",
			changes:   []FileEntry{{FromVersion: 5, ToVersion: 6, URL: "v6"}, {FromVersion: 7, ToVersion: 8, URL: "v8"}},
			wantFault: FaultGap,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := CatalogEntry{
				CatalogID: "p/c",
				Baseline: FileEntry{Version: 1, URL: "base"},
				Changes:  tt.changes,
			}
			var fetched []string
			fetch := func(f FileEntry) ([]byte, error) {
				fetched = append(fetched, f.URL)
				return files[f.URL], nil
			}

			doc, _, ok, err := ResolveDelta(entry, 5, 8, fetch)
			for _, u := range fetched {
				if u == "base" {
					t.Fatal("delta must NOT fetch the baseline")
				}
			}
			if tt.wantFault == "" {
				if err != nil || !ok {
					t.Fatalf("ResolveDelta ok=%v err=%v, want a composed delta", ok, err)
				}
				if ids := resourceIDs(t, doc); !reflect.DeepEqual(ids, tt.wantResIDs) {
					t.Fatalf("delta resources = %v, want %v", ids, tt.wantResIDs)
				}
				return
			}
			if err == nil {
				t.Fatalf("gap composed silently: ok=%v doc=%s", ok, doc)
			}
			if !IsPermanent(err) {
				t.Fatalf("a gap must be permanent (park, not retry), got %v", err)
			}
			if c := PermanentClass(err); c != tt.wantFault {
				t.Fatalf("fault class = %q, want %q (err: %v)", c, tt.wantFault, err)
			}
		})
	}
}

func TestFilterCatalog(t *testing.T) {
	catalog := []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"p"},"resources":[{"id":"r1"},{"id":"r2"},{"id":"r3"}]}`)
	out, err := FilterCatalog(catalog, map[string]bool{"r2": true}, nil)
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

func TestStampIsActive(t *testing.T) {
	catalog := []byte(`{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"p"},"resources":[]}`)

	t.Run("nil leaves the doc untouched", func(t *testing.T) {
		out, err := StampIsActive(catalog, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(catalog) {
			t.Fatalf("expected doc unchanged, got %s", out)
		}
	})

	t.Run("false is stamped onto the doc", func(t *testing.T) {
		active := false
		out, err := StampIsActive(catalog, &active)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			IsActive *bool `json:"isActive"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if got.IsActive == nil || *got.IsActive {
			t.Fatalf("isActive = %v, want false", got.IsActive)
		}
	})

	t.Run("true is stamped onto the doc", func(t *testing.T) {
		active := true
		out, err := StampIsActive(catalog, &active)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			IsActive *bool `json:"isActive"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if got.IsActive == nil || !*got.IsActive {
			t.Fatalf("isActive = %v, want true", got.IsActive)
		}
	})

	t.Run("malformed doc is a permanent error", func(t *testing.T) {
		active := false
		if _, err := StampIsActive([]byte("not json"), &active); err == nil {
			t.Fatal("expected an error for malformed doc")
		}
	})
}
