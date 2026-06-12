package schemaversionmediator

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// --- loadTranslationPolicy tests ---

func TestLoadTranslationPolicy_Defaults(t *testing.T) {
	p, err := loadTranslationPolicy(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Action != PolicyActionTranslate {
		t.Errorf("expected default action=translate, got %q", p.Action)
	}
	if p.OnFailure != PolicyActionReject {
		t.Errorf("expected default onFailure=reject, got %q", p.OnFailure)
	}
}

func TestLoadTranslationPolicy_AllValidActions(t *testing.T) {
	for _, action := range []PolicyAction{PolicyActionReject, PolicyActionTranslate, PolicyActionPassIncompatible} {
		p, err := loadTranslationPolicy(map[string]string{"action": string(action)})
		if err != nil {
			t.Errorf("action=%q: unexpected error: %v", action, err)
			continue
		}
		if p.Action != action {
			t.Errorf("action=%q: got %q", action, p.Action)
		}
	}
}

func TestLoadTranslationPolicy_ValidOnFailure(t *testing.T) {
	for _, onFailure := range []PolicyAction{PolicyActionReject, PolicyActionPassIncompatible} {
		p, err := loadTranslationPolicy(map[string]string{
			"action":    "translate",
			"onFailure": string(onFailure),
		})
		if err != nil {
			t.Errorf("onFailure=%q: unexpected error: %v", onFailure, err)
			continue
		}
		if p.OnFailure != onFailure {
			t.Errorf("onFailure=%q: got %q", onFailure, p.OnFailure)
		}
	}
}

func TestLoadTranslationPolicy_InvalidAction(t *testing.T) {
	_, err := loadTranslationPolicy(map[string]string{"action": "unknown"})
	if err == nil {
		t.Fatal("expected error for unrecognised action, got nil")
	}
}

func TestLoadTranslationPolicy_InvalidOnFailure(t *testing.T) {
	_, err := loadTranslationPolicy(map[string]string{"onFailure": "unknown"})
	if err == nil {
		t.Fatal("expected error for unrecognised onFailure, got nil")
	}
}

func TestLoadTranslationPolicy_OnFailureTranslateRejected(t *testing.T) {
	_, err := loadTranslationPolicy(map[string]string{"onFailure": "translate"})
	if err == nil {
		t.Fatal("expected error when onFailure=translate, got nil")
	}
}

func TestLoadTranslationPolicy_OnFailureIgnoredWhenActionNotTranslate(t *testing.T) {
	// onFailure is meaningless when action=reject or pass_incompatible.
	// A stale/invalid onFailure key must not produce an error in those cases.
	for _, action := range []PolicyAction{PolicyActionReject, PolicyActionPassIncompatible} {
		p, err := loadTranslationPolicy(map[string]string{
			"action":    string(action),
			"onFailure": "unknown_value",
		})
		if err != nil {
			t.Errorf("action=%q: expected no error for stale onFailure key, got %v", action, err)
			continue
		}
		if p.Action != action {
			t.Errorf("action=%q: got %q", action, p.Action)
		}
	}
}

// --- isVersionSegment tests ---

func TestIsVersionSegment(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"v1.1", true},
		{"v2.0", true},
		{"V1.0", true},
		{"1.0", true},
		{"1.0.0", true},
		{"retail", false},
		{"", false},
		{"v", false},
		{"2", false},   // bare number with no dot must not match
		{"v2", false},  // bare number with v prefix, no dot
		{"v.", false},  // dot but no digits
		{"vX.Y", false},
		{"order.jsonld", false},
	}
	for _, c := range cases {
		if got := isVersionSegment(c.input); got != c.want {
			t.Errorf("isVersionSegment(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// --- extractVersionSegment tests ---

func TestExtractVersionSegment_Valid(t *testing.T) {
	got, err := extractVersionSegment("https://schema.beckn.io/retail/v1.1/Order.jsonld")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v1.1" {
		t.Errorf("expected v1.1, got %q", got)
	}
}

func TestExtractVersionSegment_NoVersion(t *testing.T) {
	_, err := extractVersionSegment("https://schema.beckn.io/retail/Order.jsonld")
	if err == nil {
		t.Fatal("expected error for URL with no version segment")
	}
}

func TestExtractVersionSegment_InvalidURL(t *testing.T) {
	_, err := extractVersionSegment("://bad url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

// --- deriveArtifactURL tests ---

func TestDeriveArtifactURL_Valid(t *testing.T) {
	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: "https://schema.beckn.io/retail/v1.1/Order.jsonld", Type: "Order"},
		To:   &model.SchemaObject{ContextURL: "https://schema.beckn.io/retail/v2.0/Order.jsonld", Type: "Order"},
	}
	got, err := deriveArtifactURL(need)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://schema.beckn.io/retail/v2.0/Order_from_v1.1"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDeriveArtifactURL_NilTo(t *testing.T) {
	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: "https://schema.beckn.io/retail/v1.1/Order.jsonld", Type: "Order"},
	}
	_, err := deriveArtifactURL(need)
	if err == nil {
		t.Fatal("expected error when To is nil")
	}
}

