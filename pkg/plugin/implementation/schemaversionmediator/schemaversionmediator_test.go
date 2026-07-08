package schemaversionmediator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/jsonata-go/jsonata"
)

// mockManifestLoader is a test double for definition.ManifestLoader.
type mockManifestLoader struct {
	bySubscriberID func(ctx context.Context, subscriberID string) (*model.ManifestDocument, error)
}

func (m *mockManifestLoader) GetBySubscriberID(ctx context.Context, subscriberID string) (*model.ManifestDocument, error) {
	if m.bySubscriberID != nil {
		return m.bySubscriberID(ctx, subscriberID)
	}
	return nil, nil
}

func (m *mockManifestLoader) GetByNetworkID(ctx context.Context, networkID string) (*model.ManifestDocument, error) {
	return nil, nil
}

func (m *mockManifestLoader) GetByMetadata(ctx context.Context, metadata model.ManifestMetadata) (*model.ManifestDocument, error) {
	return nil, nil
}

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

// schemaObj builds a SchemaObject for test manifests.
// baseURL is the URL prefix without version; versions are the supported version strings.
// The first version is used as the canonical (latest) by default.
func schemaObj(baseURL, typ string, versions ...string) model.SchemaObject {
	return model.SchemaObject{BaseURL: baseURL, Type: typ, SupportedVersions: versions}
}

