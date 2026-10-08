package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/testutil"
)

// mockCache is a test double for definition.Cache. When entries is non-nil,
// Set stores into it and Get reads from it, so what Lookup writes can be read
// back; gets records the keys Get was asked for.
type mockCache struct {
	getFunc func(ctx context.Context, key string) (string, error)
	setKey  string
	setVal  string
	setTTL  time.Duration
	setErr  error
	entries map[string]string
	gets    []string
}

func (m *mockCache) Get(ctx context.Context, key string) (string, error) {
	m.gets = append(m.gets, key)
	if m.getFunc != nil {
		return m.getFunc(ctx, key)
	}
	if v, ok := m.entries[key]; ok {
		return v, nil
	}
	return "", errors.New("cache miss")
}
func (m *mockCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	m.setKey = key
	m.setVal = value
	m.setTTL = ttl
	if m.entries != nil {
		m.entries[key] = value
	}
	return m.setErr
}
func (m *mockCache) Delete(ctx context.Context, key string) error { return nil }
func (m *mockCache) Clear(ctx context.Context) error              { return nil }

// TestValidate ensures the config validation logic works correctly.
func TestValidate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name        string
		config      *Config
		expectedErr string
	}{
		{
			name:        "should return error for nil config",
			config:      nil,
			expectedErr: "registry config cannot be nil",
		},
		{
			name:        "should return error for empty URL",
			config:      &Config{URL: ""},
			expectedErr: "registry URL cannot be empty",
		},
		{
			name:        "should succeed for valid config",
			config:      &Config{URL: "http://localhost:8080"},
			expectedErr: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(tc.config)
			if tc.expectedErr != "" {
				if err == nil {
					t.Fatalf("expected an error but got none")
				}
				if err.Error() != tc.expectedErr {
					t.Errorf("expected error message '%s', but got '%s'", tc.expectedErr, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, but got: %v", err)
				}
			}
		})
	}
}

// TestNew tests the constructor for the RegistryClient.
func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("should fail with invalid config", func(t *testing.T) {
		_, _, err := New(context.Background(), nil, &Config{URL: ""})
		if err == nil {
			t.Fatal("expected an error for invalid config but got none")
		}
	})

	t.Run("should succeed with valid config and set defaults", func(t *testing.T) {
		cfg := &Config{URL: "http://test.com"}
		client, closer, err := New(context.Background(), nil, cfg)
		if err != nil {
			t.Fatalf("expected no error, but got: %v", err)
		}
		if client == nil {
			t.Fatal("expected client to be non-nil")
		}
		if closer == nil {
			t.Fatal("expected closer to be non-nil")
		}
		// Check if default retry settings are applied (go-retryablehttp defaults)
		if client.client.RetryMax != 4 {
			t.Errorf("expected default RetryMax of 4, but got %d", client.client.RetryMax)
		}
	})

	t.Run("should apply custom retry settings", func(t *testing.T) {
		cfg := &Config{
			URL:          "http://test.com",
			RetryMax:     10,
			RetryWaitMin: 100 * time.Millisecond,
			RetryWaitMax: 1 * time.Second,
		}
		client, _, err := New(context.Background(), nil, cfg)
		if err != nil {
			t.Fatalf("expected no error, but got: %v", err)
		}

		if client.client.RetryMax != cfg.RetryMax {
			t.Errorf("expected RetryMax to be %d, but got %d", cfg.RetryMax, client.client.RetryMax)
		}
		if client.client.RetryWaitMin != cfg.RetryWaitMin {
			t.Errorf("expected RetryWaitMin to be %v, but got %v", cfg.RetryWaitMin, client.client.RetryWaitMin)
		}
		if client.client.RetryWaitMax != cfg.RetryWaitMax {
			t.Errorf("expected RetryWaitMax to be %v, but got %v", cfg.RetryWaitMax, client.client.RetryWaitMax)
		}
	})
}

