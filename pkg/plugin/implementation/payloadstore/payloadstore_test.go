package payloadstore

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// stubCache is an in-memory Cache backed by a sync.Map for test use.
type stubCache struct {
	mu sync.Map
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

const testNamespace = "onix"

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

func testEntry() definition.PayloadEntry {
	return definition.PayloadEntry{
		MessageID:     "msg1",
		TransactionID: "txn1",
		NetworkID:     "net1",
		Action:        "search",
		SubscriberID:  "sub1",
		Role:          model.RoleBAP,
		RequestBody:   []byte(`{"hello":"world"}`),
		ResponseBody:  []byte(`{"ack":"ok"}`),
		Signature:     "sig123",
		Outcome:       definition.OutcomeACK,
		OutcomeReason: "",
		StoredAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt:     time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func assertEntryEqual(t *testing.T, want, got definition.PayloadEntry) {
	t.Helper()
	if want.MessageID != got.MessageID {
		t.Errorf("MessageID: want %q got %q", want.MessageID, got.MessageID)
	}
	if want.TransactionID != got.TransactionID {
		t.Errorf("TransactionID: want %q got %q", want.TransactionID, got.TransactionID)
	}
	if want.Action != got.Action {
		t.Errorf("Action: want %q got %q", want.Action, got.Action)
	}
	if string(want.RequestBody) != string(got.RequestBody) {
		t.Errorf("RequestBody: want %q got %q", want.RequestBody, got.RequestBody)
	}
	if string(want.ResponseBody) != string(got.ResponseBody) {
		t.Errorf("ResponseBody: want %q got %q", want.ResponseBody, got.ResponseBody)
	}
	if want.Outcome != got.Outcome {
		t.Errorf("Outcome: want %v got %v", want.Outcome, got.Outcome)
	}
	if !want.StoredAt.Equal(got.StoredAt) {
		t.Errorf("StoredAt: want %v got %v", want.StoredAt, got.StoredAt)
	}
}

// Config tests

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TTL != defaultTTL {
		t.Errorf("TTL: got %v, want %v", cfg.TTL, defaultTTL)
	}
	if cfg.IndexTTL != defaultTTL+time.Hour {
		t.Errorf("IndexTTL: got %v, want %v", cfg.IndexTTL, defaultTTL+time.Hour)
	}
	if cfg.MaxBodyBytes != defaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes: got %d, want %d", cfg.MaxBodyBytes, defaultMaxBodyBytes)
	}
	if !cfg.StoreBody {
		t.Error("StoreBody: expected true by default")
	}
	if cfg.StoreSignature {
		t.Error("StoreSignature: expected false by default")
	}
	if cfg.Compress {
		t.Error("Compress: expected false by default")
	}
}

func TestParseConfig_AllFields(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"ttl":            "12h",
		"indexTTL":       "13h",
		"maxBodyBytes":   "2097152",
		"storeBody":      "false",
		"storeSignature": "true",
		"compress":       "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TTL != 12*time.Hour {
		t.Errorf("TTL: got %v", cfg.TTL)
	}
	if cfg.IndexTTL != 13*time.Hour {
		t.Errorf("IndexTTL: got %v", cfg.IndexTTL)
	}
	if cfg.MaxBodyBytes != 2097152 {
		t.Errorf("MaxBodyBytes: got %d", cfg.MaxBodyBytes)
	}
	if cfg.StoreBody {
		t.Error("StoreBody: expected false")
	}
	if !cfg.StoreSignature {
		t.Error("StoreSignature: expected true")
	}
	if !cfg.Compress {
		t.Error("Compress: expected true")
	}
}

func TestParseConfig_InvalidTTL(t *testing.T) {
	_, err := ParseConfig(map[string]string{"ttl": "notaduration"})
	if err == nil {
		t.Error("expected error for invalid ttl")
	}
}

func TestParseConfig_ZeroTTL(t *testing.T) {
	_, err := ParseConfig(map[string]string{"ttl": "0s"})
	if err == nil {
		t.Error("expected error for zero ttl")
	}
}

func TestParseConfig_InvalidBool(t *testing.T) {
	_, err := ParseConfig(map[string]string{"storeBody": "maybe"})
	if err == nil {
		t.Error("expected error for invalid storeBody")
	}
}

func TestParseConfig_InvalidMaxBodyBytes(t *testing.T) {
	_, err := ParseConfig(map[string]string{"maxBodyBytes": "abc"})
	if err == nil {
		t.Error("expected error for invalid maxBodyBytes")
	}
}

