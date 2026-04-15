package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetConfig clears the package-level compiled config between tests so they
// do not interfere with each other through shared global state.
func resetConfig(t *testing.T) {
	t.Helper()
	compiledCfgMu.Lock()
	compiledCfg = nil
	compiledCfgMu.Unlock()
}

// serveYAML starts a test HTTP server that returns the given YAML string.
func serveYAML(t *testing.T, yaml string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml")
		_, _ = w.Write([]byte(yaml))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// unmarshalJSON is a test helper that unmarshals JSON bytes and fails the test on error.
func unmarshalJSON(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

// ── LoadAuditConfig ──────────────────────────────────────────────────────────

func TestLoadAuditConfig_FullMode(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns:
  email:
    maskType: replace
    mask: "***@***.***"
maskRules:
  - keys: [email]
    pattern: email
pathOverrides: []
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))
	cfg := GetCompiledConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "full", cfg.mode)
	assert.NotNil(t, cfg.keyToPattern["email"])
	assert.Nil(t, cfg.keepPaths, "keepPaths must be nil for full mode")
}

func TestLoadAuditConfig_SelectiveMode(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: selective
patterns: {}
maskRules: []
selectedFields:
  default:
    - context.transactionId
    - context.action
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))
	cfg := GetCompiledConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "selective", cfg.mode)
	require.NotNil(t, cfg.keepPaths["default"])
	_, hasExact := cfg.keepPaths["default"]["context.transactionId"]
	_, hasAnc := cfg.keepPaths["default"]["context"]
	assert.True(t, hasExact, "exact path must be in keep set")
	assert.True(t, hasAnc, "ancestor path must be in keep set")
}

func TestLoadAuditConfig_DefaultsToFull(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
patterns: {}
maskRules: []
`) // no mode field
	require.NoError(t, LoadAuditConfig(context.Background(), url))
	assert.Equal(t, "full", GetCompiledConfig().mode)
}

func TestLoadAuditConfig_UnknownMode(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: verbose
patterns: {}
maskRules: []
`)
	require.Error(t, LoadAuditConfig(context.Background(), url))
}

func TestLoadAuditConfig_MaskRuleUnknownPattern(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns: {}
maskRules:
  - keys: [email]
    pattern: nonexistent
`)
	require.Error(t, LoadAuditConfig(context.Background(), url))
}

func TestLoadAuditConfig_PathOverrideUnknownPattern(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns: {}
maskRules: []
pathOverrides:
  - path: context.someField
    pattern: nonexistent
`)
	require.Error(t, LoadAuditConfig(context.Background(), url))
}

func TestLoadAuditConfig_EmptySource(t *testing.T) {
	resetConfig(t)
	require.Error(t, LoadAuditConfig(context.Background(), ""))
}

// ── ProcessAuditPayload — full mode ─────────────────────────────────────────

func TestProcessAuditPayload_FullMode_MasksEmailByKeyName(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns:
  email:
    maskType: replace
    mask: "***@***.***"
maskRules:
  - keys: [email, supportEmail]
    pattern: email
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	body := []byte(`{
		"context": {"action": "confirm", "transactionId": "txn-1"},
		"message": {
			"order": {
				"buyer": {"email": "buyer@example.com", "id": "u1"},
				"support": {"supportEmail": "help@acme.com", "phone": "9999"}
			}
		}
	}`)

	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))

	order := out["message"].(map[string]interface{})["order"].(map[string]interface{})
	buyer := order["buyer"].(map[string]interface{})
	support := order["support"].(map[string]interface{})

	assert.Equal(t, "***@***.***", buyer["email"], "buyer.email should be masked")
	assert.Equal(t, "u1", buyer["id"], "buyer.id should be unchanged")
	assert.Equal(t, "***@***.***", support["supportEmail"], "support.supportEmail should be masked")
	assert.Equal(t, "9999", support["phone"], "phone is not in maskRules — should be unchanged")
	assert.Equal(t, "txn-1", out["context"].(map[string]interface{})["transactionId"])
}

func TestProcessAuditPayload_FullMode_MasksPhoneLast4(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns:
  phone:
    maskType: last4
maskRules:
  - keys: [phone, telephone]
    pattern: phone
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	body := []byte(`{"message":{"buyer":{"telephone":"+91-9876543210","id":"u1"}}}`)
	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))

	buyer := out["message"].(map[string]interface{})["buyer"].(map[string]interface{})
	assert.Equal(t, "**********3210", buyer["telephone"])
	assert.Equal(t, "u1", buyer["id"])
}