// TestRegistryClient_Lookup tests the Lookup method.
func TestRegistryClient_Lookup(t *testing.T) {
	t.Parallel()

	t.Run("should succeed and unmarshal response", func(t *testing.T) {
		expectedSubs := []model.Subscription{
			{
				KeyID:            "test-key",
				SigningPublicKey: "test-signing-key",
				EncrPublicKey:    "test-encryption-key",
				ValidFrom:        time.Now(),
				ValidUntil:       time.Now().Add(24 * time.Hour),
				Status:           "SUBSCRIBED",
			},
			{
				KeyID:            "test-key-2",
				SigningPublicKey: "test-signing-key-2",
				EncrPublicKey:    "test-encryption-key-2",
				ValidFrom:        time.Now(),
				ValidUntil:       time.Now().Add(48 * time.Hour),
				Status:           "SUBSCRIBED",
			},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/lookup" {
				t.Errorf("expected path '/lookup', got '%s'", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode(expectedSubs); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		}))
		defer server.Close()

		client, closer, err := New(context.Background(), nil, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		defer closer()

		results, err := client.Lookup(context.Background(), &model.Subscription{})
		if err != nil {
			t.Fatalf("lookup failed: %v", err)
		}

		if len(results) != len(expectedSubs) {
			t.Fatalf("expected %d results, but got %d", len(expectedSubs), len(results))
		}

		if results[0].SubscriberID != expectedSubs[0].SubscriberID {
			t.Errorf("expected subscriber ID '%s', got '%s'", expectedSubs[0].SubscriberID, results[0].SubscriberID)
		}
	})

	t.Run("should fail on non-200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		client, closer, err := New(context.Background(), nil, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		defer closer()

		_, err = client.Lookup(context.Background(), &model.Subscription{})
		if err == nil {
			t.Fatal("expected an error but got none")
		}
	})

	t.Run("should fail on bad JSON response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `[{"subscriber_id": "bad-json"`) // Malformed JSON
		}))
		defer server.Close()

		client, closer, err := New(context.Background(), nil, &Config{URL: server.URL, RetryMax: 1})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		defer closer()

		_, err = client.Lookup(context.Background(), &model.Subscription{})
		if err == nil {
			t.Fatal("expected an unmarshaling error but got none")
		}
	})
}

