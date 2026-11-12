package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/payloadstore"
)

// memCache is an in-memory definition.Cache with the real cache's contract:
// a missing key is ("", nil).
type memCache struct {
	mu sync.Mutex
	m  map[string]string
}

func (c *memCache) Get(_ context.Context, k string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[k], nil
}
func (c *memCache) Set(_ context.Context, k, v string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
	return nil
}
func (c *memCache) Delete(_ context.Context, k string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
	return nil
}
func (c *memCache) Clear(context.Context) error { return nil }

func becknStepCtx(action, msgID, auth string) *model.StepContext {
	body := fmt.Sprintf(`{"context":{"version":"2.0.0","action":%q,"messageId":%q,"transactionId":"txn-1"}}`, action, msgID)
	req := httptest.NewRequest(http.MethodPost, "/bap/receiver/"+action, nil)
	req.Header.Set(model.AuthHeaderSubscriber, auth)
	return &model.StepContext{
		Context:         context.Background(),
		Request:         req,
		Body:            []byte(body),
		Role:            model.RoleBAP,
		SubID:           "bap.example",
		RespHeader:      http.Header{},
		ProtocolVersion: "2.0.0",
		MessageID:       msgID,
	}
}

// One search is answered by several BPPs, each with an on_search carrying the
// search's message_id. The BAP runs storePayload in its caller module (stores
// the search) and in its receiver module (stores each on_search), sharing one
// cache, as in config/local-beckn-one-bap.yaml. Every solicited callback must
// find the search's signature, not only the first one.
func TestValidateSign_SecondCallbackForSameMessageStillFindsRequest(t *testing.T) {
	ps, _, err := payloadstore.New(context.Background(), &memCache{m: map[string]string{}}, "onix", map[string]string{})
	if err != nil {
		t.Fatalf("payloadstore.New: %v", err)
	}
	storeStep, err := newStorePayloadStep(ps)
	if err != nil {
		t.Fatal(err)
	}
	validateStep, err := newValidateSignStep(&mockSignValidatorBasic{}, &mockKMBasic{publicKey: "pk"}, ps)
	if err != nil {
		t.Fatal(err)
	}

	// bapTxnCaller: ... sign, storePayload.
	searchAuth := `Signature keyId="bap.example|k1|ed25519",algorithm="ed25519",created="1",expires="2",headers="(created) (expires) digest",signature="c2VhcmNo"`
	if err := storeStep.Run(becknStepCtx("search", "msg-1", searchAuth)); err != nil {
		t.Fatalf("store search: %v", err)
	}

	// bapTxnReceiver: validateSign, ..., storePayload — once per answering BPP.
	for i, bpp := range []string{"bpp-one.example", "bpp-two.example", "bpp-three.example"} {
		cbAuth := fmt.Sprintf(`Signature keyId="%s|k1|ed25519",algorithm="ed25519",created="1",expires="2",headers="(created) (expires) digest request-signature",signature="Y2I="`, bpp)
		cb := becknStepCtx("on_search", "msg-1", cbAuth)
		if err := validateStep.Run(cb); err != nil {
			t.Fatalf("on_search #%d from %s rejected: %v", i+1, bpp, err)
		}
		if err := storeStep.Run(cb); err != nil {
			t.Fatalf("store on_search #%d: %v", i+1, err)
		}
	}
}