func TestParseConfig_NegativeMaxBodyBytes(t *testing.T) {
	_, err := ParseConfig(map[string]string{"maxBodyBytes": "-1"})
	if err == nil {
		t.Error("expected error for negative maxBodyBytes")
	}
}

func TestParseConfig_ZeroMaxBodyBytes(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{"maxBodyBytes": "0"})
	if err != nil {
		t.Fatalf("expected no error for maxBodyBytes=0, got: %v", err)
	}
	if cfg.MaxBodyBytes != 0 {
		t.Errorf("MaxBodyBytes: got %d, want 0", cfg.MaxBodyBytes)
	}
}

func TestParseConfig_IndexTTLShorterThanTTL(t *testing.T) {
	_, err := ParseConfig(map[string]string{
		"ttl":      "24h",
		"indexTTL": "1h",
	})
	if err == nil {
		t.Error("expected error when indexTTL < ttl")
	}
}

func TestParseConfig_IndexTTLEqualToTTL(t *testing.T) {
	_, err := ParseConfig(map[string]string{
		"ttl":      "24h",
		"indexTTL": "24h",
	})
	if err != nil {
		t.Errorf("expected no error when indexTTL == ttl, got: %v", err)
	}
}

// Serializer tests

func TestRoundTrip_NoCompress(t *testing.T) {
	entry := testEntry()
	raw, err := marshalEntry(entry, false)
	if err != nil {
		t.Fatalf("marshalEntry: %v", err)
	}
	got, err := unmarshalEntry(raw)
	if err != nil {
		t.Fatalf("unmarshalEntry: %v", err)
	}
	assertEntryEqual(t, entry, got)
}

func TestRoundTrip_Compress(t *testing.T) {
	entry := testEntry()
	raw, err := marshalEntry(entry, true)
	if err != nil {
		t.Fatalf("marshalEntry (compress): %v", err)
	}
	got, err := unmarshalEntry(raw)
	if err != nil {
		t.Fatalf("unmarshalEntry (compress): %v", err)
	}
	assertEntryEqual(t, entry, got)
}

func TestPrefix_NoCompress(t *testing.T) {
	raw, err := marshalEntry(testEntry(), false)
	if err != nil {
		t.Fatalf("marshalEntry: %v", err)
	}
	if len(raw) < 2 || raw[:2] != prefixJSON {
		t.Errorf("expected %q prefix, got %q", prefixJSON, raw[:2])
	}
}

func TestPrefix_Compress(t *testing.T) {
	raw, err := marshalEntry(testEntry(), true)
	if err != nil {
		t.Fatalf("marshalEntry: %v", err)
	}
	if len(raw) < 2 || raw[:2] != prefixGzip {
		t.Errorf("expected %q prefix, got %q", prefixGzip, raw[:2])
	}
}

func TestCrossFormat_CompressedReadAsUncompressed(t *testing.T) {
	entry := testEntry()
	raw, _ := marshalEntry(entry, true)
	got, err := unmarshalEntry(raw)
	if err != nil {
		t.Fatalf("unmarshalEntry: %v", err)
	}
	assertEntryEqual(t, entry, got)
}

func TestCrossFormat_UncompressedReadAsCompressed(t *testing.T) {
	entry := testEntry()
	raw, _ := marshalEntry(entry, false)
	got, err := unmarshalEntry(raw)
	if err != nil {
		t.Fatalf("unmarshalEntry: %v", err)
	}
	assertEntryEqual(t, entry, got)
}

func TestLegacy_NoPrefixFallback(t *testing.T) {
	legacyRaw := `{"MessageID":"msg1","TransactionID":"txn1","NetworkID":"net1","Action":"search","SubscriberID":"sub1","Role":"bap","RequestBody":"eyJoZWxsbyI6IndvcmxkIn0=","ResponseBody":"eyJhY2siOiJvayJ9","Signature":"sig123","Outcome":1,"OutcomeReason":"","StoredAt":"2026-01-01T00:00:00Z","ExpiresAt":"2026-01-02T00:00:00Z"}`
	got, err := unmarshalEntry(legacyRaw)
	if err != nil {
		t.Fatalf("unmarshalEntry legacy: %v", err)
	}
	if got.MessageID != "msg1" {
		t.Errorf("MessageID: want msg1, got %q", got.MessageID)
	}
}

// Store tests

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