func TestProcessAuditPayload_FullMode_MasksAcrossDepths(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns:
  sensitive:
    maskType: replace
    mask: "[REDACTED]"
maskRules:
  - keys: [email]
    pattern: sensitive
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	// email appears at different depths — both must be masked.
	body := []byte(`{
		"a": {"email": "a@a.com"},
		"b": {"c": {"email": "b@b.com"}},
		"d": {"e": {"f": {"email": "c@c.com"}}}
	}`)
	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))

	assert.Equal(t, "[REDACTED]", out["a"].(map[string]interface{})["email"])
	assert.Equal(t, "[REDACTED]", out["b"].(map[string]interface{})["c"].(map[string]interface{})["email"])
	assert.Equal(t, "[REDACTED]", out["d"].(map[string]interface{})["e"].(map[string]interface{})["f"].(map[string]interface{})["email"])
}

func TestProcessAuditPayload_FullMode_ArrayTraversal(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns:
  email:
    maskType: replace
    mask: "***@***.***"
maskRules:
  - keys: [email]
    pattern: email
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	body := []byte(`{
		"message": {
			"order": {
				"items": [
					{"buyer": {"email": "a@b.com", "id": "u1"}},
					{"buyer": {"email": "c@d.com", "id": "u2"}}
				]
			}
		}
	}`)

	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))
	items := out["message"].(map[string]interface{})["order"].(map[string]interface{})["items"].([]interface{})
	require.Len(t, items, 2)

	for i, item := range items {
		buyer := item.(map[string]interface{})["buyer"].(map[string]interface{})
		assert.Equal(t, "***@***.***", buyer["email"], "email in item %d should be masked", i)
		assert.NotEqual(t, "***@***.***", buyer["id"], "id in item %d should not be masked", i)
	}
}

func TestProcessAuditPayload_FullMode_PathOverride(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns:
  sensitive:
    maskType: replace
    mask: "[REDACTED]"
maskRules: []
pathOverrides:
  - path: context.internalRef
    pattern: sensitive
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	body := []byte(`{"context":{"action":"confirm","internalRef":"ref-secret-42"},"message":{}}`)
	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))

	ctx := out["context"].(map[string]interface{})
	assert.Equal(t, "[REDACTED]", ctx["internalRef"])
	assert.Equal(t, "confirm", ctx["action"], "action should be unchanged")
}

// ── ProcessAuditPayload — selective mode ────────────────────────────────────

func TestProcessAuditPayload_SelectiveMode_DropUnlistedFields(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: selective
patterns: {}
maskRules: []
selectedFields:
  default:
    - context.transactionId
    - context.action
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	body := []byte(`{
		"context": {
			"transactionId": "txn-1",
			"messageId": "msg-1",
			"action": "confirm",
			"domain": "retail"
		},
		"message": {"order": {"id": "ord-1"}}
	}`)

	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))
	ctx := out["context"].(map[string]interface{})

	assert.Equal(t, "txn-1", ctx["transactionId"], "transactionId must be kept")
	assert.Equal(t, "confirm", ctx["action"], "action must be kept")
	assert.Nil(t, ctx["messageId"], "messageId is not selected — must be dropped")
	assert.Nil(t, ctx["domain"], "domain is not selected — must be dropped")
	assert.Nil(t, out["message"], "message is not selected — must be dropped")
}

func TestProcessAuditPayload_SelectiveMode_PerActionOverridesDefault(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: selective
patterns: {}
maskRules: []
selectedFields:
  default:
    - context.transactionId
    - context.action
  confirm:
    - context.transactionId
    - context.action
    - message.order.id
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	body := []byte(`{
		"context": {"transactionId": "txn-1", "action": "confirm", "domain": "retail"},
		"message": {"order": {"id": "ord-99", "status": "ACTIVE"}}
	}`)

	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))
	order := out["message"].(map[string]interface{})["order"].(map[string]interface{})

	assert.Equal(t, "ord-99", order["id"], "message.order.id is in confirm list — must be kept")
	assert.Nil(t, order["status"], "message.order.status is not in confirm list — must be dropped")
	assert.Nil(t, out["context"].(map[string]interface{})["domain"])
}

func TestProcessAuditPayload_SelectiveMode_FallsBackToDefault(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: selective
patterns: {}
maskRules: []
selectedFields:
  default:
    - context.transactionId
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	// action is "search" — not listed in selectedFields, so default applies.
	body := []byte(`{"context":{"transactionId":"txn-1","action":"search","domain":"ev"}}`)
	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))
	ctx := out["context"].(map[string]interface{})

	assert.Equal(t, "txn-1", ctx["transactionId"])
	assert.Nil(t, ctx["action"], "action not in default list — must be dropped")
	assert.Nil(t, ctx["domain"])
}

