package schemaversionmediator

import (
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

func TestCheckCompatibility_AllCompatible(t *testing.T) {
	extracted := []model.SchemaObject{
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),
	}
	m := manifest(schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"))

	needs := CheckCompatibility(extracted, m)
	if len(needs) != 0 {
		t.Fatalf("expected no translation needed, got %d", len(needs))
	}
}

func TestCheckCompatibility_VersionMismatch(t *testing.T) {
	extracted := []model.SchemaObject{
		schemaObj("https://schema.beckn.io/retail/schema/1.0.0/order.jsonld", "Order"),
	}
	m := manifest(schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"))

	needs := CheckCompatibility(extracted, m)
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

	needs := CheckCompatibility(extracted, m)
	if len(needs) != 1 {
		t.Fatalf("expected 1 translation needed, got %d", len(needs))
	}
	if needs[0].To != nil {
		t.Error("expected To to be nil for unknown type")
	}
}

func TestCheckCompatibility_MixedOutcomes(t *testing.T) {
	extracted := []model.SchemaObject{
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),   // compatible
		schemaObj("https://schema.beckn.io/retail/schema/1.0.0/item.jsonld", "Item"),     // version mismatch
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/quote.jsonld", "Quote"),   // unknown type
	}
	m := manifest(
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/item.jsonld", "Item"),
	)

	needs := CheckCompatibility(extracted, m)
	if len(needs) != 2 {
		t.Fatalf("expected 2 translation needs, got %d", len(needs))
	}

	// Item: version mismatch — To must be set
	itemNeed := needs[0]
	if itemNeed.From.Type != "Item" {
		t.Errorf("expected Item mismatch first, got %s", itemNeed.From.Type)
	}
	if itemNeed.To == nil {
		t.Error("expected To to be set for Item version mismatch")
	}

	// Quote: unknown type — To must be nil
	quoteNeed := needs[1]
	if quoteNeed.From.Type != "Quote" {
		t.Errorf("expected Quote unknown type second, got %s", quoteNeed.From.Type)
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

	needs := CheckCompatibility(extracted, m)
	if len(needs) != 1 {
		t.Fatalf("expected 1 translation needed, got %d", len(needs))
	}
	if needs[0].To != nil {
		t.Error("expected To to be nil when manifest is empty")
	}
}

func TestCheckCompatibility_EmptyPayload(t *testing.T) {
	needs := CheckCompatibility([]model.SchemaObject{}, manifest(
		schemaObj("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order"),
	))
	if len(needs) != 0 {
		t.Fatalf("expected no translation needed for empty payload, got %d", len(needs))
	}
}
