package dediregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/testutil"
)

// fastRetryConfig returns a Config pointing at serverURL with a single, near-
// instant retry, so status-exhaustion cases don't pay the library's default
// multi-second backoff.
func fastRetryConfig(serverURL string) *Config {
	return &Config{
		URL:          serverURL + "/dedi",
		RetryMax:     1,
		RetryWaitMin: time.Millisecond,
		RetryWaitMax: time.Millisecond,
	}
}

// newTestClient builds a client for cfg, failing the test on error.
func newTestClient(t *testing.T, cfg *Config) *DeDiRegistryClient {
	t.Helper()
	return newTestClientWithCache(t, nil, cfg)
}

// newTestClientWithCache builds a client for cfg using cache, failing the
// test on error.
func newTestClientWithCache(t *testing.T, cache definition.Cache, cfg *Config) *DeDiRegistryClient {
	t.Helper()
	client, closer, err := New(context.Background(), cache, cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = closer() })
	return client
}

// statusHandler responds with status and an empty body.
func statusHandler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }
}

// jsonHandler responds 200 with body encoded as JSON.
func jsonHandler(body any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body) //nolint:errcheck
	}
}

// rawHandler responds with status and body written verbatim.
func rawHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body)) //nolint:errcheck
	}
}

// blockingHandler never responds; it returns once the client gives up, which
// closes the connection and cancels the request context.
func blockingHandler(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }

// call invokes one of the client's public lookup methods with valid inputs,
// returning only its error.
type call func(ctx context.Context, c *DeDiRegistryClient) error

func callLookup(ctx context.Context, c *DeDiRegistryClient) error {
	_, err := c.Lookup(ctx, &model.Subscription{Subscriber: model.Subscriber{SubscriberID: "np.example.com"}, KeyID: "k1"})
	return err
}

func callLookupNode(ctx context.Context, c *DeDiRegistryClient) error {
	_, err := c.LookupNode(ctx, "ns/reg/np.example.com")
	return err
}

func callLookupRegistry(ctx context.Context, c *DeDiRegistryClient) error {
	_, err := c.LookupRegistry(ctx, "ns", "reg")
	return err
}

func callQueryByNetwork(ctx context.Context, c *DeDiRegistryClient) error {
	_, err := c.QueryByNetwork(ctx, "ns/reg")
	return err
}

// requireWrappedCodedErr asserts the classification survives the %w wrapping
// the signing-key path applies (keymanager's "failed to lookup registry",
// core's "failed to get validation key"), which is what nackBecknError sees.
func requireWrappedCodedErr(t *testing.T, err error, wantStatus int, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error classified as %s, got nil", wantCode)
	}
	wrapped := fmt.Errorf("failed to get validation key: %w", fmt.Errorf("failed to lookup registry: %w", err))
	testutil.RequireCodedErr(t, wrapped, wantStatus, wantCode)
}

// requireSanitizedMembership asserts err is a 401 AUT_NETWORK_NOT_ALLOWED
// whose NACK message names no adapter config, while the detailed cause keeps
// it.
func requireSanitizedMembership(t *testing.T, err error) {
	t.Helper()
	requireWrappedCodedErr(t, err, http.StatusUnauthorized, codeAutNetworkNotAllowed)
	var codedErr *model.CodedErr
	errors.As(err, &codedErr)
	if msg := codedErr.BecknError().Message; strings.Contains(msg, "allowedNetworkIDs") {
		t.Errorf("NACK message %q names the adapter config", msg)
	}
	var pe *publicErr
	if !errors.As(err, &pe) || !strings.Contains(pe.cause.Error(), "allowedNetworkIDs") {
		t.Error("expected the detailed cause to keep the allowedNetworkIDs detail")
	}
}

