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

func serveAuditConfigHTTP(t *testing.T, yaml string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml")
		_, _ = w.Write([]byte(yaml))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestLoadAuditConfig_PatternsAndPaths(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: email
    regex: "^[\\w.+-]+@[\\w.-]+\\.[a-zA-Z]{2,}$"
    maskType: replace
    mask: "***@***.***"
  - name: phone
    regex: "^\\+?[0-9][0-9\\-\\s]{7,15}$"
    maskType: last4
piiPaths:
  - path: message.order.beckn:buyer.beckn:email
    pattern: email
  - path: message.order.beckn:buyer.beckn:telephone
    pattern: phone
`)
	err := LoadAuditConfig(ctx, url)
	require.NoError(t, err)
	cfg := GetPIIConfig()
	require.NotNil(t, cfg)
	require.Len(t, cfg.Patterns, 2)
	require.Len(t, cfg.Paths, 2)
	assert.NotNil(t, cfg.Patterns["email"])
	assert.Equal(t, "***@***.***", cfg.Patterns["email"].Mask)
	assert.Equal(t, "replace", cfg.Patterns["email"].MaskType)
	assert.NotNil(t, cfg.Patterns["phone"])
	assert.Equal(t, "last4", cfg.Patterns["phone"].MaskType)
}

func TestLoadAuditConfig_InvalidRegex(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: bad
    regex: "[invalid"
    mask: "X"
piiPaths: []
`)
	err := LoadAuditConfig(ctx, url)
	require.Error(t, err)
}

func TestLoadAuditConfig_DefaultMaskType(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: generic
    regex: "."
    mask: "[MASKED]"
piiPaths: []
`)
	require.NoError(t, LoadAuditConfig(ctx, url))
	cfg := GetPIIConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "replace", cfg.Patterns["generic"].MaskType)
}

func TestMaskPIIInPayload_EmailReplace(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: email
    regex: "^[\\w.+-]+@[\\w.-]+\\.[a-zA-Z]{2,}$"
    maskType: replace
    mask: "***@***.***"
piiPaths:
  - path: message.order.beckn:buyer.beckn:email
    pattern: email
`)
	require.NoError(t, LoadAuditConfig(ctx, url))

	body := []byte(`{
  "context": {"action": "select", "transaction_id": "txn-1"},
  "message": {
    "order": {
      "beckn:buyer": {
        "beckn:id": "user-1",
        "beckn:email": "ravi@example.com"
      },
      "beckn:seller": "shop-1"
    }
  }
}`)

	got := MaskPIIInPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))

	buyer := out["message"].(map[string]interface{})["order"].(map[string]interface{})["beckn:buyer"].(map[string]interface{})
	assert.Equal(t, "***@***.***", buyer["beckn:email"], "email should be masked with replace pattern")
	assert.Equal(t, "user-1", buyer["beckn:id"], "non-PII field should be unchanged")
	assert.Equal(t, "shop-1", out["message"].(map[string]interface{})["order"].(map[string]interface{})["beckn:seller"])
	assert.Equal(t, "txn-1", out["context"].(map[string]interface{})["transaction_id"])
}

func TestMaskPIIInPayload_PhoneLast4(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: phone
    regex: "^\\+?[0-9][0-9\\-\\s]{7,15}$"
    maskType: last4
piiPaths:
  - path: message.order.beckn:buyer.beckn:telephone
    pattern: phone
`)
	require.NoError(t, LoadAuditConfig(ctx, url))

	body := []byte(`{
  "message": {
    "order": {
      "beckn:buyer": {
        "beckn:id": "user-1",
        "beckn:telephone": "+91-9876543210"
      }
    }
  }
}`)

	got := MaskPIIInPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))

	buyer := out["message"].(map[string]interface{})["order"].(map[string]interface{})["beckn:buyer"].(map[string]interface{})
	phone := buyer["beckn:telephone"].(string)
	assert.Equal(t, "**********3210", phone, "phone should keep last 4, mask rest with *")
	assert.Equal(t, "user-1", buyer["beckn:id"])
}

func TestMaskPIIInPayload_ArrayTraversal(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: email
    regex: "^[\\w.+-]+@[\\w.-]+\\.[a-zA-Z]{2,}$"
    maskType: replace
    mask: "***@***.***"
piiPaths:
  - path: message.order.beckn:orderItems.beckn:buyer.beckn:email
    pattern: email
`)
	require.NoError(t, LoadAuditConfig(ctx, url))

	body := []byte(`{
  "message": {
    "order": {
      "beckn:orderItems": [
        {"beckn:buyer": {"beckn:email": "a@b.com", "beckn:id": "u1"}},
        {"beckn:buyer": {"beckn:email": "c@d.com", "beckn:id": "u2"}}
      ]
    }
  }
}`)

	got := MaskPIIInPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))
	items := out["message"].(map[string]interface{})["order"].(map[string]interface{})["beckn:orderItems"].([]interface{})
	require.Len(t, items, 2)

	for i, item := range items {
		buyer := item.(map[string]interface{})["beckn:buyer"].(map[string]interface{})
		assert.Equal(t, "***@***.***", buyer["beckn:email"], "email in item %d should be masked", i)
		assert.NotEqual(t, "***@***.***", buyer["beckn:id"], "id in item %d should NOT be masked", i)
	}
}

