package catalogfile

import (
	"encoding/json"
	"testing"
)

func TestApply_UpsertsAndRemovals(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"Old"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}},{"id":"ITEM-2","descriptor":{"name":"two"}}]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{"upserts":[{"id":"ITEM-1","descriptor":{"name":"one-updated"}},{"id":"ITEM-3","descriptor":{"name":"three"}}],"removals":["ITEM-2"]},"offers":{}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var doc Doc
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if len(doc.Resources) != 2 {
		t.Fatalf("expected 2 resources after apply, got %d: %s", len(doc.Resources), result)
	}
	byID := map[string]json.RawMessage{}
	for _, r := range doc.Resources {
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

func TestDoc_IsActiveRoundTrips(t *testing.T) {
	active := false
	doc := Doc{ID: json.RawMessage(`"CAT-1"`), Descriptor: json.RawMessage(`{}`), Provider: json.RawMessage(`{}`), IsActive: &active}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Doc
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.IsActive == nil || *got.IsActive != false {
		t.Fatalf("IsActive = %v, want false", got.IsActive)
	}
}

func TestDoc_IsActiveOmittedWhenNil(t *testing.T) {
	doc := Doc{ID: json.RawMessage(`"CAT-1"`), Descriptor: json.RawMessage(`{}`), Provider: json.RawMessage(`{}`)}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := raw["isActive"]; ok {
		t.Fatalf("expected isActive omitted when nil, got %s", b)
	}
}

func TestApply_PreservesIsActive(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"Old"},"provider":{},"resources":[],"isActive":false}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var doc Doc
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if doc.IsActive == nil || *doc.IsActive != false {
		t.Fatalf("IsActive = %v, want false to survive Apply unchanged", doc.IsActive)
	}
}

func TestApply_CatalogAttributeOverlay(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"Old Name"},"provider":{"id":"P1"},"resources":[]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{},"catalog":{"descriptor":{"name":"New Name"}}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var doc Doc
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	var descriptor struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(doc.Descriptor, &descriptor); err != nil {
		t.Fatalf("parsing descriptor: %v", err)
	}
	if descriptor.Name != "New Name" {
		t.Errorf("descriptor.Name = %q, want New Name", descriptor.Name)
	}
}

// A catalog-attribute patch isn't limited to descriptor/provider -- the file
// spec's own examples name other fields too (e.g. a validity window), and a
// domain may add its own. Applying one must not drop it, and it must survive
// a further Apply untouched (Extra round-trips through Doc unmarshal/marshal).
func TestApply_ArbitraryCatalogAttributeOverlay(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{},"catalog":{"validity":{"endDate":"2027-01-01T00:00:00Z"},"isActive":false}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var doc Doc
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if doc.IsActive == nil || *doc.IsActive {
		t.Fatalf("isActive = %v, want false", doc.IsActive)
	}
	validity, ok := doc.Extra["validity"]
	if !ok {
		t.Fatalf("validity field dropped; doc = %s", result)
	}
	var got struct {
		EndDate string `json:"endDate"`
	}
	if err := json.Unmarshal(validity, &got); err != nil {
		t.Fatal(err)
	}
	if got.EndDate != "2027-01-01T00:00:00Z" {
		t.Errorf("validity.endDate = %q, want 2027-01-01T00:00:00Z", got.EndDate)
	}
}

// Apply must reject resources/offers named INSIDE the nested catalog-attribute
// patch: they have their own dedicated top-level upserts/removals diffing, so
// allowing them here too would open a second, conflicting path for the same
// content.
func TestApply_CatalogAttributeCannotNameResourcesOrOffers(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{},"catalog":{"resources":[{"id":"sneaky"}]}}`)

	if _, err := Apply(catalog, change); err == nil {
		t.Fatal("expected an error when the catalog-attribute patch names resources")
	}
}

// A top-level field that isn't one of the six this package actively reads/
// writes (e.g. a domain-specific attribute, or "validity") must survive a
// plain parse-then-marshal round trip through Doc, exactly like descriptor/
// provider/resources/offers/isActive already do.
func TestDoc_ExtraFieldsRoundTrip(t *testing.T) {
	original := []byte(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[],"rating":{"ratingValue":4.5},"validity":{"startDate":"2026-01-01T00:00:00Z"}}`)
	var doc Doc
	if err := json.Unmarshal(original, &doc); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["rating"]; !ok {
		t.Errorf("rating field dropped on round trip, got %s", out)
	}
	if _, ok := got["validity"]; !ok {
		t.Errorf("validity field dropped on round trip, got %s", out)
	}
}

func TestApply_NoChangesIsIdentity(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{}}`)

	result, err := Apply(catalog, change)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var doc Doc
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if len(doc.Resources) != 1 {
		t.Fatalf("expected 1 resource unchanged, got %d", len(doc.Resources))
	}
}

func TestApply_MissingIDIsError(t *testing.T) {
	catalog := []byte(`{"id":"CAT-1","descriptor":{},"provider":{},"resources":[{"descriptor":{"name":"no id"}}]}`)
	change := []byte(`{"catalogId":"CAT-1","fromVersion":1,"toVersion":2,"resources":{},"offers":{}}`)

	if _, err := Apply(catalog, change); err == nil {
		t.Fatal("expected error for resource missing id")
	}
}