// requireNoInternalDetail asserts the NACK message for err doesn't expose the
// DeDi server's address, while the detailed cause stays reachable.
func requireNoInternalDetail(t *testing.T, err error, serverURL string) {
	t.Helper()
	var codedErr *model.CodedErr
	if !errors.As(err, &codedErr) {
		t.Fatalf("expected a *model.CodedErr, got %T", err)
	}
	host := strings.TrimPrefix(serverURL, "http://")
	if msg := codedErr.BecknError().Message; strings.Contains(msg, host) {
		t.Errorf("NACK message %q exposes the DeDi address %q", msg, host)
	}
	var pe *publicErr
	if !errors.As(err, &pe) || pe.cause == nil {
		t.Error("expected a sanitized publicErr that keeps the detailed cause")
	}
}

func TestUpstreamResponseClassification(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		call       call
		wantStatus int
		wantCode   string
		// wantHits, if set, is how many requests reach DeDi: with RetryMax 1,
		// 2 means the status is retried and 1 that it isn't.
		wantHits int32
	}{
		// 404: meaning depends on which lookup asked. (Lookup, LookupNode and
		// LookupRegistry 404s, and the other cases dediregistry_test.go
		// already asserts, are not repeated here.)
		{"QueryByNetwork 404 is an absent entity", statusHandler(http.StatusNotFound), callQueryByNetwork, http.StatusNotFound, codeNetEntityNotFound, 0},

		// Statuses that exhaust retries, or that DeDi should never send, mean
		// it isn't serving the lookup.
		{"5xx after retries is downstream unavailable", statusHandler(http.StatusServiceUnavailable), callLookup, http.StatusServiceUnavailable, codeNetDownstreamUnavailable, 2},
		{"429 after retries is downstream unavailable", statusHandler(http.StatusTooManyRequests), callLookup, http.StatusServiceUnavailable, codeNetDownstreamUnavailable, 2},
		{"501 (not retried) is downstream unavailable", statusHandler(http.StatusNotImplemented), callLookup, http.StatusServiceUnavailable, codeNetDownstreamUnavailable, 1},
		{"unexpected non-200 success status is downstream unavailable", statusHandler(http.StatusAccepted), callLookup, http.StatusServiceUnavailable, codeNetDownstreamUnavailable, 0},
		// Classified by status before the size cap applies.
		{"QueryByNetwork oversized 5xx page is downstream unavailable", rawHandler(http.StatusBadGateway, strings.Repeat("x", maxQueryResponseBytes+1)), callQueryByNetwork, http.StatusServiceUnavailable, codeNetDownstreamUnavailable, 0},

		// Other 4xx: DeDi rejected a request this adapter built.
		{"other 4xx is an internal error, not retried", statusHandler(http.StatusBadRequest), callLookup, http.StatusInternalServerError, codeNetInternalError, 1},

		// 200 with a body DeDi should never have sent.
		{"missing details field is an invalid response", jsonHandler(map[string]any{"data": map[string]any{}}), callLookup, http.StatusBadGateway, codeNetDownstreamInvalidResp, 0},
		{"LookupNode missing details field is an invalid response", jsonHandler(map[string]any{"data": map[string]any{}}), callLookupNode, http.StatusBadGateway, codeNetDownstreamInvalidResp, 0},
		{"LookupRegistry non-object meta is an invalid response", jsonHandler(map[string]any{"data": map[string]any{"meta": "x"}}), callLookupRegistry, http.StatusBadGateway, codeNetDownstreamInvalidResp, 0},
		{"non-object data is an invalid response", jsonHandler(map[string]any{"data": "x"}), callLookup, http.StatusBadGateway, codeNetDownstreamInvalidResp, 0},
		{"LookupNode non-object data is an invalid response", jsonHandler(map[string]any{"data": "x"}), callLookupNode, http.StatusBadGateway, codeNetDownstreamInvalidResp, 0},
		{"LookupNode missing data field is an invalid response", jsonHandler(map[string]any{}), callLookupNode, http.StatusBadGateway, codeNetDownstreamInvalidResp, 0},
		{"LookupRegistry non-JSON body is an invalid response", rawHandler(http.StatusOK, "not json{{"), callLookupRegistry, http.StatusBadGateway, codeNetDownstreamInvalidResp, 0},
		{"LookupRegistry non-object data is an invalid response", jsonHandler(map[string]any{"data": "x"}), callLookupRegistry, http.StatusBadGateway, codeNetDownstreamInvalidResp, 0},
		{"QueryByNetwork wrong-shape JSON is an invalid response", jsonHandler(map[string]any{"data": map[string]any{"records": "x"}}), callQueryByNetwork, http.StatusBadGateway, codeNetDownstreamInvalidResp, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, hits := countingServer(t, tt.handler)
			client := newTestClient(t, fastRetryConfig(server.URL))

			requireWrappedCodedErr(t, tt.call(context.Background(), client), tt.wantStatus, tt.wantCode)
			if tt.wantHits != 0 {
				requireHits(t, hits, tt.wantHits)
			}
		})
	}
}