func TestMaskPIIInPayload_SameKeyDifferentPaths(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: email
    regex: "^[\\w.+-]+@[\\w.-]+\\.[a-zA-Z]{2,}$"
    maskType: replace
    mask: "***@***.***"
  - name: phone
    regex: "^\\+?[0-9][0-9\\-\\s]{7,15}$"
    maskType: last4
piiPaths:
  - path: message.order.beckn:buyer.beckn:email
    pattern: email
  - path: message.support.email
    pattern: email
  - path: message.support.phone
    pattern: phone
`)
	require.NoError(t, LoadAuditConfig(ctx, url))

	body := []byte(`{
  "message": {
    "order": {
      "beckn:buyer": {
        "beckn:email": "buyer@example.com"
      }
    },
    "support": {
      "email": "support@acme.com",
      "phone": "+91-80-12345678",
      "name": "Acme Support"
    }
  }
}`)

	got := MaskPIIInPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))

	buyer := out["message"].(map[string]interface{})["order"].(map[string]interface{})["beckn:buyer"].(map[string]interface{})
	assert.Equal(t, "***@***.***", buyer["beckn:email"], "buyer email should be masked")

	support := out["message"].(map[string]interface{})["support"].(map[string]interface{})
	assert.Equal(t, "***@***.***", support["email"], "support email should be masked")
	assert.Equal(t, "***********5678", support["phone"], "support phone should keep last 4")
	assert.Equal(t, "Acme Support", support["name"], "name is not in piiPaths so should stay")
}

func TestMaskPIIInPayload_NoMatchDefaultMask(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: email
    regex: "^[\\w.+-]+@[\\w.-]+\\.[a-zA-Z]{2,}$"
    maskType: replace
    mask: "***@***.***"
piiPaths:
  - path: message.name
    pattern: email
`)
	require.NoError(t, LoadAuditConfig(ctx, url))

	body := []byte(`{"message":{"name":"not-an-email"}}`)
	got := MaskPIIInPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))
	assert.Equal(t, "[MASKED]", out["message"].(map[string]interface{})["name"],
		"value that does not match regex should still be masked with default mask")
}

func TestMaskPIIInPayload_NoConfig_ReturnsUnchanged(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns: []
piiPaths: []
`)
	require.NoError(t, LoadAuditConfig(ctx, url))

	body := []byte(`{"context":{"action":"select"}}`)
	got := MaskPIIInPayload(ctx, body)
	require.Equal(t, body, got)
}

func TestMaskPIIInPayload_EmptyBody(t *testing.T) {
	ctx := context.Background()
	got := MaskPIIInPayload(ctx, nil)
	require.Nil(t, got)
	got = MaskPIIInPayload(ctx, []byte{})
	require.Nil(t, got)
}

func TestMaskPIIInPayload_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: generic
    regex: "."
    maskType: replace
    mask: "[MASKED]"
piiPaths:
  - path: message.name
    pattern: generic
`)
	require.NoError(t, LoadAuditConfig(ctx, url))
	got := MaskPIIInPayload(ctx, []byte("not json"))
	require.Nil(t, got)
}

func TestMaskLast4_ShortString(t *testing.T) {
	assert.Equal(t, "***", maskLast4("abc"))
	assert.Equal(t, "****", maskLast4("abcd"))
	assert.Equal(t, "*bcde", maskLast4("abcde"))
}

func TestMaskPIIInPayload_MissingPathIsNoOp(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns:
  - name: generic
    regex: "."
    maskType: replace
    mask: "[MASKED]"
piiPaths:
  - path: message.order.beckn:buyer.beckn:email
    pattern: generic
`)
	require.NoError(t, LoadAuditConfig(ctx, url))

	body := []byte(`{"message":{"order":{"beckn:seller":"shop-1"}}}`)
	got := MaskPIIInPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))
	assert.Equal(t, "shop-1", out["message"].(map[string]interface{})["order"].(map[string]interface{})["beckn:seller"],
		"unrelated fields should be untouched when path does not exist")
}

func TestMaskPIIInPayload_NilPattern(t *testing.T) {
	ctx := context.Background()
	url := serveAuditConfigHTTP(t, `
piiPatterns: []
piiPaths:
  - path: message.name
    pattern: nonexistent
`)
	require.NoError(t, LoadAuditConfig(ctx, url))

	body := []byte(`{"message":{"name":"test"}}`)
	got := MaskPIIInPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))
	assert.Equal(t, "[MASKED]", out["message"].(map[string]interface{})["name"],
		"nil pattern should fall back to default mask")
}
