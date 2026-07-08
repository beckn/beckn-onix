//go:build integration

// Integration tests for schemaversionmediator.
//
// These tests make real HTTP requests to raw.githubusercontent.com to fetch
// translation artifacts from the feat/773-schema-version-mediation-test branch
// of github.com/beckn/local-retail. They are intentionally excluded from the
// standard CI pipeline (go test ./...) and must be run explicitly:
//
//	go test -tags integration -timeout 60s ./pkg/plugin/implementation/schemaversionmediator/...
//
// Network access to raw.githubusercontent.com is required.
// The test fixtures are defined by the node manifests in:
//
//	testnet/retail-devkit/manifests/bap-node-manifest.yaml
//	testnet/retail-devkit/manifests/bpp-node-manifest.yaml
//
// on the feat/773-schema-version-mediation-test branch of beckn/local-retail.

package schemaversionmediator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// featureBranchBaseURL is the raw GitHub base for all schema artifacts used in
// these tests. Both BAP and BPP node manifests point to this branch for
// RetailConsideration and RetailOffer schemas.
const featureBranchBaseURL = "https://raw.githubusercontent.com/beckn/local-retail/refs/heads/feat/773-schema-version-mediation-test/schema"

const (
	retailConsiderationBaseURL = featureBranchBaseURL + "/RetailConsideration"
	retailOfferBaseURL         = featureBranchBaseURL + "/RetailOffer"
)

// bppManifest mirrors bpp-node-manifest.yaml: RetailConsideration v2.2, RetailOffer v2.2.
func bppManifest() *model.NodeManifest {
	return localManifestWith(
		model.SchemaObject{
			Type:              "RetailConsideration",
			BaseURL:           retailConsiderationBaseURL,
			SupportedVersions: []string{"v2.2"},
		},
		model.SchemaObject{
			Type:              "RetailOffer",
			BaseURL:           retailOfferBaseURL,
			SupportedVersions: []string{"v2.2"},
		},
	)
}

// bapManifest mirrors bap-node-manifest.yaml: RetailConsideration v2.1, RetailOffer v2.1.
func bapManifest() *model.NodeManifest {
	return localManifestWith(
		model.SchemaObject{
			Type:              "RetailConsideration",
			BaseURL:           retailConsiderationBaseURL,
			SupportedVersions: []string{"v2.1"},
		},
		model.SchemaObject{
			Type:              "RetailOffer",
			BaseURL:           retailOfferBaseURL,
			SupportedVersions: []string{"v2.1"},
		},
	)
}

// bppReceiverMediator returns a mediator configured as a BPP receiver:
// local manifest is the BPP manifest (expects v2.2 schemas).
func bppReceiverMediator(t *testing.T) *mediator {
	t.Helper()
	return newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{"action": "translate", "onFailure": "reject"}, bppManifest())
}

// bapReceiverMediator returns a mediator configured as a BAP receiver:
// local manifest is the BAP manifest (expects v2.1 schemas).
func bapReceiverMediator(t *testing.T) *mediator {
	t.Helper()
	return newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{"action": "translate", "onFailure": "reject"}, bapManifest())
}

// considerationPayload builds a minimal Beckn payload with a RetailConsideration
// message object at the given version, carrying the supplied extra fields.
func considerationPayload(version string, fields map[string]any) []byte {
	contextURL := retailConsiderationBaseURL + "/" + version + "/context.jsonld"
	msg := map[string]any{
		"@context": contextURL,
		"@type":    "RetailConsideration",
	}
	for k, v := range fields {
		msg[k] = v
	}
	body, _ := json.Marshal(map[string]any{
		"context": map[string]any{"network_id": "beckn.one"},
		"message": msg,
	})
	return body
}

// offerPayload builds a minimal Beckn payload with a RetailOffer message object.
func offerPayload(version string, fields map[string]any) []byte {
	contextURL := retailOfferBaseURL + "/" + version + "/context.jsonld"
	msg := map[string]any{
		"@context": contextURL,
		"@type":    "RetailOffer",
	}
	for k, v := range fields {
		msg[k] = v
	}
	body, _ := json.Marshal(map[string]any{
		"context": map[string]any{"network_id": "beckn.one"},
		"message": msg,
	})
	return body
}