func TestTransportFailureClassification(t *testing.T) {
	t.Run("connection dropped before a response is downstream unavailable, and retried", func(t *testing.T) {
		server, hits := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		})

		err := callLookup(context.Background(), newTestClient(t, fastRetryConfig(server.URL)))
		requireWrappedCodedErr(t, err, http.StatusServiceUnavailable, codeNetDownstreamUnavailable)
		requireHits(t, hits, 2) // RetryMax 1
	})

	t.Run("unreachable registry is downstream unavailable", func(t *testing.T) {
		server := httptest.NewServer(statusHandler(http.StatusOK))
		cfg := fastRetryConfig(server.URL)
		server.Close() // nothing listening: connection refused

		err := callLookup(context.Background(), newTestClient(t, cfg))
		requireWrappedCodedErr(t, err, http.StatusServiceUnavailable, codeNetDownstreamUnavailable)
		requireNoInternalDetail(t, err, server.URL)
	})

	t.Run("HTTP client timeout is a timeout, and retried", func(t *testing.T) {
		server, hits := countingServer(t, blockingHandler)

		client := newTestClient(t, fastRetryConfig(server.URL))
		// Config.Timeout is whole seconds; set a sub-second one directly so
		// the test stays fast.
		client.client.HTTPClient.Timeout = 50 * time.Millisecond

		err := callLookup(context.Background(), client)
		requireWrappedCodedErr(t, err, http.StatusGatewayTimeout, codeNetTimeout)
		requireNoInternalDetail(t, err, server.URL)
		requireHits(t, hits, 2) // RetryMax 1
	})

	t.Run("caller context deadline is a timeout, not retried", func(t *testing.T) {
		server, hits := countingServer(t, blockingHandler)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		requireWrappedCodedErr(t, callLookup(ctx, newTestClient(t, fastRetryConfig(server.URL))), http.StatusGatewayTimeout, codeNetTimeout)
		requireHits(t, hits, 1)
	})

	t.Run("caller cancellation is request cancelled, not retried", func(t *testing.T) {
		server, hits := countingServer(t, blockingHandler)

		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)

		requireWrappedCodedErr(t, callLookup(ctx, newTestClient(t, fastRetryConfig(server.URL))), http.StatusInternalServerError, codeNetRequestCancelled)
		requireHits(t, hits, 1)
	})

	t.Run("redirect loop keeps its cause, not retried", func(t *testing.T) {
		server, hits := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, r.URL.Path, http.StatusFound)
		})

		err := callLookup(context.Background(), newTestClient(t, fastRetryConfig(server.URL)))
		// net/http gives up at its 10th request; a retry would double it.
		requireHits(t, hits, 10)
		requireWrappedCodedErr(t, err, http.StatusServiceUnavailable, codeNetDownstreamUnavailable)
		var urlErr *url.Error
		if !errors.As(err, &urlErr) || !strings.Contains(urlErr.Error(), "redirects") {
			t.Errorf("expected the redirect failure to stay in the chain, got %v", err)
		}
	})

	t.Run("TLS certificate failure is downstream unavailable and not retried", func(t *testing.T) {
		var conns int32
		server := httptest.NewUnstartedServer(statusHandler(http.StatusOK))
		server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				atomic.AddInt32(&conns, 1)
			}
		}
		server.Config.ErrorLog = log.New(io.Discard, "", 0) // expected handshake errors
		server.StartTLS()
		defer server.Close()

		// The default client doesn't trust httptest's self-signed certificate;
		// with RetryMax 1, a retried failure would open a second connection.
		err := callLookup(context.Background(), newTestClient(t, fastRetryConfig(server.URL)))
		requireWrappedCodedErr(t, err, http.StatusServiceUnavailable, codeNetDownstreamUnavailable)
		if n := atomic.LoadInt32(&conns); n != 1 {
			t.Errorf("got %d connections, want 1 (certificate failures are not retried)", n)
		}
	})

	t.Run("body cut short is downstream unavailable, not retried", func(t *testing.T) {
		server, hits := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
			// Promise more bytes than are sent, so the read fails with
			// unexpected EOF after a 200 status.
			w.Header().Set("Content-Length", "100")
			w.Write([]byte(`{"data":`)) //nolint:errcheck
		})

		requireWrappedCodedErr(t, callLookup(context.Background(), newTestClient(t, fastRetryConfig(server.URL))), http.StatusServiceUnavailable, codeNetDownstreamUnavailable)
		requireHits(t, hits, 1)
	})

	t.Run("unbuildable request URL is an internal error", func(t *testing.T) {
		// validate() rejects such a URL at startup and caller IDs are
		// escaped, so only a URL changed after New() can reach this branch.
		client := newTestClient(t, &Config{URL: "http://example.com/dedi"})
		client.config.URL = "http://example.com/\x00"
		err := callLookup(context.Background(), client)
		requireWrappedCodedErr(t, err, http.StatusInternalServerError, codeNetInternalError)
		requireNoInternalDetail(t, err, "example.com")
	})
}

