package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test projectPath

func TestProjectPath_EmptyParts(t *testing.T) {
	root := map[string]interface{}{"a": "v"}
	got, ok := projectPath(root, nil)
	require.True(t, ok)
	assert.Equal(t, root, got)

	got, ok = projectPath(root, []string{})
	require.True(t, ok)
	assert.Equal(t, root, got)
}

func TestProjectPath_MapSingleLevel(t *testing.T) {
	root := map[string]interface{}{"context": map[string]interface{}{"action": "search"}}
	got, ok := projectPath(root, []string{"context"})
	require.True(t, ok)
	assert.Equal(t, map[string]interface{}{"context": map[string]interface{}{"action": "search"}}, got)
}

func TestProjectPath_MapNested(t *testing.T) {
	root := map[string]interface{}{
		"context": map[string]interface{}{
			"action": "select",
			"transaction_id": "tx-1",
		},
	}
	got, ok := projectPath(root, []string{"context", "action"})
	require.True(t, ok)
	assert.Equal(t, map[string]interface{}{"context": map[string]interface{}{"action": "select"}}, got)
}

func TestProjectPath_MissingKey(t *testing.T) {
	root := map[string]interface{}{"context": map[string]interface{}{"action": "search"}}
	got, ok := projectPath(root, []string{"context", "missing"})
	require.False(t, ok)
	assert.Nil(t, got)
}

func TestProjectPath_ArrayTraverseAndProject(t *testing.T) {
	root := map[string]interface{}{
		"message": map[string]interface{}{
			"order": map[string]interface{}{
				"beckn:orderItems": []interface{}{
					map[string]interface{}{"beckn:orderedItem": "item-1"},
					map[string]interface{}{"beckn:orderedItem": "item-2"},
				},
			},
		},
	}
	parts := []string{"message", "order", "beckn:orderItems", "beckn:orderedItem"}
	got, ok := projectPath(root, parts)
	require.True(t, ok)

	expected := map[string]interface{}{
		"message": map[string]interface{}{
			"order": map[string]interface{}{
				"beckn:orderItems": []interface{}{
					map[string]interface{}{"beckn:orderedItem": "item-1"},
					map[string]interface{}{"beckn:orderedItem": "item-2"},
				},
			},
		},
	}
	assert.Equal(t, expected, got)
}

func TestProjectPath_NonMapOrSlice(t *testing.T) {
	_, ok := projectPath("string", []string{"a"})
	require.False(t, ok)

	_, ok = projectPath(42, []string{"a"})
	require.False(t, ok)
}

func TestProjectPath_EmptyArray(t *testing.T) {
	root := map[string]interface{}{"items": []interface{}{}}
	got, ok := projectPath(root, []string{"items", "id"})
	require.False(t, ok)
	assert.Nil(t, got)
}

// Test deepMerge

func TestDeepMerge_NilDst(t *testing.T) {
	src := map[string]interface{}{"a": 1}
	got := deepMerge(nil, src)
	assert.Equal(t, src, got)
}

func TestDeepMerge_MapIntoMap(t *testing.T) {
	dst := map[string]interface{}{"a": 1, "b": 2}
	src := map[string]interface{}{"b": 20, "c": 3}
	got := deepMerge(dst, src)
	assert.Equal(t, map[string]interface{}{"a": 1, "b": 20, "c": 3}, got)
}

func TestDeepMerge_MapNested(t *testing.T) {
	dst := map[string]interface{}{
		"context": map[string]interface{}{"action": "search", "domain": "retail"},
	}
	src := map[string]interface{}{
		"context": map[string]interface{}{"action": "search", "transaction_id": "tx-1"},
	}
	got := deepMerge(dst, src)
	ctx, ok := got.(map[string]interface{})["context"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "search", ctx["action"])
	assert.Equal(t, "retail", ctx["domain"])
	assert.Equal(t, "tx-1", ctx["transaction_id"])
}

func TestDeepMerge_ArrayIntoArray(t *testing.T) {
	dst := []interface{}{
		map[string]interface{}{"id": "a"},
		map[string]interface{}{"id": "b"},
	}
	src := []interface{}{
		map[string]interface{}{"id": "a", "name": "A"},
		map[string]interface{}{"id": "b", "name": "B"},
	}
	got := deepMerge(dst, src)
	sl, ok := got.([]interface{})
	require.True(t, ok)
	require.Len(t, sl, 2)
	assert.Equal(t, map[string]interface{}{"id": "a", "name": "A"}, sl[0])
	assert.Equal(t, map[string]interface{}{"id": "b", "name": "B"}, sl[1])
}