func TestProcessAuditPayload_SelectiveMode_NoSelectedFieldsPassThrough(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: selective
patterns: {}
maskRules: []
`) // no selectedFields — keep set is nil → pass through
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	body := []byte(`{"context":{"transactionId":"txn-1","action":"search"}}`)
	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))
	ctx := out["context"].(map[string]interface{})

	assert.Equal(t, "txn-1", ctx["transactionId"], "no selectedFields → full pass-through")
	assert.Equal(t, "search", ctx["action"])
}

func TestProcessAuditPayload_SelectiveMode_MaskingAppliedAfterSelection(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: selective
patterns:
  email:
    maskType: replace
    mask: "***@***.***"
maskRules:
  - keys: [email]
    pattern: email
selectedFields:
  default:
    - context.transactionId
    - message.buyer.email
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	body := []byte(`{
		"context": {"transactionId": "txn-1", "action": "init", "domain": "retail"},
		"message": {
			"buyer": {"email": "secret@example.com", "name": "Alice"},
			"order": {"id": "ord-1"}
		}
	}`)

	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))
	ctx := out["context"].(map[string]interface{})
	buyer := out["message"].(map[string]interface{})["buyer"].(map[string]interface{})

	assert.Equal(t, "txn-1", ctx["transactionId"])
	assert.Nil(t, ctx["domain"], "domain not selected — must be dropped")
	// email is selected AND in maskRules — key-name masking fires before selection check
	assert.Equal(t, "***@***.***", buyer["email"])
	assert.Nil(t, buyer["name"], "name not in selectedFields — must be dropped")
	assert.Nil(t, out["message"].(map[string]interface{})["order"], "order not in selectedFields — must be dropped")
}

// ── Edge cases ───────────────────────────────────────────────────────────────

func TestProcessAuditPayload_NilBodyReturnsNil(t *testing.T) {
	resetConfig(t)
	assert.Equal(t, []byte(nil), ProcessAuditPayload(context.Background(), nil))
}

func TestProcessAuditPayload_EmptyBodyReturnsEmpty(t *testing.T) {
	resetConfig(t)
	assert.Equal(t, []byte{}, ProcessAuditPayload(context.Background(), []byte{}))
}

func TestProcessAuditPayload_NoConfigPassThrough(t *testing.T) {
	resetConfig(t)
	body := []byte(`{"context":{"action":"search"}}`)
	assert.Equal(t, body, ProcessAuditPayload(context.Background(), body))
}

func TestProcessAuditPayload_InvalidJSONReturnsOriginal(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns:
  sensitive:
    maskType: replace
    mask: "[REDACTED]"
maskRules:
  - keys: [name]
    pattern: sensitive
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	body := []byte("not valid json")
	// Should return the original body, not nil or panic.
	assert.Equal(t, body, ProcessAuditPayload(context.Background(), body))
}

func TestProcessAuditPayload_MissingPathIsNoOp(t *testing.T) {
	resetConfig(t)
	url := serveYAML(t, `
mode: full
patterns:
  email:
    maskType: replace
    mask: "***@***.***"
maskRules:
  - keys: [email]
    pattern: email
`)
	require.NoError(t, LoadAuditConfig(context.Background(), url))

	// Payload has no email field — should come through untouched.
	body := []byte(`{"context":{"action":"search"},"message":{"id":"m1"}}`)
	out := unmarshalJSON(t, ProcessAuditPayload(context.Background(), body))
	assert.Equal(t, "search", out["context"].(map[string]interface{})["action"])
	assert.Equal(t, "m1", out["message"].(map[string]interface{})["id"])
}

// ── applyMask / maskLast4 unit tests ────────────────────────────────────────

func TestMaskLast4(t *testing.T) {
	assert.Equal(t, "***", maskLast4("abc"))
	assert.Equal(t, "****", maskLast4("abcd"))
	assert.Equal(t, "*bcde", maskLast4("abcde"))
	assert.Equal(t, "**********3210", maskLast4("+91-9876543210"))
}

func TestApplyMask_NilPatternReturnsDefault(t *testing.T) {
	assert.Equal(t, defaultMask, applyMask("anything", nil))
}

func TestApplyMask_Replace(t *testing.T) {
	p := &CompiledPattern{MaskType: "replace", Mask: "***@***.***"}
	assert.Equal(t, "***@***.***", applyMask("real@email.com", p))
}

func TestApplyMask_ReplaceEmptyMaskFallsBackToDefault(t *testing.T) {
	p := &CompiledPattern{MaskType: "replace", Mask: ""}
	assert.Equal(t, defaultMask, applyMask("value", p))
}

func TestApplyMask_Last4(t *testing.T) {
	p := &CompiledPattern{MaskType: "last4"}
	assert.Equal(t, "**3456", applyMask("123456", p))
}

func TestApplyMask_NonStringValue(t *testing.T) {
	p := &CompiledPattern{MaskType: "replace", Mask: "[REDACTED]"}
	assert.Equal(t, "[REDACTED]", applyMask(42, p))
	assert.Equal(t, "[REDACTED]", applyMask(true, p))
}