func TestInputValidationClassification(t *testing.T) {
	// No request is sent for any of these, so the URL is never dialled.
	// (Empty Lookup IDs, a two-part nodeID and an empty networkID are
	// asserted in dediregistry_test.go.)
	client := newTestClient(t, &Config{URL: "http://127.0.0.1:1/dedi"})
	ctx := context.Background()

	lookup := func(subscriberID, keyID string) func() error {
		return func() error {
			_, err := client.Lookup(ctx, &model.Subscription{Subscriber: model.Subscriber{SubscriberID: subscriberID}, KeyID: keyID})
			return err
		}
	}
	lookupRegistry := func(namespace, registry string) func() error {
		return func() error { _, err := client.LookupRegistry(ctx, namespace, registry); return err }
	}
	lookupNode := func(nodeID string) func() error {
		return func() error { _, err := client.LookupNode(ctx, nodeID); return err }
	}
	query := func(networkID string) func() error {
		return func() error { _, err := client.QueryByNetwork(ctx, networkID); return err }
	}

	tests := []struct {
		name       string
		call       func() error
		wantStatus int
		wantCode   string
	}{
		{"LookupRegistry without namespace", lookupRegistry("", "reg"), http.StatusBadRequest, codeCtxMissingField},
		{"LookupRegistry without registry name", lookupRegistry("ns", ""), http.StatusBadRequest, codeCtxMissingField},

		// Dot and empty segments pass url.PathEscape unchanged and could make
		// a normalising server serve a different path, so they are rejected.
		// Lookup's IDs come from the signature keyId: a malformed signature.
		{"Lookup with '..' subscriber_id", lookup("..", "k1"), http.StatusUnauthorized, codeAutSignatureInvalid},
		{"Lookup with '.' key_id", lookup("np.example.com", "."), http.StatusUnauthorized, codeAutSignatureInvalid},
		{"LookupRegistry with '..' namespace", lookupRegistry("..", "reg"), http.StatusBadRequest, codeCtxInvalidField},
		{"LookupNode with a '..' segment", lookupNode("ns/../x"), http.StatusBadRequest, codeCtxInvalidField},
		{"LookupNode with an empty segment", lookupNode("ns//x"), http.StatusBadRequest, codeCtxInvalidField},
		{"LookupNode with a whitespace-only segment", lookupNode("ns/ /x"), http.StatusBadRequest, codeCtxInvalidField},
		{"LookupNode without nodeID", lookupNode(""), http.StatusBadRequest, codeCtxMissingField},
		{"Lookup with a whitespace-only subscriber_id", lookup(" ", "k1"), http.StatusUnauthorized, codeAutSignatureInvalid},
		{"QueryByNetwork with a '..' segment", query("../x"), http.StatusBadRequest, codeCtxInvalidField},
		{"QueryByNetwork with an empty segment", query("beckn.one//x"), http.StatusBadRequest, codeCtxInvalidField},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireWrappedCodedErr(t, tt.call(), tt.wantStatus, tt.wantCode)
		})
	}
}

