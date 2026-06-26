// spike_jsonata_compose_test.go — Spike: validate that github.com/jsonata-go/jsonata
// supports single-pass multi-path expression composition over a real Beckn-shaped
// payload. This is a discovery test — it documents findings, not production behaviour.
// Run with: go test -v -run TestSpike
package schemaversionmediator

import (
	"encoding/json"
	"testing"

	"github.com/jsonata-go/jsonata"
)

// becknPayload is a minimal Beckn-shaped message containing three schema objects
// at different nesting depths — the realistic scenario for the mediator.
var becknPayload = []byte(`{
	"context": {
		"domain": "retail",
		"action": "on_search",
		"bap_id": "bap.example.com",
		"bpp_id": "bpp.example.com"
	},
	"message": {
		"@context": "https://schema.beckn.io/retail/v1.1/Order.jsonld",
		"@type": "Order",
		"id": "order-001",
		"status": "ACTIVE",
		"fulfillment": {
			"@context": "https://schema.beckn.io/retail/v1.1/Fulfillment.jsonld",
			"@type": "Fulfillment",
			"id": "ff-001",
			"type": "HOME-DELIVERY",
			"tracking": true
		},
		"quote": {
			"@context": "https://schema.beckn.io/retail/v1.1/Quote.jsonld",
			"@type": "Quote",
			"price": {
				"currency": "INR",
				"value": "1500"
			},
			"breakup": [
				{"title": "item", "price": {"currency": "INR", "value": "1200"}},
				{"title": "delivery", "price": {"currency": "INR", "value": "300"}}
			]
		}
	}
}`)

// TestSpike_SingleExpression_SinglePath verifies that a JSONata expression can
// target and transform a specific sub-path, leaving the rest of the document intact.
func TestSpike_SingleExpression_SinglePath(t *testing.T) {
	instance, err := jsonata.OpenLatest()
	if err != nil {
		t.Fatalf("failed to open jsonata instance: %v", err)
	}

	// Artifact expression for Order v1.1→v2.0: rename "status" to "state".
	// The expression reconstructs the full message.order node.
	expr, err := instance.Compile(`$merge([$, {"state": status}])`, false)
	if err != nil {
		t.Fatalf("failed to compile expression: %v", err)
	}

	// Extract just the message subtree to pass as input.
	var payload map[string]any
	json.Unmarshal(becknPayload, &payload)
	message := payload["message"].(map[string]any)
	msgBytes, _ := json.Marshal(message)

	result, err := expr.Evaluate(msgBytes, nil)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}
	t.Logf("Single-path result:\n%s", result)

	var out map[string]any
	json.Unmarshal(result, &out)
	if _, ok := out["state"]; !ok {
		t.Error("expected 'state' field in output")
	}
}

// TestSpike_ComposedExpression_MultiPath is the key spike test.
// It validates that a SINGLE JSONata expression can apply INDEPENDENT transforms
// to DIFFERENT sub-paths of a payload in ONE evaluation pass.
//
// Scenario:
//   - Order at message root: rename "status" → "state"
//   - Fulfillment at message.fulfillment: rename "type" → "fulfillment_type"
//   - Quote at message.quote: add "currency_code" field from price.currency
func TestSpike_ComposedExpression_MultiPath(t *testing.T) {
	instance, err := jsonata.OpenLatest()
	if err != nil {
		t.Fatalf("failed to open jsonata instance: %v", err)
	}

	// A single composed expression that transforms all three sub-paths in one pass.
	// JSONata $merge allows deep-merging objects; each sub-expression targets its path.
	composedExpr := `
	$merge([$, {
		"state": status,
		"fulfillment": $merge([fulfillment, {
			"fulfillment_type": fulfillment.type
		}]),
		"quote": $merge([quote, {
			"currency_code": quote.price.currency
		}])
	}])`

	expr, err := instance.Compile(composedExpr, false)
	if err != nil {
		t.Fatalf("failed to compile composed expression: %v", err)
	}

	var payload map[string]any
	json.Unmarshal(becknPayload, &payload)
	message := payload["message"].(map[string]any)
	msgBytes, _ := json.Marshal(message)

	result, err := expr.Evaluate(msgBytes, nil)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}
	t.Logf("Multi-path composed result:\n%s", result)

	var out map[string]any
	json.Unmarshal(result, &out)

	if _, ok := out["state"]; !ok {
		t.Error("SPIKE RESULT: Order transform FAILED — 'state' not found")
	} else {
		t.Log("SPIKE RESULT: Order transform OK — 'state' present")
	}

	if ff, ok := out["fulfillment"].(map[string]any); ok {
		if _, ok := ff["fulfillment_type"]; ok {
			t.Log("SPIKE RESULT: Fulfillment transform OK — 'fulfillment_type' present")
		} else {
			t.Error("SPIKE RESULT: Fulfillment transform FAILED — 'fulfillment_type' not found")
		}
	} else {
		t.Error("SPIKE RESULT: fulfillment field missing from output")
	}

	if q, ok := out["quote"].(map[string]any); ok {
		if _, ok := q["currency_code"]; ok {
			t.Log("SPIKE RESULT: Quote transform OK — 'currency_code' present")
		} else {
			t.Error("SPIKE RESULT: Quote transform FAILED — 'currency_code' not found")
		}
	} else {
		t.Error("SPIKE RESULT: quote field missing from output")
	}
}