func TestDeepMerge_ArraySrcLonger(t *testing.T) {
	dst := []interface{}{map[string]interface{}{"a": 1}}
	src := []interface{}{
		map[string]interface{}{"a": 1},
		map[string]interface{}{"a": 2},
	}
	got := deepMerge(dst, src)
	sl, ok := got.([]interface{})
	require.True(t, ok)
	require.Len(t, sl, 2)
}

func TestDeepMerge_ScalarSrc(t *testing.T) {
	dst := map[string]interface{}{"a": 1}
	src := "overwrite"
	got := deepMerge(dst, src)
	assert.Equal(t, "overwrite", got)
}

// Test getFieldForAction and selectAuditPayload (require loaded rules via temp file)

func writeAuditRulesFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit-fields.yaml")
	err := os.WriteFile(path, []byte(content), 0600)
	require.NoError(t, err)
	return path
}

func TestGetFieldForAction_ActionMatch(t *testing.T) {
	ctx := context.Background()
	path := writeAuditRulesFile(t, `
auditRules:
  default:
    - context.transaction_id
    - context.action
  search:
    - context.action
    - context.timestamp
  select:
    - context.action
    - message.order
`)
	require.NoError(t, LoadAuditFieldRules(ctx, path))

	fields := getFieldForAction(ctx, "search")
	assert.Equal(t, []string{"context.action", "context.timestamp"}, fields)

	fields = getFieldForAction(ctx, "select")
	assert.Equal(t, []string{"context.action", "message.order"}, fields)
}

func TestGetFieldForAction_FallbackToDefault(t *testing.T) {
	ctx := context.Background()
	path := writeAuditRulesFile(t, `
auditRules:
  default:
    - context.transaction_id
    - context.message_id
  search:
    - context.action
`)
	require.NoError(t, LoadAuditFieldRules(ctx, path))

	fields := getFieldForAction(ctx, "unknown_action")
	assert.Equal(t, []string{"context.transaction_id", "context.message_id"}, fields)

	fields = getFieldForAction(ctx, "")
	assert.Equal(t, []string{"context.transaction_id", "context.message_id"}, fields)
}

func TestGetFieldForAction_EmptyDefault(t *testing.T) {
	ctx := context.Background()
	path := writeAuditRulesFile(t, `
auditRules:
  default: []
  search:
    - context.action
`)
	require.NoError(t, LoadAuditFieldRules(ctx, path))

	fields := getFieldForAction(ctx, "other")
	assert.Empty(t, fields)
}

func TestSelectAuditPayload_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	path := writeAuditRulesFile(t, `
auditRules:
  default:
    - context.action
`)
	require.NoError(t, LoadAuditFieldRules(ctx, path))

	got := selectAuditPayload(ctx, []byte("not json"))
	assert.Nil(t, got)
}

func TestSelectAuditPayload_NoRulesLoaded(t *testing.T) {
	ctx := context.Background()
	// use a fresh context without loading any rules; auditRules may be from previous test
	path := writeAuditRulesFile(t, `
auditRules:
  default: []
`)
	require.NoError(t, LoadAuditFieldRules(ctx, path))

	body := []byte(`{"context":{"action":"search"}}`)
	got := selectAuditPayload(ctx, body)
	assert.Nil(t, got)
}

func TestSelectAuditPayload_ContextAndActionOnly(t *testing.T) {
	ctx := context.Background()
	path := writeAuditRulesFile(t, `
auditRules:
  default:
    - context.transaction_id
    - context.message_id
    - context.action
`)
	require.NoError(t, LoadAuditFieldRules(ctx, path))

	body := []byte(`{
		"context": {
			"action": "search",
			"transaction_id": "tx-1",
			"message_id": "msg-1",
			"domain": "retail"
		},
		"message": {"intent": "buy"}
	}`)
	got := selectAuditPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))
	ctxMap, ok := out["context"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "search", ctxMap["action"])
	assert.Equal(t, "tx-1", ctxMap["transaction_id"])
	assert.Equal(t, "msg-1", ctxMap["message_id"])
	_, hasMessage := out["message"]
	assert.False(t, hasMessage)
}