// TestQueryByNetworkEmptyBody verifies a /query body with no data is an
// empty result, not an error.
func TestQueryByNetworkEmptyBody(t *testing.T) {
	server := httptest.NewServer(jsonHandler(map[string]any{}))
	defer server.Close()

	records, err := newTestClient(t, fastRetryConfig(server.URL)).QueryByNetwork(context.Background(), "ns/reg")
	if err != nil || len(records) != 0 {
		t.Fatalf("QueryByNetwork() = %d records, %v; want 0 records, nil", len(records), err)
	}
}

// TestCallerValuesAreEscaped verifies caller-supplied IDs are path-escaped:
// they reach DeDi as literal path segments and can't change the requested
// path or make the request URL unparseable.
func TestCallerValuesAreEscaped(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client := newTestClient(t, fastRetryConfig(server.URL))
	ctx := context.Background()

	t.Run("Lookup", func(t *testing.T) {
		_, err := client.Lookup(ctx, &model.Subscription{Subscriber: model.Subscriber{SubscriberID: "np?x#y/../z"}, KeyID: "k%zz"})
		// A malformed ID is simply an unknown subscriber, not an adapter fault.
		requireWrappedCodedErr(t, err, http.StatusUnauthorized, codeAutSubscriberNotFound)
		if want := "/dedi/lookup/np%3Fx%23y%2F..%2Fz/subscribers.beckn.one/k%25zz"; gotPath != want {
			t.Errorf("DeDi path = %q, want %q", gotPath, want)
		}
	})

	t.Run("LookupRegistry", func(t *testing.T) {
		_, err := client.LookupRegistry(ctx, "ns?x", "reg/y")
		requireWrappedCodedErr(t, err, http.StatusNotFound, codeNetEntityNotFound)
		if want := "/dedi/lookup/ns%3Fx/reg%2Fy"; gotPath != want {
			t.Errorf("DeDi path = %q, want %q", gotPath, want)
		}
	})

	t.Run("QueryByNetwork keeps the namespace/registry separator", func(t *testing.T) {
		_, err := client.QueryByNetwork(ctx, "beckn.one/test net?x")
		requireWrappedCodedErr(t, err, http.StatusNotFound, codeNetEntityNotFound)
		if want := "/dedi/query/beckn.one/test%20net%3Fx"; gotPath != want {
			t.Errorf("DeDi path = %q, want %q", gotPath, want)
		}
	})
}

// echoRecordHandler answers /lookup/{subscriber}/{registry}/{key} with a
// record whose subscriber_id and signing key are taken from the request path.
func echoRecordHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/dedi/lookup/"), "/")
	sub, key := parts[0], parts[len(parts)-1]
	jsonHandler(map[string]any{"data": map[string]any{"details": map[string]any{
		"subscriber_id": sub, "signing_public_key": "key-of-" + sub + "-" + key,
	}}})(w, r)
}