// TestSpike_IndependentArtifacts_SequentialApply tests the ALTERNATIVE approach:
// applying artifact expressions sequentially (each to the running payload).
// This is the fallback if single-pass composition proves impractical.
func TestSpike_IndependentArtifacts_SequentialApply(t *testing.T) {
	instance, err := jsonata.OpenLatest()
	if err != nil {
		t.Fatalf("failed to open jsonata instance: %v", err)
	}

	// Artifact 1: Order transform (applied to full message)
	orderExpr, _ := instance.Compile(`$merge([$, {"state": status}])`, false)
	// Artifact 2: Fulfillment transform (applied to full message, targets sub-path)
	ffExpr, _ := instance.Compile(`$merge([$, {"fulfillment": $merge([fulfillment, {"fulfillment_type": fulfillment.type}])}])`, false)
	// Artifact 3: Quote transform (applied to full message, targets sub-path)
	quoteExpr, _ := instance.Compile(`$merge([$, {"quote": $merge([quote, {"currency_code": quote.price.currency}])}])`, false)

	var payload map[string]any
	json.Unmarshal(becknPayload, &payload)
	message := payload["message"].(map[string]any)
	current, _ := json.Marshal(message)

	for i, expr := range []jsonata.Expression{orderExpr, ffExpr, quoteExpr} {
		result, err := expr.Evaluate(current, nil)
		if err != nil {
			t.Fatalf("artifact %d evaluation failed: %v", i+1, err)
		}
		current = result
	}

	t.Logf("Sequential result:\n%s", current)

	var out map[string]any
	json.Unmarshal(current, &out)

	checks := map[string]func() bool{
		"state":      func() bool { _, ok := out["state"]; return ok },
		"fulfillment_type": func() bool {
			ff, ok := out["fulfillment"].(map[string]any)
			if !ok { return false }
			_, ok = ff["fulfillment_type"]
			return ok
		},
		"currency_code": func() bool {
			q, ok := out["quote"].(map[string]any)
			if !ok { return false }
			_, ok = q["currency_code"]
			return ok
		},
	}
	for field, check := range checks {
		if check() {
			t.Logf("SPIKE RESULT: Sequential — %q OK", field)
		} else {
			t.Errorf("SPIKE RESULT: Sequential — %q FAILED", field)
		}
	}
}

// TestSpike_JSONataPathNotation validates that JSONata path references inside
// expressions work as expected and documents which notation is correct.
func TestSpike_JSONataPathNotation(t *testing.T) {
	instance, err := jsonata.OpenLatest()
	if err != nil {
		t.Fatalf("failed to open jsonata instance: %v", err)
	}

	// Test: can we reference a deeply nested field?
	expr, err := instance.Compile(`fulfillment.type`, false)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	var payload map[string]any
	json.Unmarshal(becknPayload, &payload)
	message := payload["message"].(map[string]any)
	msgBytes, _ := json.Marshal(message)

	result, err := expr.Evaluate(msgBytes, nil)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}
	t.Logf("Path notation result for 'fulfillment.type': %s", result)
	if string(result) != `"HOME-DELIVERY"` {
		t.Errorf("expected HOME-DELIVERY, got %s", result)
	}
}