func TestDeriveArtifactURL_NoVersionInFrom(t *testing.T) {
	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: "https://schema.beckn.io/retail/Order.jsonld", Type: "Order"},
		To:   &model.SchemaObject{ContextURL: "https://schema.beckn.io/retail/v2.0/Order.jsonld", Type: "Order"},
	}
	_, err := deriveArtifactURL(need)
	if err == nil {
		t.Fatal("expected error when From URL has no version segment")
	}
}

// --- artifactCache tests ---

func TestArtifactCache_Miss(t *testing.T) {
	c := newArtifactCache(time.Hour, time.Minute, 10)
	_, found := c.get("missing")
	if found {
		t.Error("expected cache miss")
	}
}

func TestArtifactCache_PositiveHit(t *testing.T) {
	c := newArtifactCache(time.Hour, time.Minute, 10)
	artifact := &TranslationArtifact{Content: []byte("expr"), ContentType: "application/jsonata"}
	c.set("key", artifact)
	got, found := c.get("key")
	if !found {
		t.Fatal("expected cache hit")
	}
	if got != artifact {
		t.Error("unexpected artifact returned")
	}
}

func TestArtifactCache_NegativeHit(t *testing.T) {
	c := newArtifactCache(time.Hour, time.Minute, 10)
	c.set("key", nil)
	got, found := c.get("key")
	if !found {
		t.Fatal("expected negative cache hit")
	}
	if got != nil {
		t.Error("expected nil artifact for negative entry")
	}
}

func TestArtifactCache_PositiveExpiry(t *testing.T) {
	c := newArtifactCache(time.Millisecond, time.Minute, 10)
	c.set("key", &TranslationArtifact{Content: []byte("x"), ContentType: "application/jsonata"})
	time.Sleep(5 * time.Millisecond)
	_, found := c.get("key")
	if found {
		t.Error("expected expired positive entry to be a miss")
	}
}

func TestArtifactCache_NegativeExpiry(t *testing.T) {
	c := newArtifactCache(time.Hour, time.Millisecond, 10)
	c.set("key", nil)
	time.Sleep(5 * time.Millisecond)
	_, found := c.get("key")
	if found {
		t.Error("expected expired negative entry to be a miss")
	}
}

func TestArtifactCache_Eviction(t *testing.T) {
	c := newArtifactCache(time.Hour, time.Minute, 2)
	a := &TranslationArtifact{Content: []byte("a"), ContentType: "application/jsonata"}
	c.set("first", a)
	c.set("second", a)
	c.set("third", a) // should evict "first"
	_, found := c.get("first")
	if found {
		t.Error("expected first entry to be evicted")
	}
	if _, found := c.get("third"); !found {
		t.Error("expected third entry to be present")
	}
}

// --- fetchArtifact tests ---

func TestFetchArtifact_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/jsonata")
		w.Write([]byte(`$.orderId`))
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}

	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:   &model.SchemaObject{ContextURL: srv.URL + "/retail/v2.0/Order.jsonld", Type: "Order"},
	}

	got, err := m.fetchArtifact(context.Background(), need)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ContentType != "application/jsonata" {
		t.Errorf("unexpected ContentType: %s", got.ContentType)
	}
	if string(got.Content) != `$.orderId` {
		t.Errorf("unexpected Content: %s", got.Content)
	}
}

func TestFetchArtifact_MissingContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Suppress Go's content-type sniffing by setting it explicitly to "".
		w.Header()["Content-Type"] = []string{""}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`$.orderId`))
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}
	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:   &model.SchemaObject{ContextURL: srv.URL + "/retail/v2.0/Order.jsonld", Type: "Order"},
	}

	_, err := m.fetchArtifact(context.Background(), need)
	if err == nil {
		t.Fatal("expected error when Content-Type header is absent")
	}
}