// messageFields unmarshals the "message" subtree from a Mediate result body.
func messageFields(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(envelope["message"], &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	return msg
}

// --- S1: BPP Receiver — inbound RetailConsideration v2.1 translated to v2.2 ---
//
// BAP sends currency field (v2.1 convention).
// BPP expects currencyCode (v2.2 convention).
// Artifact: RetailConsideration/v2.2/RetailConsideration_from_v2.1 → {"currencyCode": currency}
func TestIntegration_BPPReceiver_RetailConsideration_v21_to_v22(t *testing.T) {
	m := bppReceiverMediator(t)
	body := considerationPayload("v2.1", map[string]any{"currency": "USD", "value": "99.00"})
	ctx := stepCtxWithRemoteID(body, "food-finder-bap.beckn.one")

	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("Mediate() unexpected error: %v", err)
	}

	msg := messageFields(t, ctx.Body)

	if msg["currencyCode"] != "USD" {
		t.Errorf("expected currencyCode=USD after v2.1→v2.2 translation, got %v", msg["currencyCode"])
	}
	if msg["value"] != "99.00" {
		t.Errorf("non-translated field value should be preserved, got %v", msg["value"])
	}
}

// --- S2: BAP Receiver — inbound RetailConsideration v2.2 translated back to v2.1 ---
//
// BPP response carries currencyCode (v2.2). BAP expects currency (v2.1).
// Artifact: RetailConsideration/v2.1/RetailConsideration_from_v2.2
//   → $ ~> |$|{"currency": currencyCode}, ["currencyCode"]|
//
// Note: the artifact uses a JSONata transform expression to rename the field.
// The rename (currencyCode → currency) is applied. Field deletion of currencyCode
// does not take effect through $merge composition — see the comment at
// TestMediate_DataLoss in schemaversionmediator_test.go for the known limitation.
func TestIntegration_BAPReceiver_RetailConsideration_v22_to_v21(t *testing.T) {
	m := bapReceiverMediator(t)
	body := considerationPayload("v2.2", map[string]any{"currencyCode": "USD", "value": "99.00"})
	ctx := stepCtxWithRemoteID(body, "open-kitchen-bpp.beckn.one")

	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("Mediate() unexpected error: %v", err)
	}

	msg := messageFields(t, ctx.Body)

	if msg["currency"] != "USD" {
		t.Errorf("expected currency=USD after v2.2→v2.1 rename, got %v", msg["currency"])
	}
	if msg["value"] != "99.00" {
		t.Errorf("non-translated field value should be preserved, got %v", msg["value"])
	}
}

// --- S3: Compatible payload — same version, no artifact fetched ---
//
// BPP manifest expects RetailConsideration v2.2.
// Inbound payload already declares v2.2 → compatible → pass through unchanged.
func TestIntegration_BPPReceiver_Compatible_NoTranslation(t *testing.T) {
	m := bppReceiverMediator(t)
	body := considerationPayload("v2.2", map[string]any{"currencyCode": "USD"})
	ctx := stepCtxWithRemoteID(body, "food-finder-bap.beckn.one")
	originalBody := string(body)

	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("Mediate() unexpected error on compatible payload: %v", err)
	}
	if string(ctx.Body) != originalBody {
		t.Errorf("expected body unchanged for compatible payload")
	}
}