// TestRegistryClient_Lookup_Cache tests the caching behaviour of the Lookup method.
func TestRegistryClient_Lookup_Cache(t *testing.T) {
	t.Parallel()

	sub := &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: "test-np"},
		KeyID:      "key-1",
	}
	expectedCacheKey := "lookup_v2_test-np/key-1"

	t.Run("cache hit skips HTTP call", func(t *testing.T) {
		cached := []model.Subscription{{SigningPublicKey: "cached-key"}}
		cachedJSON, _ := json.Marshal(cached)

		httpCalled := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpCalled = true
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		cache := &mockCache{
			getFunc: func(ctx context.Context, key string) (string, error) {
				return string(cachedJSON), nil
			},
		}
		client, closer, err := New(context.Background(), cache, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		defer closer()

		results, err := client.Lookup(context.Background(), sub)
		if err != nil {
			t.Fatalf("Lookup() unexpected error: %v", err)
		}
		if httpCalled {
			t.Error("expected HTTP call to be skipped on cache hit")
		}
		if len(results) != 1 || results[0].SigningPublicKey != "cached-key" {
			t.Errorf("expected cached result, got %+v", results)
		}
	})

	t.Run("cache miss calls HTTP and writes to cache", func(t *testing.T) {
		validUntil := time.Now().Add(10 * time.Minute)
		resp := []model.Subscription{{
			Subscriber:      model.Subscriber{SubscriberID: "test-np"},
			KeyID:           "key-1",
			SigningPublicKey: "registry-key",
			ValidUntil:      validUntil,
		}}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		cache := &mockCache{}
		client, closer, err := New(context.Background(), cache, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		defer closer()

		results, err := client.Lookup(context.Background(), sub)
		if err != nil {
			t.Fatalf("Lookup() unexpected error: %v", err)
		}
		if len(results) != 1 || results[0].SigningPublicKey != "registry-key" {
			t.Errorf("unexpected result: %+v", results)
		}
		if cache.setKey != expectedCacheKey {
			t.Errorf("expected cache key %q, got %q", expectedCacheKey, cache.setKey)
		}
		if cache.setVal == "" {
			t.Error("expected non-empty value written to cache")
		}
		if cache.setTTL <= 0 {
			t.Errorf("expected positive TTL, got %v", cache.setTTL)
		}
	})

	t.Run("corrupt cache value falls through to HTTP", func(t *testing.T) {
		resp := []model.Subscription{{SigningPublicKey: "fresh-key"}}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		cache := &mockCache{
			getFunc: func(ctx context.Context, key string) (string, error) {
				return "this is not valid json{{{{", nil
			},
		}
		client, closer, err := New(context.Background(), cache, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		defer closer()

		results, err := client.Lookup(context.Background(), sub)
		if err != nil {
			t.Fatalf("Lookup() unexpected error: %v", err)
		}
		if len(results) != 1 || results[0].SigningPublicKey != "fresh-key" {
			t.Errorf("expected HTTP result after corrupt cache, got %+v", results)
		}
	})

	t.Run("cache set error does not fail lookup", func(t *testing.T) {
		resp := []model.Subscription{{SigningPublicKey: "registry-key"}}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		cache := &mockCache{setErr: errors.New("redis down")}
		client, closer, err := New(context.Background(), cache, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		defer closer()

		results, err := client.Lookup(context.Background(), sub)
		if err != nil {
			t.Fatalf("Lookup() must not fail when cache.Set errors, got: %v", err)
		}
		if len(results) != 1 || results[0].SigningPublicKey != "registry-key" {
			t.Errorf("unexpected result: %+v", results)
		}
	})

	t.Run("zero ValidUntil uses default cache TTL", func(t *testing.T) {
		resp := []model.Subscription{{
			SigningPublicKey: "registry-key",
			// ValidUntil is zero — no expiry in the response
		}}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		cache := &mockCache{}
		client, closer, err := New(context.Background(), cache, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		defer closer()

		_, err = client.Lookup(context.Background(), sub)
		if err != nil {
			t.Fatalf("Lookup() unexpected error: %v", err)
		}
		if cache.setTTL != defaultCacheTTL {
			t.Errorf("expected default TTL %v, got %v", defaultCacheTTL, cache.setTTL)
		}
	})

	t.Run("nil cache behaves as before — no cache operations", func(t *testing.T) {
		resp := []model.Subscription{{SigningPublicKey: "direct-key"}}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client, closer, err := New(context.Background(), nil, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		defer closer()

		results, err := client.Lookup(context.Background(), sub)
		if err != nil {
			t.Fatalf("Lookup() unexpected error: %v", err)
		}
		if len(results) != 1 || results[0].SigningPublicKey != "direct-key" {
			t.Errorf("unexpected result: %+v", results)
		}
	})
}

// registryServer answers /lookup with respond(request) and counts the requests.
func registryServer(t *testing.T, respond func(req model.Subscription) []model.Subscription) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		var req model.Subscription
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode lookup request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(respond(req)); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// echoRecord answers with a record for the requested subscriber.
func echoRecord(req model.Subscription) []model.Subscription {
	return []model.Subscription{{
		Subscriber:       model.Subscriber{SubscriberID: req.SubscriberID},
		KeyID:            req.KeyID,
		SigningPublicKey: "key-of-" + req.SubscriberID + "-" + req.KeyID,
	}}
}

// recordFor answers every lookup with one record for subscriberID.
func recordFor(subscriberID string) func(model.Subscription) []model.Subscription {
	return func(req model.Subscription) []model.Subscription {
		return []model.Subscription{{
			Subscriber:       model.Subscriber{SubscriberID: subscriberID},
			KeyID:            req.KeyID,
			SigningPublicKey: "key-of-" + subscriberID,
		}}
	}
}

// cachingClient returns a client whose cache stores what Lookup writes.
func cachingClient(t *testing.T, serverURL string) (*RegistryClient, *mockCache) {
	t.Helper()
	cache := &mockCache{entries: map[string]string{}}
	client, closer, err := New(context.Background(), cache, &Config{URL: serverURL, RetryMax: 1})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	t.Cleanup(func() { _ = closer() })
	return client, cache
}

func mustLookup(t *testing.T, c *RegistryClient, sub, key string) model.Subscription {
	t.Helper()
	results, err := c.Lookup(context.Background(), &model.Subscription{Subscriber: model.Subscriber{SubscriberID: sub}, KeyID: key})
	if err != nil || len(results) != 1 {
		t.Fatalf("Lookup(%q, %q) = %v, %v", sub, key, results, err)
	}
	return results[0]
}

// requireHits asserts the server behind hits received exactly want requests.
func requireHits(t *testing.T, hits *int32, want int32) {
	t.Helper()
	if n := atomic.LoadInt32(hits); n != want {
		t.Errorf("registry hit %d times, want %d", n, want)
	}
}

// TestLookupCacheKey verifies pairs that the old "lookup_<sub>_<key>" format
// joined alike now get distinct keys.
func TestLookupCacheKey(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ subA, keyA, subB, keyB string }{
		{"bpp.example_k", "1", "bpp.example", "k_1"},
		{"a/b", "c", "a", "b/c"},
		{"a%2Fb", "c", "a/b", "c"},
	} {
		a, b := lookupCacheKey(tc.subA, tc.keyA), lookupCacheKey(tc.subB, tc.keyB)
		if a == b {
			t.Errorf("(%q, %q) and (%q, %q) share cache key %q", tc.subA, tc.keyA, tc.subB, tc.keyB, a)
		}
	}
}

// TestLookupCacheCannotServeAnotherSubscriber verifies one subscriber's
// cached signing key is never returned for a different subscriber.
func TestLookupCacheCannotServeAnotherSubscriber(t *testing.T) {
	t.Parallel()

	t.Run("pairs that joined to the same key get separate entries", func(t *testing.T) {
		server, hits := registryServer(t, echoRecord)
		client, _ := cachingClient(t, server.URL)

		// Victim "bpp.example"+"k_1" and attacker "bpp.example_k"+"1" both
		// became lookup_bpp.example_k_1 under the old key.
		mustLookup(t, client, "bpp.example_k", "1")
		if got := mustLookup(t, client, "bpp.example", "k_1"); got.SubscriberID != "bpp.example" {
			t.Fatalf("got the record of %q for a lookup of bpp.example", got.SubscriberID)
		}
		// Each pair must now be served from its own entry. With a shared key,
		// each repeat would find the other's record and go back to the registry.
		mustLookup(t, client, "bpp.example_k", "1")
		mustLookup(t, client, "bpp.example", "k_1")
		requireHits(t, hits, 2) // one per pair, then cache hits
	})

	t.Run("a cached record for another subscriber is ignored", func(t *testing.T) {
		server, hits := registryServer(t, echoRecord)
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

// TestLookupFreshRecordIdentity verifies a record fetched from the registry
// must belong to the requested subscriber, as a cached one must.
func TestLookupFreshRecordIdentity(t *testing.T) {
	t.Parallel()

	rejected := func(t *testing.T, respond func(model.Subscription) []model.Subscription, sub string) {
		t.Helper()
		server, _ := registryServer(t, respond)
		client, cache := cachingClient(t, server.URL)

		_, err := client.Lookup(context.Background(), &model.Subscription{Subscriber: model.Subscriber{SubscriberID: sub}, KeyID: "k1"})
		if err == nil {
			t.Fatalf("Lookup(%q) succeeded, want AUT_SUBSCRIBER_NOT_FOUND", sub)
		}
		// keymanager and core wrap the error with %w before building the NACK.
		wrapped := fmt.Errorf("failed to get validation key: %w", fmt.Errorf("failed to lookup registry: %w", err))
		testutil.RequireCodedErr(t, wrapped, http.StatusUnauthorized, "AUT_SUBSCRIBER_NOT_FOUND")
		if len(cache.entries) != 0 {
			t.Errorf("rejected record was cached: %v", cache.entries)
		}
	}

	t.Run("a record for another subscriber is rejected and not cached", func(t *testing.T) {
		rejected(t, recordFor("attacker.example"), "bpp.example")
	})

	t.Run("a response that also has another subscriber's record is rejected", func(t *testing.T) {
		rejected(t, func(req model.Subscription) []model.Subscription {
			return append(echoRecord(req), recordFor("attacker.example")(req)...)
		}, "bpp.example")
	})

	t.Run("a lookup without a subscriber ID gets no record that names one", func(t *testing.T) {
		rejected(t, recordFor("bpp.example"), "")
	})

	t.Run("a record without subscriber_id is accepted and cached", func(t *testing.T) {
		server, hits := registryServer(t, recordFor(""))
		client, _ := cachingClient(t, server.URL)

		mustLookup(t, client, "bpp.example", "k1")
		mustLookup(t, client, "bpp.example", "k1")
		requireHits(t, hits, 1) // second lookup served from cache
	})

	t.Run("a subscriber_id differing only in case matches and is cached", func(t *testing.T) {
		server, hits := registryServer(t, recordFor("BPP.Example"))
		client, _ := cachingClient(t, server.URL)

		mustLookup(t, client, "bpp.example", "k1")
		mustLookup(t, client, "bpp.example", "k1")
		requireHits(t, hits, 1) // second lookup served from cache
	})
}
