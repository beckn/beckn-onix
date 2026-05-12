package payloadstore

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/model"
)

// stubCache is an in-memory Cache backed by a sync.Map for test use.
type stubCache struct {
	mu   sync.Map
}

func (c *stubCache) Get(_ context.Context, key string) (string, error) {
	v, ok := c.mu.Load(key)
	if !ok {
		return "", nil
	}
	return v.(string), nil
}

func (c *stubCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	c.mu.Store(key, value)
	return nil
}

func (c *stubCache) Delete(_ context.Context, key string) error {
	c.mu.Delete(key)
	return nil
}

func (c *stubCache) Clear(_ context.Context) error {
	c.mu.Range(func(k, _ any) bool { c.mu.Delete(k); return true })
	return nil
}

const testNamespace = "test-module"

func newTestStore(t *testing.T, cfgOverrides map[string]string) (*store, *stubCache) {
	t.Helper()
	cache := &stubCache{}
	cfg := map[string]string{}
	for k, v := range cfgOverrides {
		cfg[k] = v
	}
	s, _, err := New(context.Background(), cache, testNamespace, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, cache
}

func sampleEntry(msgID, txnID string) definition.PayloadEntry {
	return definition.PayloadEntry{
		MessageID:     msgID,
		TransactionID: txnID,
		NetworkID:     "net1",
		Action:        "search",
		SubscriberID:  "sub1",
		Role:          model.RoleBAP,
		RequestBody:   []byte(`{"context":{"action":"search"}}`),
		ResponseBody:  []byte(`{"message":{"ack":{"status":"ACK"}}}`),
		Outcome:       definition.OutcomeACK,
	}
}

// TestStore_SetsMessageAndIndexKeys verifies that Store writes both cache keys.
func TestStore_SetsMessageAndIndexKeys(t *testing.T) {
	s, cache := newTestStore(t, nil)
	entry := sampleEntry("msg1", "txn1")

	if err := s.Store(context.Background(), entry); err != nil {
		t.Fatalf("Store: %v", err)
	}

	msgVal, _ := cache.Get(context.Background(), msgKey(testNamespace, "msg1"))
	if msgVal == "" {
		t.Error("expected payload:msg:msg1 to be set")
	}

	idxVal, _ := cache.Get(context.Background(), txnIndexKey(testNamespace, "txn1"))
	var ids []string
	if err := json.Unmarshal([]byte(idxVal), &ids); err != nil || len(ids) != 1 || ids[0] != "msg1" {
		t.Errorf("unexpected index value: %q", idxVal)
	}
}

// TestGetByTransactionID_ReturnsEntriesInOrder verifies multiple entries come back in insertion order.
func TestGetByTransactionID_ReturnsEntriesInOrder(t *testing.T) {
	s, _ := newTestStore(t, nil)

	for _, id := range []string{"m1", "m2", "m3"} {
		if err := s.Store(context.Background(), sampleEntry(id, "txnA")); err != nil {
			t.Fatalf("Store %s: %v", id, err)
		}
	}

	entries, err := s.GetByTransactionID(context.Background(), "txnA")
	if err != nil {
		t.Fatalf("GetByTransactionID: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	for i, want := range []string{"m1", "m2", "m3"} {
		if entries[i].MessageID != want {
			t.Errorf("index %d: got MessageID %q, want %q", i, entries[i].MessageID, want)
		}
	}
}

// TestGetByTransactionID_UnknownTransaction returns nil without error.
func TestGetByTransactionID_UnknownTransaction(t *testing.T) {
	s, _ := newTestStore(t, nil)
	entries, err := s.GetByTransactionID(context.Background(), "nope")
	if err != nil || entries != nil {
		t.Errorf("expected nil, nil; got %v, %v", entries, err)
	}
}

// TestGetByMessageID_HitAndMiss covers hit, miss, and action mismatch.
func TestGetByMessageID_HitAndMiss(t *testing.T) {
	s, _ := newTestStore(t, nil)
	_ = s.Store(context.Background(), sampleEntry("msg42", "txnB"))

	got, err := s.GetByMessageID(context.Background(), "msg42", "search")
	if err != nil || got == nil {
		t.Fatalf("expected entry; got %v, %v", got, err)
	}
	if got.MessageID != "msg42" {
		t.Errorf("wrong MessageID: %q", got.MessageID)
	}

	// action mismatch
	got, err = s.GetByMessageID(context.Background(), "msg42", "on_search")
	if err != nil || got != nil {
		t.Errorf("expected nil on action mismatch; got %v, %v", got, err)
	}

	// miss
	got, err = s.GetByMessageID(context.Background(), "missing", "")
	if err != nil || got != nil {
		t.Errorf("expected nil on miss; got %v, %v", got, err)
	}
}

// TestExists_TrueAfterStore verifies Exists returns true after Store and false before.
func TestExists_TrueAfterStore(t *testing.T) {
	s, _ := newTestStore(t, nil)

	ok, err := s.Exists(context.Background(), "msgX")
	if err != nil || ok {
		t.Errorf("expected false before Store; got %v, %v", ok, err)
	}

	_ = s.Store(context.Background(), sampleEntry("msgX", "txnC"))

	ok, err = s.Exists(context.Background(), "msgX")
	if err != nil || !ok {
		t.Errorf("expected true after Store; got %v, %v", ok, err)
	}
}

// TestStore_StoreBodyFalse omits request and response bodies.
func TestStore_StoreBodyFalse(t *testing.T) {
	s, cache := newTestStore(t, map[string]string{"storeBody": "false"})
	entry := sampleEntry("msgB", "txnD")
	_ = s.Store(context.Background(), entry)

	raw, _ := cache.Get(context.Background(), msgKey(testNamespace, "msgB"))
	var stored definition.PayloadEntry
	_ = json.Unmarshal([]byte(raw), &stored)

	if stored.RequestBody != nil {
		t.Errorf("expected nil RequestBody; got %v", stored.RequestBody)
	}
	if stored.ResponseBody != nil {
		t.Errorf("expected nil ResponseBody; got %v", stored.ResponseBody)
	}
}

// TestStore_MaxBodyBytesTruncates verifies bodies are truncated, not rejected.
func TestStore_MaxBodyBytesTruncates(t *testing.T) {
	s, cache := newTestStore(t, map[string]string{"maxBodyBytes": "5"})
	entry := sampleEntry("msgT", "txnE")
	entry.RequestBody = []byte("0123456789")
	entry.ResponseBody = []byte("abcdefghij")
	_ = s.Store(context.Background(), entry)

	raw, _ := cache.Get(context.Background(), msgKey(testNamespace, "msgT"))
	var stored definition.PayloadEntry
	_ = json.Unmarshal([]byte(raw), &stored)

	if len(stored.RequestBody) != 5 {
		t.Errorf("RequestBody: expected 5 bytes, got %d", len(stored.RequestBody))
	}
	if len(stored.ResponseBody) != 5 {
		t.Errorf("ResponseBody: expected 5 bytes, got %d", len(stored.ResponseBody))
	}
}

// TestStore_IndexDedup verifies that storing the same message twice doesn't duplicate the index entry.
func TestStore_IndexDedup(t *testing.T) {
	s, cache := newTestStore(t, nil)
	entry := sampleEntry("msgD", "txnF")
	_ = s.Store(context.Background(), entry)
	_ = s.Store(context.Background(), entry)

	idxRaw, _ := cache.Get(context.Background(), txnIndexKey(testNamespace, "txnF"))
	var ids []string
	_ = json.Unmarshal([]byte(idxRaw), &ids)
	if len(ids) != 1 {
		t.Errorf("expected 1 index entry after duplicate Store, got %d", len(ids))
	}
}

// TestStore_StoredAtAndExpiresAtSet verifies timestamps are stamped at store time.
func TestStore_StoredAtAndExpiresAtSet(t *testing.T) {
	s, cache := newTestStore(t, nil)
	before := time.Now().UTC()
	_ = s.Store(context.Background(), sampleEntry("msgTS", "txnG"))
	after := time.Now().UTC()

	raw, _ := cache.Get(context.Background(), msgKey(testNamespace, "msgTS"))
	var stored definition.PayloadEntry
	_ = json.Unmarshal([]byte(raw), &stored)

	if stored.StoredAt.Before(before) || stored.StoredAt.After(after) {
		t.Errorf("StoredAt %v not between %v and %v", stored.StoredAt, before, after)
	}
	expectedExpiry := stored.StoredAt.Add(defaultTTL)
	if stored.ExpiresAt.Sub(expectedExpiry) > time.Second {
		t.Errorf("ExpiresAt %v too far from expected %v", stored.ExpiresAt, expectedExpiry)
	}
}