func TestFetchArtifact_CacheHit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/jsonata")
		w.Write([]byte(`$.id`))
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}
	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:   &model.SchemaObject{ContextURL: srv.URL + "/retail/v2.0/Order.jsonld", Type: "Order"},
	}

	m.fetchArtifact(context.Background(), need)
	m.fetchArtifact(context.Background(), need)

	if calls != 1 {
		t.Errorf("expected 1 HTTP call due to caching, got %d", calls)
	}
}

func TestFetchArtifact_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}
	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:   &model.SchemaObject{ContextURL: srv.URL + "/retail/v2.0/Order.jsonld", Type: "Order"},
	}

	_, err := m.fetchArtifact(context.Background(), need)
	if !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("expected ErrArtifactNotFound, got %v", err)
	}
}

func TestFetchArtifact_NegativeCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}
	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:   &model.SchemaObject{ContextURL: srv.URL + "/retail/v2.0/Order.jsonld", Type: "Order"},
	}

	m.fetchArtifact(context.Background(), need)
	_, err := m.fetchArtifact(context.Background(), need)

	if !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("expected ErrArtifactNotFound, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 HTTP call (second should hit negative cache), got %d", calls)
	}
}

func TestFetchArtifact_RetryOnServerError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/jsonata")
		w.Write([]byte(`$.id`))
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}
	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:   &model.SchemaObject{ContextURL: srv.URL + "/retail/v2.0/Order.jsonld", Type: "Order"},
	}

	got, err := m.fetchArtifact(context.Background(), need)
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 HTTP calls (1 failure + 1 retry), got %d", calls)
	}
	if string(got.Content) != `$.id` {
		t.Errorf("unexpected content: %s", got.Content)
	}
}

func TestFetchArtifact_NoRetryOn404(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}
	need := TranslationNeeded{
		From: model.SchemaObject{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:   &model.SchemaObject{ContextURL: srv.URL + "/retail/v2.0/Order.jsonld", Type: "Order"},
	}

	m.fetchArtifact(context.Background(), need)
	if calls != 1 {
		t.Errorf("expected exactly 1 HTTP call (no retry on 404), got %d", calls)
	}
}

// --- loadMapManagerConfig tests ---

func TestLoadMapManagerConfig_Defaults(t *testing.T) {
	ft, pos, neg, max, err := loadMapManagerConfig(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != defaultFetchTimeout {
		t.Errorf("fetchTimeout: expected %v, got %v", defaultFetchTimeout, ft)
	}
	if pos != defaultPositiveTTL {
		t.Errorf("artifactCacheTTL: expected %v, got %v", defaultPositiveTTL, pos)
	}
	if neg != defaultNegativeTTL {
		t.Errorf("negativeCacheTTL: expected %v, got %v", defaultNegativeTTL, neg)
	}
	if max != defaultMaxCacheEntries {
		t.Errorf("maxCacheEntries: expected %v, got %v", defaultMaxCacheEntries, max)
	}
}

func TestLoadMapManagerConfig_ValidOverrides(t *testing.T) {
	ft, pos, neg, max, err := loadMapManagerConfig(map[string]string{
		"fetchTimeout":     "10s",
		"artifactCacheTTL": "1h",
		"negativeCacheTTL": "2m",
		"maxCacheEntries":  "100",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != 10*time.Second {
		t.Errorf("fetchTimeout: got %v", ft)
	}
	if pos != time.Hour {
		t.Errorf("artifactCacheTTL: got %v", pos)
	}
	if neg != 2*time.Minute {
		t.Errorf("negativeCacheTTL: got %v", neg)
	}
	if max != 100 {
		t.Errorf("maxCacheEntries: got %v", max)
	}
}

func TestLoadMapManagerConfig_InvalidDuration(t *testing.T) {
	_, _, _, _, err := loadMapManagerConfig(map[string]string{"fetchTimeout": "notaduration"})
	if err == nil {
		t.Fatal("expected error for invalid fetchTimeout")
	}
}

func TestLoadMapManagerConfig_InvalidMaxEntries(t *testing.T) {
	_, _, _, _, err := loadMapManagerConfig(map[string]string{"maxCacheEntries": "-1"})
	if err == nil {
		t.Fatal("expected error for non-positive maxCacheEntries")
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
