package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/payloadstore"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/router"
)

// The router hands every request the same *model.Route (and the same
// *url.URL) out of its rule table, and the proxy director installs that URL
// on the outbound request. This test drives the real router, the real payload
// store and the reverse proxy from many goroutines at once, with and without
// a query string, and checks under -race that nothing shared is written and
// that no request sees another request's body, route or query.
func TestStdHandler_ConcurrentRequestsDoNotShareState(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen-Path", r.URL.Path)
		w.Header().Set("X-Seen-Query", r.URL.RawQuery)
		_, _ = w.Write(body)
	}))
	defer upstream.Close()

	rulesPath := filepath.Join(t.TempDir(), "routing.yaml")
	rules := fmt.Sprintf(`routingRules:
  - version: "2.0.0"
    targetType: "url"
    target:
      url: "%s/app"
    endpoints:
      - search
      - select
`, upstream.URL)
	if err := os.WriteFile(rulesPath, []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, _, err := router.New(context.Background(), &router.Config{RoutingConfig: rulesPath})
	if err != nil {
		t.Fatalf("router.New: %v", err)
	}
	ps, _, err := payloadstore.New(context.Background(), &memCache{m: map[string]string{}}, "onix", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	routeStep, err := newAddRouteStep(rt, "/bap/caller")
	if err != nil {
		t.Fatal(err)
	}
	storeStep, err := newStorePayloadStep(ps)
	if err != nil {
		t.Fatal(err)
	}

	h := &stdHandler{
		SubscriberID: "bap.example",
		role:         model.RoleBAP,
		basePath:     "/bap/caller",
		moduleName:   "conc",
		steps:        []definition.Step{routeStep, storeStep},
		httpClient:   &http.Client{Transport: http.DefaultTransport},
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	const n = 150
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			action := []string{"search", "select"}[i%2]
			query := ""
			if i%3 == 0 {
				query = fmt.Sprintf("page=%d", i)
			}
			body := fmt.Sprintf(`{"context":{"version":"2.0.0","action":%q,"messageId":"m-%d","transactionId":"t-%d"},"message":{"n":%d}}`, action, i, i%5, i)
			u := srv.URL + "/bap/caller/" + action
			if query != "" {
				u += "?" + query
			}
			resp, err := http.Post(u, "application/json", strings.NewReader(body))
			if err != nil {
				t.Errorf("request %d: %v", i, err)
				return
			}
			defer resp.Body.Close()
			got, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("request %d: status %d body %s", i, resp.StatusCode, got)
				return
			}
			if string(got) != body {
				t.Errorf("request %d: upstream saw another body:\n got %s\nwant %s", i, got, body)
			}
			if p := resp.Header.Get("X-Seen-Path"); p != "/app/"+action {
				t.Errorf("request %d (%s): routed to %q", i, action, p)
			}
			if q := resp.Header.Get("X-Seen-Query"); q != query {
				t.Errorf("request %d: upstream saw query %q, want %q", i, q, query)
			}
		}(i)
	}
	wg.Wait()

	// Every message must be retrievable, and each of the 5 transactions must
	// list all 30 of its messages.
	for i := 0; i < n; i++ {
		action := []string{"search", "select"}[i%2]
		e, err := ps.GetByMessageID(context.Background(), fmt.Sprintf("m-%d", i), action)
		if err != nil || e == nil {
			t.Fatalf("message m-%d not stored: %v, %v", i, e, err)
		}
	}
	for txn := 0; txn < 5; txn++ {
		entries, err := ps.GetByTransactionID(context.Background(), fmt.Sprintf("t-%d", txn))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != n/5 {
			t.Errorf("transaction t-%d lists %d of %d messages", txn, len(entries), n/5)
		}
	}
}
