package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

type panicStep struct{ v any }

func (p panicStep) Run(*model.StepContext) error { panic(p.v) }

// lockingStep takes a lock shared by every request, the way a plugin guards
// its state, and panics while holding it when told to.
type lockingStep struct {
	mu *sync.Mutex
}

func (s lockingStep) Run(ctx *model.StepContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.Contains(string(ctx.Body), "boom") {
		var m map[string]int
		m["x"] = 1 // nil map write
	}
	return nil
}

// A plugin that panics on one request must cost that request only, and the
// sender must still get a Beckn NACK: net/http's own recovery just drops the
// connection, which the sender cannot tell apart from a network failure.
func TestServeHTTP_PanicInStepBecomesNack(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "bpp.example",
		role:         model.RoleBPP,
		moduleName:   "panic-test",
		steps:        []definition.Step{panicStep{v: "secret internal detail /etc/keys"}},
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/search", "application/json",
		strings.NewReader(`{"context":{"version":"2.0.0","action":"search","messageId":"m1"}}`))
	if err != nil {
		t.Fatalf("no HTTP response for a request whose pipeline panicked: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "NACK") || !strings.Contains(string(body), "NET_INTERNAL_ERROR") {
		t.Errorf("body is not a NACK with NET_INTERNAL_ERROR: %s", body)
	}
	if strings.Contains(string(body), "secret internal detail") {
		t.Errorf("panic value leaked to the sender: %s", body)
	}
}

// After a request panics while holding a plugin lock, the next requests must
// still be served: the lock was released and the process is still up.
func TestServeHTTP_PanicDoesNotPoisonLaterRequests(t *testing.T) {
	var mu sync.Mutex
	h := &stdHandler{
		SubscriberID: "bpp.example",
		role:         model.RoleBPP,
		moduleName:   "panic-test",
		steps:        []definition.Step{lockingStep{mu: &mu}},
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	post := func(body string) (int, error) {
		resp, err := http.Post(srv.URL+"/search", "application/json", strings.NewReader(body))
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}

	if code, err := post(`{"context":{"action":"search","messageId":"boom"}}`); err != nil || code != http.StatusInternalServerError {
		t.Fatalf("panicking request: status %d, err %v; want 500", code, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code, err := post(`{"context":{"action":"search","messageId":"ok"}}`); err != nil || code != http.StatusOK {
				t.Errorf("request after the panic: status %d, err %v", code, err)
			}
		}()
	}
	wg.Wait()
}

// http.ErrAbortHandler is how a handler (and httputil.ReverseProxy) asks
// net/http to abort the response; it must keep propagating.
func TestServeHTTP_ErrAbortHandlerStillAborts(t *testing.T) {
	h := &stdHandler{
		role:       model.RoleBPP,
		moduleName: "panic-test",
		steps:      []definition.Step{panicStep{v: http.ErrAbortHandler}},
	}
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler to propagate", r)
		}
	}()
	req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"context":{"action":"search"}}`))
	h.ServeHTTP(httptest.NewRecorder(), req)
}