// countingServer serves handler and counts the requests it receives.
func countingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// requireHits asserts the server behind hits received exactly want requests.
func requireHits(t *testing.T, hits *int32, want int32) {
	t.Helper()
	if n := atomic.LoadInt32(hits); n != want {
		t.Errorf("DeDi hit %d times, want %d", n, want)
	}
}

// cachingClient returns a client whose cache stores what Lookup writes.
func cachingClient(t *testing.T, serverURL string) (*DeDiRegistryClient, *mockCache) {
	t.Helper()
	cache := &mockCache{entries: map[string]string{}}
	return newTestClientWithCache(t, cache, fastRetryConfig(serverURL)), cache
}

func mustLookup(t *testing.T, c *DeDiRegistryClient, sub, key string) model.Subscription {
	t.Helper()
	results, err := c.Lookup(context.Background(), &model.Subscription{Subscriber: model.Subscriber{SubscriberID: sub}, KeyID: key})
	if err != nil || len(results) != 1 {
		t.Fatalf("Lookup(%q, %q) = %v, %v", sub, key, results, err)
	}
	return results[0]
}

// TestLookupCacheCannotServeAnotherSubscriber verifies one subscriber's
// cached signing key is never returned for a different subscriber.
func TestLookupCacheCannotServeAnotherSubscriber(t *testing.T) {
	t.Run("pairs that joined to the same key get separate entries", func(t *testing.T) {
		server, hits := countingServer(t, echoRecordHandler)
		client, _ := cachingClient(t, server.URL)

		// Victim "bpp.example"+"k_1" and attacker "bpp.example_k"+"1" both
		// became dedi_lookup_bpp.example_k_1 under the old key.
		mustLookup(t, client, "bpp.example_k", "1")
		if got := mustLookup(t, client, "bpp.example", "k_1"); got.SubscriberID != "bpp.example" {
			t.Fatalf("got the record of %q for a lookup of bpp.example", got.SubscriberID)
		}
		// Each pair must now be served from its own entry. With a shared key,
		// each repeat would find the other's record and go back to DeDi.
		mustLookup(t, client, "bpp.example_k", "1")
		mustLookup(t, client, "bpp.example", "k_1")
		requireHits(t, hits, 2) // one per pair, then cache hits
	})

	t.Run("a cached record for another subscriber is ignored", func(t *testing.T) {
		server, hits := countingServer(t, echoRecordHandler)
		client, cache := cachingClient(t, server.URL)
		key := lookupCacheKey("bpp.example", "k1")
		planted, _ := json.Marshal([]model.Subscription{{Subscriber: model.Subscriber{SubscriberID: "attacker.example"}, SigningPublicKey: "attacker-key"}})
		cache.entries[key] = string(planted)

		got := mustLookup(t, client, "bpp.example", "k1")
		if got.SigningPublicKey == "attacker-key" || got.SubscriberID != "bpp.example" {
			t.Errorf("served the planted record %+v for bpp.example", got)
		}
		if len(cache.gets) == 0 || cache.gets[0] != key {
			t.Errorf("cache reads %v, want the planted key %q read", cache.gets, key)
		}
		requireHits(t, hits, 1) // the planted entry refetched
	})
}