func TestSelectAuditPayload_ActionSpecificRules(t *testing.T) {
	ctx := context.Background()
	path := writeAuditRulesFile(t, `
auditRules:
  default:
    - context.action
  search:
    - context.action
    - context.timestamp
    - message.intent
`)
	require.NoError(t, LoadAuditFieldRules(ctx, path))

	body := []byte(`{
		"context": {"action": "search", "timestamp": "2024-01-15T10:30:00Z", "domain": "retail"},
		"message": {"intent": {"item": {"id": "x"}}}
	}`)
	got := selectAuditPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))
	ctxMap := out["context"].(map[string]interface{})
	assert.Equal(t, "search", ctxMap["action"])
	assert.Equal(t, "2024-01-15T10:30:00Z", ctxMap["timestamp"])
	msg := out["message"].(map[string]interface{})
	assert.NotNil(t, msg["intent"])
}

func TestSelectAuditPayload_ArrayFieldProjection(t *testing.T) {
	ctx := context.Background()
	path := writeAuditRulesFile(t, `
auditRules:
  default:
    - context.action
  select:
    - context.transaction_id
    - context.action
    - message.order.beckn:orderItems.beckn:orderedItem
`)
	require.NoError(t, LoadAuditFieldRules(ctx, path))

	body := []byte(`{
		"context": {"action": "select", "transaction_id": "tx-2"},
		"message": {
			"order": {
				"beckn:orderItems": [
					{"beckn:orderedItem": "item-A", "other": "x"},
					{"beckn:orderedItem": "item-B", "other": "y"}
				]
			}
		}
	}`)
	got := selectAuditPayload(ctx, body)
	require.NotNil(t, got)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))
	ctxMap := out["context"].(map[string]interface{})
	assert.Equal(t, "select", ctxMap["action"])
	assert.Equal(t, "tx-2", ctxMap["transaction_id"])

	order := out["message"].(map[string]interface{})["order"].(map[string]interface{})
	items := order["beckn:orderItems"].([]interface{})
	require.Len(t, items, 2)
	assert.Equal(t, map[string]interface{}{"beckn:orderedItem": "item-A"}, items[0])
	assert.Equal(t, map[string]interface{}{"beckn:orderedItem": "item-B"}, items[1])
}

