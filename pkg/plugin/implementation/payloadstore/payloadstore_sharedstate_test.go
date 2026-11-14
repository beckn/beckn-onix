package payloadstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// stepCtxFor builds a StepContext for the given message, transaction and
// action, carrying authHeader as the Authorization header.
func stepCtxFor(msgID, txnID, action, authHeader string) *model.StepContext {
	body := fmt.Sprintf(
		`{"context":{"message_id":%q,"transaction_id":%q,"network_id":"net1","action":%q}}`,
		msgID, txnID, action,
	)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if authHeader != "" {
		req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	}
	return &model.StepContext{
		Context: context.Background(),
		Request: req,
		Body:    []byte(body),
		Role:    model.RoleBAP,
		SubID:   "sub1",
	}
}

// A callback carries the message_id of the request it answers. When both the
// caller and the receiver module run storePayload (as the shipped
// local-beckn-one configs do), storing the callback must not make the
// request's signature unreachable: validateSign and sign look it up by
// (message_id, request action) for every later callback of that message.
func TestGetByMessageID_RequestSurvivesCallbackWithSameMessageID(t *testing.T) {
	s, _ := newTestStore(t, nil)

	if err := s.Store(stepCtxFor("m1", "t1", "search", "sig-of-search")); err != nil {
		t.Fatalf("Store search: %v", err)
	}
	if err := s.Store(stepCtxFor("m1", "t1", "on_search", "sig-of-on-search")); err != nil {
		t.Fatalf("Store on_search: %v", err)
	}

	got, err := s.GetByMessageID(context.Background(), "m1", "search")
	if err != nil {
		t.Fatalf("GetByMessageID(search): %v", err)
	}
	if got == nil {
		t.Fatal("request entry lost: GetByMessageID(m1, search) = nil after the on_search callback was stored")
	}
	if got.Action != "search" || got.Signature != "sig-of-search" {
		t.Errorf("got action=%q signature=%q, want search / sig-of-search", got.Action, got.Signature)
	}

	cb, err := s.GetByMessageID(context.Background(), "m1", "on_search")
	if err != nil || cb == nil {
		t.Fatalf("GetByMessageID(on_search) = %v, %v", cb, err)
	}
	if cb.Signature != "sig-of-on-search" {
		t.Errorf("callback signature = %q", cb.Signature)
	}
}

// Order must not matter either: a request stored after a callback with the
// same message_id (BPP side: on_search sent, then a late duplicate search)
// must leave both reachable.
func TestGetByMessageID_BothActionsReachableInEitherOrder(t *testing.T) {
	s, _ := newTestStore(t, nil)
	_ = s.Store(stepCtxFor("m2", "t2", "on_select", "sig-cb"))
	_ = s.Store(stepCtxFor("m2", "t2", "select", "sig-req"))

	for action, want := range map[string]string{"select": "sig-req", "on_select": "sig-cb"} {
		got, err := s.GetByMessageID(context.Background(), "m2", action)
		if err != nil || got == nil {
			t.Fatalf("GetByMessageID(%s) = %v, %v", action, got, err)
		}
		if got.Signature != want {
			t.Errorf("%s: signature = %q, want %q", action, got.Signature, want)
		}
	}
}

// Entries written before the per-action record existed live only under the
// message key; they must still be found.
func TestGetByMessageID_FallsBackToMessageKey(t *testing.T) {
	s, cache := newTestStore(t, nil)
	e := testEntry()
	raw, err := marshalEntry(e, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = cache.Set(context.Background(), msgKey(testNamespace, e.MessageID), raw, time.Minute)

	got, err := s.GetByMessageID(context.Background(), e.MessageID, e.Action)
	if err != nil || got == nil {
		t.Fatalf("legacy entry not found: %v, %v", got, err)
	}
	assertEntryEqual(t, e, *got)
}

// An action that needs escaping must not let two (message, action) pairs
// share a record.
func TestActionKey_NoCollision(t *testing.T) {
	a := actionKey(testNamespace, "x", "a:msg:y")
	b := actionKey(testNamespace, "y:msg:x", "a")
	c := actionKey(testNamespace, "y", "a:msg:x")
	if a == b || a == c || b == c {
		t.Errorf("action keys collide: %q %q %q", a, b, c)
	}
}

// slowCache widens the read-modify-write window of the transaction index so
// a lost update shows up reliably.
type slowCache struct {
	stubCache
}

func (c *slowCache) Get(ctx context.Context, key string) (string, error) {
	v, err := c.stubCache.Get(ctx, key)
	time.Sleep(200 * time.Microsecond)
	return v, err
}

// Concurrent Store calls for one transaction must all end up in the index.
func TestStore_ConcurrentSameTransaction_IndexKeepsEveryMessage(t *testing.T) {
	cache := &slowCache{}
	s, _, err := New(context.Background(), cache, testNamespace, map[string]string{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const n = 48
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if err := s.Store(stepCtxFor(fmt.Sprintf("m%02d", i), "txn-conc", "search", "sig")); err != nil {
				t.Errorf("Store %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	raw, _ := cache.stubCache.Get(context.Background(), txnIndexKey(testNamespace, "txn-conc"))
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		t.Fatalf("index unreadable: %v (%q)", err, raw)
	}
	if len(ids) != n {
		t.Errorf("index holds %d of %d message ids: concurrent appends were lost", len(ids), n)
	}
}

// flakyIndexCache fails Get for the transaction index key while failing is set.
type flakyIndexCache struct {
	stubCache
	failing atomic.Bool
	key     string
}

func (c *flakyIndexCache) Get(ctx context.Context, key string) (string, error) {
	if c.failing.Load() && key == c.key {
		return "", errors.New("redis: i/o timeout")
	}
	return c.stubCache.Get(ctx, key)
}

// A failed read of the index is not an empty index: writing back a
// one-element list would drop every message recorded before.
func TestStore_IndexReadError_DoesNotTruncateIndex(t *testing.T) {
	cache := &flakyIndexCache{key: txnIndexKey(testNamespace, "txn-flaky")}
	s, _, err := New(context.Background(), cache, testNamespace, map[string]string{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, id := range []string{"a", "b"} {
		if err := s.Store(stepCtxFor(id, "txn-flaky", "search", "sig")); err != nil {
			t.Fatalf("Store %s: %v", id, err)
		}
	}

	cache.failing.Store(true)
	// The message itself is stored; only the index update is skipped.
	if err := s.Store(stepCtxFor("c", "txn-flaky", "search", "sig")); err != nil {
		t.Fatalf("Store during index read failure: %v", err)
	}
	cache.failing.Store(false)

	raw, _ := cache.stubCache.Get(context.Background(), txnIndexKey(testNamespace, "txn-flaky"))
	var ids []string
	_ = json.Unmarshal([]byte(raw), &ids)
	have := map[string]bool{}
	for _, id := range ids {
		have[id] = true
	}
	if !have["a"] || !have["b"] {
		t.Errorf("index after a failed read = %v: earlier messages a and b were dropped", ids)
	}
	if got, _ := s.GetByMessageID(context.Background(), "c", "search"); got == nil {
		t.Error("message c should be stored even though its index update was skipped")
	}
}
