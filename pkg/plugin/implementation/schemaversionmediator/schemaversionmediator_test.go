package schemaversionmediator

import (
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// --- WalkPayload tests ---

func TestWalkPayload_SingleObject(t *testing.T) {
	payload := []byte(`{
		"@context": "https://schema.beckn.io/retail/schema/1.1.0/order.jsonld",
		"@type": "Order"
	}`)
	got, err := WalkPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 schema object, got %d", len(got))
	}
	if got[0].ContextURL != "https://schema.beckn.io/retail/schema/1.1.0/order.jsonld" {
		t.Errorf("unexpected ContextURL: %s", got[0].ContextURL)
	}
	if got[0].Type != "Order" {
		t.Errorf("unexpected Type: %s", got[0].Type)
	}
}

func TestWalkPayload_NestedObjects(t *testing.T) {
	payload := []byte(`{
		"message": {
			"@context": "https://schema.beckn.io/retail/schema/1.1.0/order.jsonld",
			"@type": "Order",
			"item": {
				"@context": "https://schema.beckn.io/retail/schema/1.1.0/item.jsonld",
				"@type": "Item"
			}
		}
	}`)
	got, err := WalkPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 schema objects, got %d", len(got))
	}
	assertContainsType(t, got, "Order")
	assertContainsType(t, got, "Item")
}

// TestWalkPayload_ParentAndChildBothCollected confirms that when a parent node
// and a nested child node each carry independent "@context"/"@type" declarations,
// both are collected — they represent distinct schema contracts.
func TestWalkPayload_ParentAndChildBothCollected(t *testing.T) {
	payload := []byte(`{
		"@context": "https://schema.beckn.io/retail/schema/1.1.0/order.jsonld",
		"@type": "Order",
		"fulfillment": {
			"@context": "https://schema.beckn.io/retail/schema/1.1.0/fulfillment.jsonld",
			"@type": "Fulfillment"
		}
	}`)
	got, err := WalkPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 schema objects (parent + child), got %d", len(got))
	}
	assertContainsType(t, got, "Order")
	assertContainsType(t, got, "Fulfillment")
}

func TestWalkPayload_ArrayOfObjects(t *testing.T) {
	payload := []byte(`{
		"items": [
			{
				"@context": "https://schema.beckn.io/retail/schema/1.1.0/item.jsonld",
				"@type": "Item"
			},
			{
				"@context": "https://schema.beckn.io/retail/schema/1.1.0/item.jsonld",
				"@type": "Item"
			}
		]
	}`)
	got, err := WalkPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 schema objects, got %d", len(got))
	}
}

func TestWalkPayload_NoSchemaObjects(t *testing.T) {
	payload := []byte(`{"context": {"domain": "retail"}, "message": {}}`)
	got, err := WalkPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 schema objects, got %d", len(got))
	}
}

func TestWalkPayload_MissingType(t *testing.T) {
	// @context present but no @type — should not be collected
	payload := []byte(`{"@context": "https://schema.beckn.io/retail/schema/1.1.0/order.jsonld"}`)
	got, err := WalkPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 schema objects, got %d", len(got))
	}
}

