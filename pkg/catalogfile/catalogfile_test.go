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