func TestGetByTransactionID_UnknownTransaction(t *testing.T) {
	s, _ := newTestStore(t, nil)
	entries, err := s.GetByTransactionID(context.Background(), "nope")
	if err != nil || entries != nil {
		t.Errorf("expected nil, nil; got %v, %v", entries, err)
	}
}

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

	got, err = s.GetByMessageID(context.Background(), "msg42", "on_search")
	if err != nil || got != nil {
		t.Errorf("expected nil on action mismatch; got %v, %v", got, err)
	}

	got, err = s.GetByMessageID(context.Background(), "missing", "")
	if err != nil || got != nil {
		t.Errorf("expected nil on miss; got %v, %v", got, err)
	}
}

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

func TestStore_StoreBodyFalse(t *testing.T) {
	s, cache := newTestStore(t, map[string]string{"storeBody": "false"})
	entry := sampleEntry("msgB", "txnD")
	_ = s.Store(context.Background(), entry)

	raw, _ := cache.Get(context.Background(), msgKey(testNamespace, "msgB"))
	stored, _ := unmarshalEntry(raw)

	if stored.RequestBody != nil {
		t.Errorf("expected nil RequestBody; got %v", stored.RequestBody)
	}
	if stored.ResponseBody != nil {
		t.Errorf("expected nil ResponseBody; got %v", stored.ResponseBody)
	}
}

func TestStore_MaxBodyBytesTruncates(t *testing.T) {
	s, cache := newTestStore(t, map[string]string{"maxBodyBytes": "5"})
	entry := sampleEntry("msgT", "txnE")
	entry.RequestBody = []byte("0123456789")
	entry.ResponseBody = []byte("abcdefghij")
	_ = s.Store(context.Background(), entry)

	raw, _ := cache.Get(context.Background(), msgKey(testNamespace, "msgT"))
	stored, _ := unmarshalEntry(raw)

	if len(stored.RequestBody) != 5 {
		t.Errorf("RequestBody: expected 5 bytes, got %d", len(stored.RequestBody))
	}
	if len(stored.ResponseBody) != 5 {
		t.Errorf("ResponseBody: expected 5 bytes, got %d", len(stored.ResponseBody))
	}
}

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

func TestStore_StoredAtAndExpiresAtSet(t *testing.T) {
	s, cache := newTestStore(t, nil)
	before := time.Now().UTC()
	_ = s.Store(context.Background(), sampleEntry("msgTS", "txnG"))
	after := time.Now().UTC()

	raw, _ := cache.Get(context.Background(), msgKey(testNamespace, "msgTS"))
	stored, _ := unmarshalEntry(raw)

	if stored.StoredAt.Before(before) || stored.StoredAt.After(after) {
		t.Errorf("StoredAt %v not between %v and %v", stored.StoredAt, before, after)
	}
	expectedExpiry := stored.StoredAt.Add(defaultTTL)
	if stored.ExpiresAt.Sub(expectedExpiry) > time.Second {
		t.Errorf("ExpiresAt %v too far from expected %v", stored.ExpiresAt, expectedExpiry)
	}
}

func TestStore_EmptyTransactionID(t *testing.T) {
	s, cache := newTestStore(t, nil)
	entry := sampleEntry("msgNoTxn", "")

	if err := s.Store(context.Background(), entry); err != nil {
		t.Fatalf("Store: %v", err)
	}

	msgVal, _ := cache.Get(context.Background(), msgKey(testNamespace, "msgNoTxn"))
	if msgVal == "" {
		t.Error("expected msg key to be set even without transaction_id")
	}

	idxVal, _ := cache.Get(context.Background(), txnIndexKey(testNamespace, ""))
	if idxVal != "" {
		t.Errorf("expected no index entry for empty transaction_id, got: %q", idxVal)
	}
}

func TestExists_EmptyMessageID(t *testing.T) {
	s, _ := newTestStore(t, nil)
	ok, err := s.Exists(context.Background(), "")
	if err != nil {
		t.Errorf("expected no error for empty message_id; got %v", err)
	}
	if ok {
		t.Error("expected false for empty message_id")
	}
}


func TestGetByMessageID_EmptyMessageID(t *testing.T) {
	s, _ := newTestStore(t, nil)
	got, err := s.GetByMessageID(context.Background(), "", "")
	if err != nil || got != nil {
		t.Errorf("expected nil, nil for empty message_id; got %v, %v", got, err)
	}
}

func TestGetByTransactionID_EmptyTransactionID(t *testing.T) {
	s, _ := newTestStore(t, nil)
	entries, err := s.GetByTransactionID(context.Background(), "")
	if err != nil || entries != nil {
		t.Errorf("expected nil, nil for empty transaction_id; got %v, %v", entries, err)
	}
}
