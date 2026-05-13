package payloadstore

import (
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

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

func TestRoundTrip_NoCompress(t *testing.T) {
	entry := testEntry()
	raw, err := marshalEntry(entry, false)
	if err != nil {
		t.Fatalf("marshalEntry: %v", err)
	}
	got, err := unmarshalEntry(raw, false)
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
	got, err := unmarshalEntry(raw, true)
	if err != nil {
		t.Fatalf("unmarshalEntry (compress): %v", err)
	}
	assertEntryEqual(t, entry, got)
}

// TestPrefix_NoCompress verifies the "j:" prefix is written for uncompressed entries.
func TestPrefix_NoCompress(t *testing.T) {
	raw, err := marshalEntry(testEntry(), false)
	if err != nil {
		t.Fatalf("marshalEntry: %v", err)
	}
	if len(raw) < 2 || raw[:2] != prefixJSON {
		t.Errorf("expected %q prefix, got %q", prefixJSON, raw[:2])
	}
}

// TestPrefix_Compress verifies the "c:" prefix is written for compressed entries.
func TestPrefix_Compress(t *testing.T) {
	raw, err := marshalEntry(testEntry(), true)
	if err != nil {
		t.Fatalf("marshalEntry: %v", err)
	}
	if len(raw) < 2 || raw[:2] != prefixGzip {
		t.Errorf("expected %q prefix, got %q", prefixGzip, raw[:2])
	}
}

// TestCrossFormat_CompressedReadAsUncompressed verifies an entry written with
// compress=true can be read back even when the current config has compress=false.
func TestCrossFormat_CompressedReadAsUncompressed(t *testing.T) {
	entry := testEntry()
	raw, _ := marshalEntry(entry, true)
	got, err := unmarshalEntry(raw, false)
	if err != nil {
		t.Fatalf("unmarshalEntry: %v", err)
	}
	assertEntryEqual(t, entry, got)
}

// TestCrossFormat_UncompressedReadAsCompressed verifies an entry written with
// compress=false can be read back even when the current config has compress=true.
func TestCrossFormat_UncompressedReadAsCompressed(t *testing.T) {
	entry := testEntry()
	raw, _ := marshalEntry(entry, false)
	got, err := unmarshalEntry(raw, true)
	if err != nil {
		t.Fatalf("unmarshalEntry: %v", err)
	}
	assertEntryEqual(t, entry, got)
}

// TestLegacy_NoPrefixFallback verifies that entries written before prefix support
// (plain JSON, no "j:" prefix) are still decoded correctly.
func TestLegacy_NoPrefixFallback(t *testing.T) {
	legacyRaw := `{"MessageID":"msg1","TransactionID":"txn1","NetworkID":"net1","Action":"search","SubscriberID":"sub1","Role":"bap","RequestBody":"eyJoZWxsbyI6IndvcmxkIn0=","ResponseBody":"eyJhY2siOiJvayJ9","Signature":"sig123","Outcome":1,"OutcomeReason":"","StoredAt":"2026-01-01T00:00:00Z","ExpiresAt":"2026-01-02T00:00:00Z"}`
	got, err := unmarshalEntry(legacyRaw, false)
	if err != nil {
		t.Fatalf("unmarshalEntry legacy: %v", err)
	}
	if got.MessageID != "msg1" {
		t.Errorf("MessageID: want msg1, got %q", got.MessageID)
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