// TestLookupFreshRecordIdentity verifies a record fetched from DeDi must
// belong to the requested subscriber, as a cached one must.
func TestLookupFreshRecordIdentity(t *testing.T) {
	record := func(subscriberID any) http.HandlerFunc {
		details := map[string]any{"signing_public_key": "key"}
		if subscriberID != nil {
			details["subscriber_id"] = subscriberID
		}
		return jsonHandler(map[string]any{"data": map[string]any{"details": details}})
	}

	t.Run("a record for another subscriber is rejected and not cached", func(t *testing.T) {
		server, _ := countingServer(t, record("attacker.example"))
		client, cache := cachingClient(t, server.URL)

		_, err := client.Lookup(context.Background(), &model.Subscription{Subscriber: model.Subscriber{SubscriberID: "bpp.example"}, KeyID: "k1"})
		requireWrappedCodedErr(t, err, http.StatusUnauthorized, codeAutSubscriberNotFound)
		if len(cache.entries) != 0 {
			t.Errorf("rejected record was cached: %v", cache.entries)
		}
	})

	t.Run("a record without subscriber_id is accepted as is and cached", func(t *testing.T) {
		server, hits := countingServer(t, record(nil))
		client, _ := cachingClient(t, server.URL)

		if got := mustLookup(t, client, "bpp.example", "k1"); got.SubscriberID != "" {
			t.Errorf("SubscriberID = %q, want the empty value DeDi returned", got.SubscriberID)
		}
		mustLookup(t, client, "bpp.example", "k1")
		requireHits(t, hits, 1) // second lookup served from cache
	})

	t.Run("a record without subscriber_id gets no catalog network exemption", func(t *testing.T) {
		// The catalog service skips network checks only when DeDi's record
		// names it; a record without subscriber_id gets the normal checks.
		server, _ := countingServer(t, jsonHandler(map[string]any{"data": map[string]any{
			"details":             map[string]any{"signing_public_key": "key"},
			"network_memberships": []string{"other/net"},
		}}))
		cfg := fastRetryConfig(server.URL)
		cfg.AllowedNetworkIDs = []string{"allowed/net"}
		client := newTestClient(t, cfg)

		_, err := client.Lookup(context.Background(), &model.Subscription{Subscriber: model.Subscriber{SubscriberID: "fabric.nfh.global"}, KeyID: "k1"})
		requireSanitizedMembership(t, err)
	})

	t.Run("a subscriber_id differing only in case matches and is cached", func(t *testing.T) {
		server, hits := countingServer(t, record("BPP.Example"))
		client, _ := cachingClient(t, server.URL)

		mustLookup(t, client, "bpp.example", "k1")
		mustLookup(t, client, "bpp.example", "k1")
		requireHits(t, hits, 1) // second lookup served from cache
	})
}

// TestOversizedResponseIsSanitized verifies an oversized body's NACK names no
// DeDi address, though its logged cause does.
func TestOversizedResponseIsSanitized(t *testing.T) {
	for name, tc := range map[string]struct {
		size int
		call call
	}{
		"Lookup":         {maxLookupResponseBytes + 1, callLookup},
		"QueryByNetwork": {maxQueryResponseBytes + 1, callQueryByNetwork},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(rawHandler(http.StatusOK, strings.Repeat("0", tc.size)))
			defer server.Close()

			err := tc.call(context.Background(), newTestClient(t, fastRetryConfig(server.URL)))
			requireWrappedCodedErr(t, err, http.StatusBadGateway, codeNetDownstreamInvalidResp)
			requireNoInternalDetail(t, err, server.URL)
		})
	}
}

// TestNewRejectsInvalidConfig verifies New fails, returning no client, for a
// config validate rejects.
func TestNewRejectsInvalidConfig(t *testing.T) {
	client, closer, err := New(context.Background(), nil, &Config{URL: "ftp://dedi.example.com/dedi"})
	if err == nil || client != nil || closer != nil {
		t.Fatalf("New() = %v, %v, %v; want nil client and closer and an error", client, closer != nil, err)
	}
}

// TestNewTrimsTrailingSlash verifies a url with a trailing slash is accepted
// and requests don't start with "//".
func TestNewTrimsTrailingSlash(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	cfg := &Config{URL: server.URL + "/dedi/", RetryMax: 1, RetryWaitMin: time.Millisecond, RetryWaitMax: time.Millisecond}

	_ = callLookup(context.Background(), newTestClient(t, cfg))
	if want := "/dedi/lookup/np.example.com/subscribers.beckn.one/k1"; gotPath != want {
		t.Errorf("DeDi path = %q, want %q", gotPath, want)
	}
	if cfg.URL != server.URL+"/dedi/" {
		t.Errorf("caller's Config.URL changed to %q", cfg.URL)
	}
}
