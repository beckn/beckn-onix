package catalog

import (
	"encoding/json"
	"testing"
)

func TestDiff(t *testing.T) {
	prior := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}},{"id":"ITEM-2","descriptor":{"name":"two"}}]}`)
	next := json.RawMessage(`{"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}},{"id":"ITEM-3","descriptor":{"name":"three"}}]}`)

	diff, changeCatalog, err := Diff(prior, next)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff.Resources.Upserts) != 1 || len(diff.Resources.Removals) != 1 {
		t.Fatalf("unexpected diff: %+v", diff.Resources)
	}
	if diff.Resources.Removals[0] != "ITEM-2" {
		t.Errorf("Removals = %v, want [ITEM-2]", diff.Resources.Removals)
	}
	if changeCatalog != nil {
		t.Errorf("expected no catalog-level attribute change, got %s", changeCatalog)
	}
}

func TestDiff_DuplicateSubmittedIDsCollapseToOneUpsert(t *testing.T) {
	prior := json.RawMessage(`{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}`)
	next := json.RawMessage(`{"resources":[{"id":"ITEM-1","descriptor":{"name":"v1"}},{"id":"ITEM-1","descriptor":{"name":"v2"}}]}`)

	diff, _, err := Diff(prior, next)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff.Resources.Upserts) != 1 {
		t.Fatalf("expected exactly 1 upsert for a duplicated id, got %d: %+v", len(diff.Resources.Upserts), diff.Resources.Upserts)
	}
}

func TestDiff_NoChangesIsEmpty(t *testing.T) {
	c := json.RawMessage(`{"id":"CAT-1","descriptor":{},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	diff, changeCatalog, err := Diff(c, c)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !diff.Resources.IsEmpty() || !diff.Offers.IsEmpty() {
		t.Errorf("expected empty diff for identical catalogs, got %+v", diff)
	}
	if changeCatalog != nil {
		t.Errorf("expected no catalog-level attribute change, got %s", changeCatalog)
	}
}

func TestDiff_DescriptorChangeReportedUnderCatalog(t *testing.T) {
	prior := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Old Name"},"provider":{},"resources":[]}`)
	next := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"New Name"},"provider":{},"resources":[]}`)

	diff, changeCatalog, err := Diff(prior, next)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !diff.Resources.IsEmpty() {
		t.Errorf("expected no resource diff, got %+v", diff.Resources)
	}
	if changeCatalog == nil {
		t.Fatal("expected a catalog-level attribute change")
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(changeCatalog, &attrs); err != nil {
		t.Fatalf("parsing changeCatalog: %v", err)
	}
	if _, ok := attrs["descriptor"]; !ok {
		t.Errorf("expected descriptor in changeCatalog, got %s", changeCatalog)
	}
}

// TestDiff_ArbitraryAttributeChangeReportedUnderCatalog proves the
// catalog-level attribute overlay isn't limited to a hardcoded
// descriptor/provider field list -- any top-level field the submitted
// catalog changes (e.g. a validity window) is reported under changeCatalog.
func TestDiff_ArbitraryAttributeChangeReportedUnderCatalog(t *testing.T) {
	prior := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[],"validity":{"startDate":"2026-01-01T00:00:00Z","endDate":"2026-06-30T23:59:59Z"}}`)
	next := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[],"validity":{"startDate":"2026-07-01T00:00:00Z","endDate":"2026-12-31T23:59:59Z"}}`)

	diff, changeCatalog, err := Diff(prior, next)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !diff.Resources.IsEmpty() {
		t.Errorf("expected no resource diff, got %+v", diff.Resources)
	}
	if changeCatalog == nil {
		t.Fatal("expected a catalog-level attribute change")
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(changeCatalog, &attrs); err != nil {
		t.Fatalf("parsing changeCatalog: %v", err)
	}
	if _, ok := attrs["validity"]; !ok {
		t.Errorf("expected validity in changeCatalog, got %s", changeCatalog)
	}
	if _, ok := attrs["descriptor"]; ok {
		t.Errorf("expected no descriptor change reported, got %s", changeCatalog)
	}
}