func TestCheckCompatibility_NilManifest(t *testing.T) {
	extracted := []SchemaObjectRef{
		schemaRef("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order", "$.message.order"),
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
	extracted := []SchemaObjectRef{
		schemaRef("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order", "$.message.order"),
	}
	m := manifest(schemaObj("https://schema.beckn.io/retail/schema", "Order", "1.1.0"))

	needs, err := CheckCompatibility(extracted, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(needs) != 0 {
		t.Fatalf("expected no translation needed, got %d", len(needs))
	}
}

func TestCheckCompatibility_VersionMismatch(t *testing.T) {
	extracted := []SchemaObjectRef{
		schemaRef("https://schema.beckn.io/retail/schema/1.0.0/order.jsonld", "Order", "$.message.order"),
	}
	m := manifest(schemaObj("https://schema.beckn.io/retail/schema", "Order", "1.1.0"))

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
	if needs[0].CanonicalVersion != "1.1.0" {
		t.Errorf("unexpected CanonicalVersion: %s", needs[0].CanonicalVersion)
	}
	if needs[0].To.BaseURL != "https://schema.beckn.io/retail/schema" {
		t.Errorf("unexpected To.BaseURL: %s", needs[0].To.BaseURL)
	}
}

func TestCheckCompatibility_UnknownType(t *testing.T) {
	extracted := []SchemaObjectRef{
		schemaRef("https://schema.beckn.io/retail/schema/1.1.0/quote.jsonld", "Quote", "$.message.quote"),
	}
	m := manifest(schemaObj("https://schema.beckn.io/retail/schema", "Order", "1.1.0"))

	needs, err := CheckCompatibility(extracted, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unknown types (not declared in target manifest) are skipped — no translation target exists.
	if len(needs) != 0 {
		t.Fatalf("expected 0 translation needs for unknown type, got %d", len(needs))
	}
}

func TestCheckCompatibility_MixedOutcomes(t *testing.T) {
	extracted := []SchemaObjectRef{
		schemaRef("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order", "$.message.order"),        // compatible
		schemaRef("https://schema.beckn.io/retail/schema/1.0.0/item.jsonld", "Item", "$.message.order.items[0]"), // version mismatch
		schemaRef("https://schema.beckn.io/retail/schema/1.1.0/quote.jsonld", "Quote", "$.message.quote"),        // unknown type
	}
	m := manifest(
		schemaObj("https://schema.beckn.io/retail/schema", "Order", "1.1.0"),
		schemaObj("https://schema.beckn.io/retail/schema", "Item", "1.1.0"),
	)

	needs, err := CheckCompatibility(extracted, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Quote is unknown (not in manifest) → skipped. Only Item (version mismatch) is returned.
	if len(needs) != 1 {
		t.Fatalf("expected 1 translation need, got %d", len(needs))
	}

	itemNeed := findNeedByType(needs, "Item")
	if itemNeed == nil {
		t.Fatal("expected TranslationNeeded entry for Item")
	}
	if itemNeed.To == nil {
		t.Error("expected To to be set for Item version mismatch")
	}
}

func TestCheckCompatibility_EmptyManifest(t *testing.T) {
	extracted := []SchemaObjectRef{
		schemaRef("https://schema.beckn.io/retail/schema/1.1.0/order.jsonld", "Order", "$.message.order"),
	}
	m := manifest() // no schema objects

	needs, err := CheckCompatibility(extracted, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty manifest declares no types — all payload types are unknown and skipped.
	if len(needs) != 0 {
		t.Fatalf("expected 0 translation needs for empty manifest, got %d", len(needs))
	}
}

func TestCheckCompatibility_EmptyPayload(t *testing.T) {
	needs, err := CheckCompatibility([]SchemaObjectRef{}, manifest(
		schemaObj("https://schema.beckn.io/retail/schema", "Order", "1.1.0"),
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
	for _, action := range []PolicyAction{PolicyActionReject, PolicyActionTranslate} {
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

func TestLoadTranslationPolicy_PassThroughAsActionRejected(t *testing.T) {
	_, err := loadTranslationPolicy(map[string]string{"action": "passThrough"})
	if err == nil {
		t.Fatal("expected error when action=passThrough, got nil")
	}
}

func TestLoadTranslationPolicy_ValidOnFailure(t *testing.T) {
	for _, onFailure := range []PolicyAction{PolicyActionReject, PolicyActionPassThrough} {
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
	// onFailure is meaningless when action=reject.
	// A stale/invalid onFailure key must not produce an error in that case.
	p, err := loadTranslationPolicy(map[string]string{
		"action":    "reject",
		"onFailure": "unknown_value",
	})
	if err != nil {
		t.Errorf("action=reject: expected no error for stale onFailure key, got %v", err)
	}
	if p != nil && p.Action != PolicyActionReject {
		t.Errorf("action=reject: got %q", p.Action)
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
		{"2", false},    // bare number with no dot must not match
		{"v2", false},   // bare number with v prefix, no dot
		{"v.", false},   // dot but no digits
		{"v1.", false},  // trailing dot — digits only before dot
		{"1.", false},   // trailing dot without v prefix
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
		From:             PayloadRef{ContextURL: "https://schema.beckn.io/retail/v1.1/Order.jsonld", Type: "Order"},
		To:               &model.SchemaObject{BaseURL: "https://schema.beckn.io/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
		CanonicalVersion: "v2.0",
	}
	got, err := deriveArtifactURL(need)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://schema.beckn.io/retail/v2.0/Order_from_v1.1.jsonata"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDeriveArtifactURL_NilTo(t *testing.T) {
	need := TranslationNeeded{
		From: PayloadRef{ContextURL: "https://schema.beckn.io/retail/v1.1/Order.jsonld", Type: "Order"},
	}
	_, err := deriveArtifactURL(need)
	if err == nil {
		t.Fatal("expected error when To is nil")
	}
}

func TestDeriveArtifactURL_NoVersionInFrom(t *testing.T) {
	need := TranslationNeeded{
		From:             PayloadRef{ContextURL: "https://schema.beckn.io/retail/Order.jsonld", Type: "Order"},
		To:               &model.SchemaObject{BaseURL: "https://schema.beckn.io/retail", Type: "Order", SupportedVersions: []string{"v2.0"}},
		CanonicalVersion: "v2.0",
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
		From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
		CanonicalVersion: "v2.0",
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
		From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
		CanonicalVersion: "v2.0",
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
		From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
		CanonicalVersion: "v2.0",
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
		From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
		CanonicalVersion: "v2.0",
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
		From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
		CanonicalVersion: "v2.0",
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
		From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
		CanonicalVersion: "v2.0",
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
		From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
		To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
		CanonicalVersion: "v2.0",
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

func assertContainsType(t *testing.T, objects []SchemaObjectRef, typ string) {
	t.Helper()
	for _, o := range objects {
		if o.Type == typ {
			return
		}
	}
	t.Errorf("expected schema object with Type=%q not found in %v", typ, objects)
}

// schemaRef builds a SchemaObjectRef from a payload contextURL, type, and path.
// contextURL is the full URL as it appears in the payload (includes version segment).
func schemaRef(contextURL, typ, jsonataPath string) SchemaObjectRef {
	return SchemaObjectRef{PayloadRef: PayloadRef{ContextURL: contextURL, Type: typ}, JSONataPath: jsonataPath}
}

func findNeedByType(needs []TranslationNeeded, typ string) *TranslationNeeded {
	for i := range needs {
		if needs[i].From.Type == typ {
			return &needs[i]
		}
	}
	return nil
}

// --- ComposeExpression tests ---

func TestComposeExpression_Empty(t *testing.T) {
	expr, err := ComposeExpression(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expr != "$" {
		t.Errorf("expected identity expression \"$\", got %q", expr)
	}
}

func TestComposeExpression_SinglePatch(t *testing.T) {
	entries := []MappingEntry{
		{JSONataPath: "$.message", Expression: `{"state": status}`},
	}
	expr, err := ComposeExpression(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `$merge([$, {"state": status}])`
	if expr != want {
		t.Errorf("got %q, want %q", expr, want)
	}
}

func TestComposeExpression_MultiPatch(t *testing.T) {
	entries := []MappingEntry{
		{JSONataPath: "$.message", Expression: `{"state": status}`},
		{
			JSONataPath: "$.message.fulfillment",
			Expression:  `{"fulfillment": $merge([fulfillment, {"fulfillment_type": fulfillment.type}])}`,
		},
	}
	expr, err := ComposeExpression(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expression must begin with the merge wrapper and contain both patches.
	if !strings.Contains(expr, "$merge([$,") {
		t.Errorf("composed expression missing merge wrapper: %s", expr)
	}
	if !strings.Contains(expr, `{"state": status}`) {
		t.Errorf("composed expression missing Order patch: %s", expr)
	}
	if !strings.Contains(expr, `{"fulfillment":`) {
		t.Errorf("composed expression missing Fulfillment patch: %s", expr)
	}
}

func TestComposeExpression_EmptyExpressionReturnsError(t *testing.T) {
	entries := []MappingEntry{
		{JSONataPath: "$.message", Expression: ""},
	}
	_, err := ComposeExpression(entries)
	if err == nil {
		t.Fatal("expected error for empty expression, got nil")
	}
}

// --- Execute tests ---

func newTestTranslatorMediator(t *testing.T) *mediator {
	t.Helper()
	instance, err := jsonata.OpenLatest()
	if err != nil {
		t.Fatalf("jsonata.OpenLatest: %v", err)
	}
	return &mediator{
		jsonataInstance: instance,
		exprs:           newExprCache(),
		cache:           newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
		httpClient:      &http.Client{},
	}
}

func TestExecute_IdentityExpression(t *testing.T) {
	m := newTestTranslatorMediator(t)
	message := []byte(`{"id":"order-1","status":"ACTIVE"}`)
	result, err := m.Execute(context.Background(), "$", message)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Compare by value, not byte-for-byte: jsonata-go may re-serialize with
	// different key ordering than the original input.
	var orig, got map[string]any
	if err := json.Unmarshal(message, &orig); err != nil {
		t.Fatalf("unmarshal original: %v", err)
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["id"] != orig["id"] || got["status"] != orig["status"] {
		t.Errorf("identity expression changed field values: got %s", result)
	}
}

func TestExecute_SingleFieldRename(t *testing.T) {
	m := newTestTranslatorMediator(t)
	message := []byte(`{"id":"order-1","status":"ACTIVE","fulfillment":{"id":"ff-1","type":"HOME"}}`)

	expr, err := ComposeExpression([]MappingEntry{
		{JSONataPath: "$.message", Expression: `{"state": status}`},
	})
	if err != nil {
		t.Fatalf("ComposeExpression: %v", err)
	}

	result, err := m.Execute(context.Background(), expr, message)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var out map[string]any
	if err := unmarshalResult(result, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["state"] != "ACTIVE" {
		t.Errorf("expected state=ACTIVE, got %v", out["state"])
	}
	if _, ok := out["status"]; !ok {
		t.Error("status field should still be present after merge (non-destructive)")
	}
}

func TestExecute_MultiPathComposed(t *testing.T) {
	m := newTestTranslatorMediator(t)
	message := []byte(`{"id":"order-1","status":"ACTIVE","fulfillment":{"id":"ff-1","type":"HOME"},"quote":{"price":{"currency":"INR"}}}`)

	expr, err := ComposeExpression([]MappingEntry{
		{JSONataPath: "$.message", Expression: `{"state": status}`},
		{JSONataPath: "$.message.fulfillment", Expression: `{"fulfillment": $merge([fulfillment, {"fulfillment_type": fulfillment.type}])}`},
		{JSONataPath: "$.message.quote", Expression: `{"quote": $merge([quote, {"currency_code": quote.price.currency}])}`},
	})
	if err != nil {
		t.Fatalf("ComposeExpression: %v", err)
	}

	result, err := m.Execute(context.Background(), expr, message)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var out map[string]any
	if err := unmarshalResult(result, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if out["state"] != "ACTIVE" {
		t.Errorf("Order transform: expected state=ACTIVE, got %v", out["state"])
	}
	if ff, ok := out["fulfillment"].(map[string]any); !ok || ff["fulfillment_type"] != "HOME" {
		t.Errorf("Fulfillment transform: expected fulfillment_type=HOME, got %v", out["fulfillment"])
	}
	if q, ok := out["quote"].(map[string]any); !ok || q["currency_code"] != "INR" {
		t.Errorf("Quote transform: expected currency_code=INR, got %v", out["quote"])
	}
}

func TestExecute_ExpressionCacheHit(t *testing.T) {
	m := newTestTranslatorMediator(t)
	message := []byte(`{"status":"ACTIVE"}`)
	expr := `$merge([$, {"state": status}])`

	// First call compiles and caches.
	if _, err := m.Execute(context.Background(), expr, message); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	// Second call should hit cache (same compiled expression returned).
	if _, err := m.Execute(context.Background(), expr, message); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	m.exprs.mu.RLock()
	_, cached := m.exprs.entries[expr]
	m.exprs.mu.RUnlock()
	if !cached {
		t.Error("expression should be in cache after first Execute call")
	}
}

func TestExecute_InvalidExpression(t *testing.T) {
	m := newTestTranslatorMediator(t)
	_, err := m.Execute(context.Background(), "!!!invalid jsonata{{", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for invalid JSONata expression")
	}
}

// unmarshalResult unmarshals JSON bytes into v, for use in Execute tests.
func unmarshalResult(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

// --- fetchAllArtifacts tests ---

func TestFetchAllArtifacts_AllSucceed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/jsonata")
		w.Write([]byte(`{"state": status}`))
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}

	needs := []TranslationNeeded{
		{
			From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
			To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
			CanonicalVersion: "v2.0",
			JSONataPath:      "$.message",
		},
	}

	artifacts, failures := m.fetchAllArtifacts(context.Background(), needs)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got: %v", failures[0].Reason)
	}
	if _, ok := artifacts["$.message"]; !ok {
		t.Error("artifact keyed by JSONataPath not found in result")
	}
}

func TestFetchAllArtifacts_NilToIsFailure(t *testing.T) {
	m := &mediator{
		httpClient: &http.Client{},
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}

	needs := []TranslationNeeded{
		{
			From:        PayloadRef{ContextURL: "https://schema.beckn.io/v1.1/Unknown.jsonld", Type: "Unknown"},
			To:          nil,
			JSONataPath: "$.message.unknown",
		},
	}

	_, failures := m.fetchAllArtifacts(context.Background(), needs)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure for nil To, got %d", len(failures))
	}
	if failures[0].Need.From.Type != "Unknown" {
		t.Errorf("unexpected failed type: %q", failures[0].Need.From.Type)
	}
}

func TestFetchAllArtifacts_CollectsAllFailures(t *testing.T) {
	// Server returns 404 for all requests.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}

	needs := []TranslationNeeded{
		{
			From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
			To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
			CanonicalVersion: "v2.0",
			JSONataPath:      "$.message",
		},
		{
			From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Fulfillment.jsonld", Type: "Fulfillment"},
			To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Fulfillment", SupportedVersions: []string{"v1.1", "v2.0"}},
			CanonicalVersion: "v2.0",
			JSONataPath:      "$.message.fulfillment",
		},
	}

	_, failures := m.fetchAllArtifacts(context.Background(), needs)
	if len(failures) != 2 {
		t.Fatalf("expected 2 failures (one per 404), got %d", len(failures))
	}
	for _, f := range failures {
		if !errors.Is(f.Reason, ErrArtifactNotFound) {
			t.Errorf("expected ErrArtifactNotFound for %q, got: %v", f.Need.From.Type, f.Reason)
		}
	}
}

func TestFetchAllArtifacts_PartialSuccessReturnsBothSets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/retail/v2.0/Order_from_v1.1.jsonata" {
			w.Header().Set("Content-Type", "application/jsonata")
			w.Write([]byte(`{"state": status}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := &mediator{
		httpClient: srv.Client(),
		cache:      newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
	}

	needs := []TranslationNeeded{
		{
			From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Order.jsonld", Type: "Order"},
			To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Order", SupportedVersions: []string{"v1.1", "v2.0"}},
			CanonicalVersion: "v2.0",
			JSONataPath:      "$.message",
		},
		{
			From:             PayloadRef{ContextURL: srv.URL + "/retail/v1.1/Fulfillment.jsonld", Type: "Fulfillment"},
			To:               &model.SchemaObject{BaseURL: srv.URL + "/retail", Type: "Fulfillment", SupportedVersions: []string{"v1.1", "v2.0"}},
			CanonicalVersion: "v2.0",
			JSONataPath:      "$.message.fulfillment",
		},
	}

	artifacts, failures := m.fetchAllArtifacts(context.Background(), needs)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(failures))
	}
	if failures[0].Need.From.Type != "Fulfillment" {
		t.Errorf("wrong failed type: %q", failures[0].Need.From.Type)
	}
	if _, ok := artifacts["$.message"]; !ok {
		t.Error("Order artifact should be present despite Fulfillment failure")
	}
}

// --- provider.New / cold-start tests ---

// nodeManifestDoc returns a ManifestDocument whose Content is a valid node
// manifest YAML parseable by model.ParseNodeManifest. YAML keys match the
// struct tags in model.NodeManifest (camelCase).
func nodeManifestDoc(objects ...model.SchemaObject) *model.ManifestDocument {
	var sb strings.Builder
	sb.WriteString("manifestType: node-manifest\n")
	sb.WriteString("manifestVersion: \"2.0\"\n")
	sb.WriteString("subscriberId: \"test/test/test\"\n")
	sb.WriteString("schema:\n  schemaObjects:\n")
	for _, o := range objects {
		sb.WriteString("  - type: " + o.Type + "\n")
		sb.WriteString("    baseUrl: " + o.BaseURL + "\n")
		sb.WriteString("    supportedVersions:\n")
		for _, v := range o.SupportedVersions {
			sb.WriteString("      - \"" + v + "\"\n")
		}
	}
	sb.WriteString("governance:\n  effectiveFrom: \"2020-01-01T00:00:00Z\"\n")
	return &model.ManifestDocument{Content: []byte(sb.String())}
}

func TestNew_ColdStart_LoaderError(t *testing.T) {
	loader := &mockManifestLoader{
		bySubscriberID: func(_ context.Context, _ string) (*model.ManifestDocument, error) {
			return nil, errors.New("dedi unavailable")
		},
	}
	p := &provider{}
	svm, _, err := p.New(context.Background(), loader, map[string]string{"nodeId": "test-node"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	med := svm.(*mediator)
	if !med.notOnboarded {
		t.Error("expected notOnboarded=true when loader returns error")
	}
}

func TestNew_ColdStart_EmptySchemaObjects(t *testing.T) {
	loader := &mockManifestLoader{
		bySubscriberID: func(_ context.Context, _ string) (*model.ManifestDocument, error) {
			return nodeManifestDoc( /* no objects */ ), nil
		},
	}
	p := &provider{}
	svm, _, err := p.New(context.Background(), loader, map[string]string{"nodeId": "test-node"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	med := svm.(*mediator)
	if !med.notOnboarded {
		t.Error("expected notOnboarded=true when manifest has no schemaObjects")
	}
}

func TestNew_ColdStart_MissingNodeId(t *testing.T) {
	p := &provider{}
	svm, _, err := p.New(context.Background(), &mockManifestLoader{}, map[string]string{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	med := svm.(*mediator)
	if !med.notOnboarded {
		t.Error("expected notOnboarded=true when nodeId config key is absent")
	}
}

func TestNew_ValidManifest_NotOnboarded_False(t *testing.T) {
	loader := &mockManifestLoader{
		bySubscriberID: func(_ context.Context, _ string) (*model.ManifestDocument, error) {
			return nodeManifestDoc(model.SchemaObject{
				BaseURL:           "https://schema.beckn.io/retail",
				Type:              "Order",
				SupportedVersions: []string{"v2.0"},
			}), nil
		},
	}
	p := &provider{}
	svm, _, err := p.New(context.Background(), loader, map[string]string{"nodeId": "test-node"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	med := svm.(*mediator)
	if med.notOnboarded {
		t.Error("expected notOnboarded=false for valid manifest with schemaObjects")
	}
}

// --- Mediate tests ---

// buildPayload builds a minimal Beckn JSON body with the given network_id and
// optional message-level schema object declaration.
func buildPayload(networkID, counterpartyID string, msgContextURL, msgType string) []byte {
	msg := `{}`
	if msgContextURL != "" {
		msg = `{"@context":"` + msgContextURL + `","@type":"` + msgType + `"}`
	}
	return []byte(`{"context":{"network_id":"` + networkID + `","bap_id":"` + counterpartyID + `"},"message":` + msg + `}`)
}

func newTestMediatorFull(t *testing.T, loader *mockManifestLoader, cfg map[string]string, localManifest *model.NodeManifest) *mediator {
	t.Helper()
	instance, err := jsonata.OpenLatest()
	if err != nil {
		t.Fatalf("jsonata.OpenLatest: %v", err)
	}
	policy, err := loadTranslationPolicy(cfg)
	if err != nil {
		t.Fatalf("loadTranslationPolicy: %v", err)
	}
	return &mediator{
		policy:          *policy,
		loader:          loader,
		httpClient:      &http.Client{},
		cache:           newArtifactCache(defaultPositiveTTL, defaultNegativeTTL, defaultMaxCacheEntries),
		jsonataInstance: instance,
		exprs:           newExprCache(),
		localManifest:   localManifest,
	}
}

// stepCtxWithRemoteID returns a receiver StepContext (IsCallerHandler=false).
func stepCtxWithRemoteID(body []byte, remoteID string) *model.StepContext {
	ctx := context.WithValue(context.Background(), model.ContextKeyRemoteID, remoteID)
	return &model.StepContext{
		Context: ctx,
		Body:    body,
	}
}

// callerStepCtxWithRemoteID returns a caller StepContext (IsCallerHandler=true).
func callerStepCtxWithRemoteID(body []byte, remoteID string) *model.StepContext {
	ctx := context.WithValue(context.Background(), model.ContextKeyRemoteID, remoteID)
	return &model.StepContext{
		Context:         ctx,
		Body:            body,
		IsCallerHandler: true,
	}
}

// localManifestWith builds a minimal NodeManifest for use as the receiver's local manifest.
func localManifestWith(objs ...model.SchemaObject) *model.NodeManifest {
	return &model.NodeManifest{
		ManifestVersion: "1.0",
		ManifestType:    "node-manifest",
		SubscriberID:    "test/test/test",
		Schema:          model.NodeManifestSchema{SchemaObjects: objs},
		Governance:      model.NodeManifestGovernance{EffectiveFrom: "2020-01-01T00:00:00Z"},
	}
}

func TestMediate_NotOnboarded(t *testing.T) {
	m := &mediator{notOnboarded: true}
	err := m.Mediate(&model.StepContext{Context: context.Background(), Body: []byte(`{}`)})
	var me *MediationError
	if !errors.As(err, &me) {
		t.Fatalf("expected MediationError, got %T: %v", err, err)
	}
	if me.Code != "subscriberNotOnboarded" {
		t.Errorf("expected subscriberNotOnboarded, got %q", me.Code)
	}
}

func TestMediate_NoNetworkID_PassThrough(t *testing.T) {
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{}, nil)
	body := []byte(`{"context":{},"message":{}}`)
	ctx := stepCtxWithRemoteID(body, "bap.example.com")
	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("expected pass-through (nil), got: %v", err)
	}
}

func TestMediate_NetworkIdCamelCase_Recognised(t *testing.T) {
	// networkId (camelCase) must be accepted in addition to network_id (snake_case).
	// Use a local manifest with v1.0; payload is at v2.0 → incompatible → reject proves networkId was read.
	lm := localManifestWith(model.SchemaObject{
		BaseURL:           "https://schema.beckn.io/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{"action": "reject"}, lm)
	body := []byte(`{"context":{"networkId":"net1"},"message":{"@context":"https://schema.beckn.io/retail/v2.0/Order.jsonld","@type":"Order"}}`)
	ctx := stepCtxWithRemoteID(body, "bap.example.com")
	err := m.Mediate(ctx)
	var me *MediationError
	if !errors.As(err, &me) {
		t.Fatalf("expected MediationError (networkId recognised), got %T: %v", err, err)
	}
	if me.Code != "schemaIncompatible" {
		t.Errorf("expected schemaIncompatible, got %q", me.Code)
	}
}

func TestMediate_NoCounterpartyID_PassThrough(t *testing.T) {
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{}, nil)
	body := []byte(`{"context":{"network_id":"net1"},"message":{}}`)
	sctx := &model.StepContext{Context: context.Background(), Body: body}
	if err := m.Mediate(sctx); err != nil {
		t.Fatalf("expected pass-through (nil), got: %v", err)
	}
}

func TestMediate_CounterpartyManifestUnavailable_Reject(t *testing.T) {
	// Caller path: counterparty manifest fetch fails → onFailure=reject.
	loader := &mockManifestLoader{
		bySubscriberID: func(_ context.Context, _ string) (*model.ManifestDocument, error) {
			return nil, errors.New("dedi lookup failed")
		},
	}
	m := newTestMediatorFull(t, loader, map[string]string{"onFailure": "reject"}, nil)
	body := buildPayload("net1", "bap.example.com", "", "")
	ctx := callerStepCtxWithRemoteID(body, "bap.example.com")
	err := m.Mediate(ctx)
	var me *MediationError
	if !errors.As(err, &me) {
		t.Fatalf("expected MediationError, got %T: %v", err, err)
	}
	if me.Code != "schemaIncompatible" {
		t.Errorf("expected schemaIncompatible, got %q", me.Code)
	}
}

func TestMediate_CounterpartyManifestUnavailable_PassThrough(t *testing.T) {
	// Caller path: counterparty manifest fetch fails → onFailure=passThrough.
	loader := &mockManifestLoader{
		bySubscriberID: func(_ context.Context, _ string) (*model.ManifestDocument, error) {
			return nil, errors.New("dedi lookup failed")
		},
	}
	m := newTestMediatorFull(t, loader, map[string]string{"onFailure": "passThrough"}, nil)
	body := buildPayload("net1", "bap.example.com", "", "")
	ctx := callerStepCtxWithRemoteID(body, "bap.example.com")
	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("expected pass-through (nil), got: %v", err)
	}
}

func TestMediate_AllCompatible_PassThrough(t *testing.T) {
	lm := localManifestWith(model.SchemaObject{
		BaseURL:           "https://schema.beckn.io/retail",
		Type:              "Order",
		SupportedVersions: []string{"v2.0"},
	})
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{}, lm)
	body := buildPayload("net1", "bap.example.com",
		"https://schema.beckn.io/retail/v2.0/Order.jsonld", "Order")
	original := string(body)
	ctx := stepCtxWithRemoteID(body, "bap.example.com")
	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(ctx.Body) != original {
		t.Error("body should be unchanged when all schema objects are compatible")
	}
}

func TestMediate_ActionReject_OnIncompatible(t *testing.T) {
	// Receiver expects v1.0; inbound payload is at v2.0 — mismatch → reject.
	lm := localManifestWith(model.SchemaObject{
		BaseURL:           "https://schema.beckn.io/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{"action": "reject"}, lm)
	body := buildPayload("net1", "bap.example.com",
		"https://schema.beckn.io/retail/v2.0/Order.jsonld", "Order")
	ctx := stepCtxWithRemoteID(body, "bap.example.com")
	err := m.Mediate(ctx)
	var me *MediationError
	if !errors.As(err, &me) {
		t.Fatalf("expected MediationError, got %T: %v", err, err)
	}
	if me.Code != "schemaIncompatible" {
		t.Errorf("expected schemaIncompatible, got %q", me.Code)
	}
}

func TestMediate_ArtifactNotFound_Reject(t *testing.T) {
	// Artifact server returns 404 for all requests.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// Local manifest expects v1.0; payload is at v2.0 → mismatch → artifact fetch → 404 → reject.
	lm := localManifestWith(model.SchemaObject{
		BaseURL:           srv.URL + "/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{"onFailure": "reject"}, lm)
	m.httpClient = srv.Client()

	body := []byte(`{"context":{"network_id":"net1"},"message":{"@context":"` +
		srv.URL + `/retail/v2.0/Order.jsonld","@type":"Order"}}`)
	ctx := stepCtxWithRemoteID(body, "bap.example.com")
	err := m.Mediate(ctx)
	var me *MediationError
	if !errors.As(err, &me) {
		t.Fatalf("expected MediationError, got %T: %v", err, err)
	}
	if me.Code != "schemaIncompatible" {
		t.Errorf("expected schemaIncompatible, got %q", me.Code)
	}
}

func TestMediate_ArtifactNotFound_PassThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	lm := localManifestWith(model.SchemaObject{
		BaseURL:           srv.URL + "/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{"onFailure": "passThrough"}, lm)
	m.httpClient = srv.Client()

	body := []byte(`{"context":{"network_id":"net1"},"message":{"@context":"` +
		srv.URL + `/retail/v2.0/Order.jsonld","@type":"Order"}}`)
	ctx := stepCtxWithRemoteID(body, "bap.example.com")
	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("expected pass-through (nil), got: %v", err)
	}
}

func TestMediate_TranslationApplied(t *testing.T) {
	// Artifact server returns a JSONata expression that adds "state" from "status".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/jsonata")
		w.Write([]byte(`{"state": status}`))
	}))
	defer srv.Close()

	// Local manifest expects v1.0; inbound payload declares v2.0 → mismatch → artifact fetched → translation applied.
	lm := localManifestWith(model.SchemaObject{
		BaseURL:           srv.URL + "/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{}, lm)
	m.httpClient = srv.Client()

	body := []byte(`{"context":{"network_id":"net1"},"message":{"@context":"` +
		srv.URL + `/retail/v2.0/Order.jsonld","@type":"Order","status":"ACTIVE"}}`)
	ctx := stepCtxWithRemoteID(body, "bap.example.com")

	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(ctx.Body, &envelope); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(envelope["message"], &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if msg["state"] != "ACTIVE" {
		t.Errorf("expected state=ACTIVE after translation, got %v", msg["state"])
	}
}

// TestMediate_DataLoss note: the JSONata $merge executor is additive by design —
// it cannot drop fields present in the source. Data-loss detection in Mediate is
// therefore not reachable through the current JSONata executor path. The
// droppedFields function is tested directly in TestDroppedFields_* below. A
// Mediate-level integration test requires a replacement (non-merge) executor and
// will be added when non-JSONata translator support is introduced.

// --- droppedFields tests ---

func TestDroppedFields_NoneDropped(t *testing.T) {
	src := []byte(`{"a":"1","b":"2"}`)
	dst := []byte(`{"a":"1","b":"2","c":"3"}`)
	dropped, err := droppedFields(src, dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("expected no dropped fields, got %v", dropped)
	}
}

func TestDroppedFields_OneDropped(t *testing.T) {
	src := []byte(`{"a":"1","b":"2"}`)
	dst := []byte(`{"a":"1"}`)
	dropped, err := droppedFields(src, dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != "b" {
		t.Errorf("expected [b], got %v", dropped)
	}
}

func TestDroppedFields_NestedDropped(t *testing.T) {
	src := []byte(`{"order":{"id":"1","status":"ACTIVE"}}`)
	dst := []byte(`{"order":{"id":"1"}}`)
	dropped, err := droppedFields(src, dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != "order.status" {
		t.Errorf("expected [order.status], got %v", dropped)
	}
}

func TestDroppedFields_NoneDropped_Nested(t *testing.T) {
	src := []byte(`{"order":{"id":"1","status":"ACTIVE"}}`)
	dst := []byte(`{"order":{"id":"1","status":"ACTIVE","state":"ACTIVE"}}`)
	dropped, err := droppedFields(src, dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("expected no dropped fields, got %v", dropped)
	}
}