func TestWalkPayload_InvalidJSON(t *testing.T) {
	_, err := WalkPayload([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// --- CheckCompatibility tests ---

func manifest(objects ...model.SchemaObject) *model.NodeManifest {
	return &model.NodeManifest{
		Schema: model.NodeManifestSchema{SchemaObjects: objects},
	}
}

func schemaObj(contextURL, typ string) model.SchemaObject {
	return model.SchemaObject{ContextURL: contextURL, Type: typ}
}

func TestCheckCompatibility_NilManifest(t *testing.T) {
	extracted := []model.SchemaObject{
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),
	}
	needs, err := CheckCompatibility(extracted, nil)
	if !errors.Is(err, ErrNoManifest) {
		t.Fatalf("expected ErrNoManifest, got %v", err)
	}
	if needs != nil {
		t.Errorf("expected nil needs when manifest is absent, got %v", needs)
	}
}

func TestCheckCompatibility_AllCompatible(t *testing.T) {
	extracted := []model.SchemaObject{
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),
	}
	m := manifest(schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"))

	needs, err := CheckCompatibility(extracted, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(needs) != 0 {
		t.Fatalf("expected no translation needed, got %d", len(needs))
	}
}

func TestCheckCompatibility_VersionMismatch(t *testing.T) {
	extracted := []model.SchemaObject{
		schemaObj("https://schema.beckn.io/retail/schema/1.0.0/order.jsonld", "Order"),
	}
	m := manifest(schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"))

	needs, err := CheckCompatibility(extracted, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(needs) != 1 {
		t.Fatalf("expected 1 translation needed, got %d", len(needs))
	}
	if needs[0].From.ContextURL != "https://schema.beckn.io/retail/schema/1.0.0/order.jsonld" {
		t.Errorf("unexpected From.ContextURL: %s", needs[0].From.ContextURL)
	}
	if needs[0].To == nil {
		t.Fatal("expected To to be set for version mismatch")
	}
	if needs[0].To.ContextURL != "https://schema.beckn.io/retail/schema/1.1.0/order.jsonld" {
		t.Errorf("unexpected To.ContextURL: %s", needs[0].To.ContextURL)
	}
}

func TestCheckCompatibility_UnknownType(t *testing.T) {
	extracted := []model.SchemaObject{
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/quote.jsonld", "Quote"),
	}
	m := manifest(schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"))

	needs, err := CheckCompatibility(extracted, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(needs) != 1 {
		t.Fatalf("expected 1 translation needed, got %d", len(needs))
	}
	if needs[0].To != nil {
		t.Error("expected To to be nil for unknown type")
	}
}

func TestCheckCompatibility_MixedOutcomes(t *testing.T) {
	extracted := []model.SchemaObject{
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),  // compatible
		schemaObj("https://schema.beckn.io/retail/schema/1.0.0/item.jsonld", "Item"),    // version mismatch
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/quote.jsonld", "Quote"), // unknown type
	}
	m := manifest(
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/item.jsonld", "Item"),
	)

	needs, err := CheckCompatibility(extracted, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(needs) != 2 {
		t.Fatalf("expected 2 translation needs, got %d", len(needs))
	}

	// Assert by type rather than by index — order is an implementation detail.
	itemNeed := findNeedByType(needs, "Item")
	if itemNeed == nil {
		t.Fatal("expected TranslationNeeded entry for Item")
	}
	if itemNeed.To == nil {
		t.Error("expected To to be set for Item version mismatch")
	}

	quoteNeed := findNeedByType(needs, "Quote")
	if quoteNeed == nil {
		t.Fatal("expected TranslationNeeded entry for Quote")
	}
	if quoteNeed.To != nil {
		t.Error("expected To to be nil for unknown Quote type")
	}
}

func TestCheckCompatibility_EmptyManifest(t *testing.T) {
	extracted := []model.SchemaObject{
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),
	}
	m := manifest() // no schema objects

	needs, err := CheckCompatibility(extracted, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(needs) != 1 {
		t.Fatalf("expected 1 translation needed, got %d", len(needs))
	}
	if needs[0].To != nil {
		t.Error("expected To to be nil when manifest is empty")
	}
}

func TestCheckCompatibility_EmptyPayload(t *testing.T) {
	needs, err := CheckCompatibility([]model.SchemaObject{}, manifest(
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(needs) != 0 {
		t.Fatalf("expected no translation needed for empty payload, got %d", len(needs))
	}
}

// --- helpers ---

func assertContainsType(t *testing.T, objects []model.SchemaObject, typ string) {
	t.Helper()
	for _, o := range objects {
		if o.Type == typ {
			return
		}
	}
	t.Errorf("expected schema object with Type=%q not found in %v", typ, objects)
}

func findNeedByType(needs []TranslationNeeded, typ string) *TranslationNeeded {
	for i := range needs {
		if needs[i].From.Type == typ {
			return &needs[i]
		}
	}
	return nil
}