// --- S4: Artifact cache — second call skips the HTTP fetch ---
//
// Run the same translation twice. The artifact cache should serve the second
// request from memory; we verify by checking the mediator's cache state after
// the first call populates it.
func TestIntegration_ArtifactCache_HitOnSecondCall(t *testing.T) {
	m := bppReceiverMediator(t)
	body := considerationPayload("v2.1", map[string]any{"currency": "EUR"})

	ctx1 := stepCtxWithRemoteID(body, "food-finder-bap.beckn.one")
	if err := m.Mediate(ctx1); err != nil {
		t.Fatalf("first Mediate() error: %v", err)
	}

	// Cache should now hold the artifact. Record cache size before second call.
	m.cache.mu.Lock()
	cacheSize := len(m.cache.entries)
	m.cache.mu.Unlock()

	ctx2 := stepCtxWithRemoteID(body, "food-finder-bap.beckn.one")
	if err := m.Mediate(ctx2); err != nil {
		t.Fatalf("second Mediate() error: %v", err)
	}

	m.cache.mu.Lock()
	cacheSizeAfter := len(m.cache.entries)
	m.cache.mu.Unlock()

	if cacheSizeAfter != cacheSize {
		t.Errorf("expected cache size unchanged on second call (hit), got %d → %d", cacheSize, cacheSizeAfter)
	}

	msg := messageFields(t, ctx2.Body)
	if msg["currencyCode"] != "EUR" {
		t.Errorf("expected currencyCode=EUR from cached artifact, got %v", msg["currencyCode"])
	}
}

// --- S5: Multi-object payload — RetailConsideration + RetailOffer both translated ---
//
// Payload has two schema objects at v2.1 nested inside the message — WalkPayload
// collects @context/@type pairs at any depth. BPP expects both at v2.2.
// Both artifacts are fetched and composed into a single $merge expression.
func TestIntegration_BPPReceiver_MultiObject_BothTranslated(t *testing.T) {
	m := bppReceiverMediator(t)

	considerationCtxURL := retailConsiderationBaseURL + "/v2.1/context.jsonld"
	offerCtxURL := retailOfferBaseURL + "/v2.1/context.jsonld"

	// WalkPayload finds @context+@type pairs at any nesting level.
	// The outer message carries RetailConsideration; a nested "offer" carries RetailOffer.
	body, _ := json.Marshal(map[string]any{
		"context": map[string]any{"network_id": "beckn.one"},
		"message": map[string]any{
			"@context": considerationCtxURL,
			"@type":    "RetailConsideration",
			"currency": "USD",
			"offer": map[string]any{
				"@context":     offerCtxURL,
				"@type":        "RetailOffer",
				"discountCode": "SAVE10",
				"price":        "49.00",
			},
		},
	})

	ctx := stepCtxWithRemoteID(body, "food-finder-bap.beckn.one")
	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("Mediate() unexpected error: %v", err)
	}

	msg := messageFields(t, ctx.Body)

	// RetailConsideration translation: currency → currencyCode added
	if msg["currencyCode"] != "USD" {
		t.Errorf("expected currencyCode=USD after RetailConsideration translation, got %v", msg["currencyCode"])
	}

	// Nested RetailOffer: price should be preserved
	offer, ok := msg["offer"].(map[string]any)
	if !ok {
		t.Fatalf("expected offer nested object in translated message")
	}
	if offer["price"] != "49.00" {
		t.Errorf("expected offer.price=49.00 preserved, got %v", offer["price"])
	}
}

// --- S6: onFailure=passThrough when artifact server returns an error ---
//
// Uses an httptest server that is closed before Mediate is called, so the
// artifact fetch gets an immediate "connection refused" error. With
// onFailure=passThrough the payload should pass through unchanged.
func TestIntegration_ArtifactUnreachable_PassThrough(t *testing.T) {
	// Start then immediately close the server so any connection attempt fails fast.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedBaseURL := srv.URL + "/schema/RetailConsideration"
	srv.Close()

	failManifest := localManifestWith(model.SchemaObject{
		Type:              "RetailConsideration",
		BaseURL:           closedBaseURL,
		SupportedVersions: []string{"v2.2"},
	})
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{"action": "translate", "onFailure": "passThrough"}, failManifest)

	body := considerationPayload("v2.1", map[string]any{"currency": "USD"})
	ctx := stepCtxWithRemoteID(body, "food-finder-bap.beckn.one")

	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("expected passThrough on failed artifact fetch, got error: %v", err)
	}
	msg := messageFields(t, ctx.Body)
	if msg["currency"] != "USD" {
		t.Errorf("expected original currency field preserved on passThrough, got %v", msg["currency"])
	}
}