// TestSelectAuditPayload_SelectOrderExample uses a full select request payload and
// select audit rules to verify that only configured fields are projected into the
// audit log body. The request mirrors a real select with context, message.order,
// beckn:orderItems (array), beckn:acceptedOffer, and beckn:orderAttributes.
// Rules include array traversal (e.g. message.order.beckn:orderItems.beckn:orderedItem
// projects that field from each array element) and nested paths like
// message.order.beckn:orderItems.beckn:acceptedOffer.beckn:price.value.
func TestSelectAuditPayload_SelectOrderExample(t *testing.T) {
	ctx := context.Background()
	path := writeAuditRulesFile(t, `
auditRules:
  default: []
  select:
    - context.transaction_id
    - context.message_id
    - context.action
    - context.timestamp
    - message.order
    - message.order.beckn:seller
    - message.order.beckn:buyer
    - message.order.beckn:buyer.beckn:id
    - message.order.beckn:orderItems
    - message.order.beckn:orderItems.beckn:orderedItem
    - message.order.beckn:orderItems.beckn:acceptedOffer
    - message.order.beckn:orderItems.beckn:acceptedOffer.beckn:id
    - message.order.beckn:orderItems.beckn:acceptedOffer.beckn:price
    - message.order.beckn:orderItems.beckn:acceptedOffer.beckn:price.value
    - message.order.beckn:orderAttributes
    - message.order.beckn:orderAttributes.preferences
    - message.order.beckn:orderAttributes.preferences.startTime
`)
	require.NoError(t, LoadAuditFieldRules(ctx, path))

	// Full select request example: context (version, action, domain, timestamp, ids, URIs, ttl)
	// and message.order with orderStatus, seller, buyer, orderItems array (orderedItem, quantity,
	// acceptedOffer with id, descriptor, items, provider, price), orderAttributes (buyerFinderFee, preferences).
	body := []byte(`{
  "context": {
    "version": "1.0.0",
    "action": "select",
    "domain": "ev_charging",
    "timestamp": "2024-01-15T10:30:00Z",
    "message_id": "bb9f86db-9a3d-4e9c-8c11-81c8f1a7b901",
    "transaction_id": "2b4d69aa-22e4-4c78-9f56-5a7b9e2b2002",
    "bap_id": "bap.example.com",
    "bap_uri": "https://bap.example.com",
    "ttl": "PT30S",
    "bpp_id": "bpp.example.com",
    "bpp_uri": "https://bpp.example.com"
  },
  "message": {
    "order": {
      "@context": "https://raw.githubusercontent.com/beckn/protocol-specifications-new/refs/heads/main/schema/core/v2/context.jsonld",
      "@type": "beckn:Order",
      "beckn:orderStatus": "CREATED",
      "beckn:seller": "ecopower-charging",
      "beckn:buyer": {
        "@context": "https://raw.githubusercontent.com/beckn/protocol-specifications-new/refs/heads/main/schema/core/v2/context.jsonld",
        "@type": "beckn:Buyer",
        "beckn:id": "user-123",
        "beckn:role": "BUYER",
        "beckn:displayName": "Ravi Kumar",
        "beckn:telephone": "+91-9876543210",
        "beckn:email": "ravi.kumar@example.com",
        "beckn:taxID": "GSTIN29ABCDE1234F1Z5"
      },
      "beckn:orderItems": [
        {
          "beckn:orderedItem": "IND*ecopower-charging*cs-01*IN*ECO*BTM*01*CCS2*A*CCS2-A",
          "beckn:quantity": {
            "unitText": "Kilowatt Hour",
            "unitCode": "KWH",
            "unitQuantity": 2.5
          },
          "beckn:acceptedOffer": {
            "@context": "https://raw.githubusercontent.com/beckn/protocol-specifications-new/refs/heads/main/schema/core/v2/context.jsonld",
            "@type": "beckn:Offer",
            "beckn:id": "offer-ccs2-60kw-kwh",
            "beckn:descriptor": {
              "@type": "beckn:Descriptor",
              "schema:name": "Per-kWh Tariff - CCS2 60kW"
            },
            "beckn:items": [
              "IND*ecopower-charging*cs-01*IN*ECO*BTM*01*CCS2*A*CCS2-A"
            ],
            "beckn:provider": "ecopower-charging",
            "beckn:price": {
              "currency": "INR",
              "value": 45.0,
              "applicableQuantity": {
                "unitText": "Kilowatt Hour",
                "unitCode": "KWH",
                "unitQuantity": 1
              }
            }
          }
        }
      ],
      "beckn:orderAttributes": {
        "@context": "https://raw.githubusercontent.com/beckn/protocol-specifications-new/refs/heads/main/schema/EvChargingSession/v1/context.jsonld",
        "@type": "ChargingSession",
        "buyerFinderFee": {
          "feeType": "PERCENTAGE",
          "feeValue": 2.5
        },
        "preferences": {
          "startTime": "2026-01-04T08:00:00+05:30",
          "endTime": "2026-01-04T20:00:00+05:30"
        }
      }
    }
  }
}`)
	got := selectAuditPayload(ctx, body)
	require.NotNil(t, got, "selectAuditPayload should return projected body for select action")

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &out))

	// Context: only transaction_id, message_id, action, timestamp
	ctxMap, ok := out["context"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "select", ctxMap["action"])
	assert.Equal(t, "2b4d69aa-22e4-4c78-9f56-5a7b9e2b2002", ctxMap["transaction_id"])
	assert.Equal(t, "bb9f86db-9a3d-4e9c-8c11-81c8f1a7b901", ctxMap["message_id"])
	assert.Equal(t, "2024-01-15T10:30:00Z", ctxMap["timestamp"])
	_, hasBapID := ctxMap["bap_id"]
	assert.False(t, hasBapID, "context should not include bap_id when not in audit rules")

	// message.order: full order merged with projected array fields
	msg, ok := out["message"].(map[string]interface{})
	require.True(t, ok)
	order, ok := msg["order"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "ecopower-charging", order["beckn:seller"])
	buyer, ok := order["beckn:buyer"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "user-123", buyer["beckn:id"])

	// beckn:orderItems: array with projected fields from each element (beckn:orderedItem, beckn:acceptedOffer with id, price, price.value)
	items, ok := order["beckn:orderItems"].([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	item0, ok := items[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "IND*ecopower-charging*cs-01*IN*ECO*BTM*01*CCS2*A*CCS2-A", item0["beckn:orderedItem"])
	acceptedOffer, ok := item0["beckn:acceptedOffer"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "offer-ccs2-60kw-kwh", acceptedOffer["beckn:id"])
	price, ok := acceptedOffer["beckn:price"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 45.0, price["value"])

	// beckn:orderAttributes: only preferences and preferences.startTime
	orderAttrs, ok := order["beckn:orderAttributes"].(map[string]interface{})
	require.True(t, ok)
	prefs, ok := orderAttrs["preferences"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "2026-01-04T08:00:00+05:30", prefs["startTime"])
}
