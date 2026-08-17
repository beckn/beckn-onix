package catalog

import (
	"encoding/json"
	"testing"
)

// resourcesOf decodes result's top-level "resources" array for assertions.
func resourcesOf(t *testing.T, result []byte) []json.RawMessage {
	t.Helper()
	var doc struct {
		Resources []json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	return doc.Resources
}

func TestApply_UpsertsAndRemovals(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"Old"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}},{"id":"ITEM-2","descriptor":{"name":"two"}}]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{"upserts":[{"id":"ITEM-1","descriptor":{"name":"one-updated"}},{"id":"ITEM-3","descriptor":{"name":"three"}}],"removals":["ITEM-2"]},"offers":{}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resources := resourcesOf(t, result)
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources after apply, got %d: %s", len(resources), result)
	}
	byID := map[string]json.RawMessage{}
	for _, r := range resources {
		id, _ := ItemID(r)
		byID[id] = r
	}
	if _, ok := byID["ITEM-2"]; ok {
		t.Error("expected ITEM-2 removed")
	}
	if _, ok := byID["ITEM-1"]; !ok {
		t.Error("expected ITEM-1 to remain (updated)")
	}
	if _, ok := byID["ITEM-3"]; !ok {
		t.Error("expected ITEM-3 added")
	}
}

func TestApply_DuplicateNewIDUpsertsCollapseToOne(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"Old"},"provider":{},"resources":[]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{"upserts":[{"id":"ITEM-1","descriptor":{"name":"v1"}},{"id":"ITEM-1","descriptor":{"name":"v2"}}]},"offers":{}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resources := resourcesOf(t, result)
	count := 0
	for _, r := range resources {
		id, _ := ItemID(r)
		if id == "ITEM-1" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected ITEM-1 to appear exactly once, appeared %d times: %s", count, result)
	}
}

func TestApply_CatalogAttributeOverlay(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"Old Name"},"provider":{"id":"P1"},"resources":[]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{},"catalog":{"descriptor":{"name":"New Name"}}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var doc struct {
		Descriptor struct {
			Name string `json:"name"`
		} `json:"descriptor"`
	}
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if doc.Descriptor.Name != "New Name" {
		t.Errorf("descriptor.Name = %q, want New Name", doc.Descriptor.Name)
	}
}

// TestApply_ArbitraryCatalogAttributeOverlay proves the "catalog" overlay
// isn't limited to a hardcoded field list (e.g. descriptor/provider) --
// any top-level field the change file names gets applied, matching the
// file spec's own examples ("name, validity window").
func TestApply_ArbitraryCatalogAttributeOverlay(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[],"validity":{"startDate":"2026-01-01T00:00:00Z","endDate":"2026-06-30T23:59:59Z"}}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{},"catalog":{"validity":{"startDate":"2026-07-01T00:00:00Z","endDate":"2026-12-31T23:59:59Z"}}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var doc struct {
		Validity struct {
			StartDate string `json:"startDate"`
		} `json:"validity"`
	}
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if doc.Validity.StartDate != "2026-07-01T00:00:00Z" {
		t.Errorf("validity.startDate = %q, want 2026-07-01T00:00:00Z", doc.Validity.StartDate)
	}
}

// TestApply_PreservesUnknownTopLevelFields proves a field Apply never
// touches (neither resources/offers nor named in the change's "catalog"
// object) survives unchanged -- a fixed-struct implementation would drop
// it silently on every Apply call, not just when nothing referenced it.
func TestApply_PreservesUnknownTopLevelFields(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[],"rating":{"ratingValue":4.5,"ratingCount":100},"tags":["fresh","local"]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{"upserts":[{"id":"ITEM-1","descriptor":{"name":"one"}}]},"offers":{}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var doc struct {
		Rating struct {
			RatingValue float64 `json:"ratingValue"`
			RatingCount int     `json:"ratingCount"`
		} `json:"rating"`
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if doc.Rating.RatingValue != 4.5 || doc.Rating.RatingCount != 100 {
		t.Errorf("rating not preserved: %+v", doc.Rating)
	}
	if len(doc.Tags) != 2 || doc.Tags[0] != "fresh" || doc.Tags[1] != "local" {
		t.Errorf("tags not preserved: %+v", doc.Tags)
	}
}

func TestApply_NoChangesIsIdentity(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	resources := resourcesOf(t, result)
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource unchanged, got %d", len(resources))
	}
}

func TestApply_MissingIDIsError(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{},"provider":{},"resources":[{"descriptor":{"name":"no id"}}]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{}}`)

	if _, err := Apply(catalog, change); err == nil {
		t.Fatal("expected error for resource missing id")
	}
}

// TestApply_NoOffersFieldStaysAbsent proves a catalog with no "offers"
// field doesn't gain an empty "offers": [] just because Apply ran.
func TestApply_NoOffersFieldStaysAbsent(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if _, ok := doc["offers"]; ok {
		t.Errorf("expected no offers field, got %s", doc["offers"])
	}
}
