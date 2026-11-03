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

// mockCache is a test double for definition.Cache.
type mockCache struct {
	getFunc func(ctx context.Context, key string) (string, error)
	setKey  string
	setVal  string
	setTTL  time.Duration
	setErr  error
}

func (m *mockCache) Get(ctx context.Context, key string) (string, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, key)
	}
	return "", errors.New("cache miss")
}
func (m *mockCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	m.setKey = key
	m.setVal = value
	m.setTTL = ttl
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
			Subscriber:       model.Subscriber{SubscriberID: "test-np"},
			KeyID:            "key-1",
			SigningPublicKey: "registry-key",
			ValidUntil:       validUntil,
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

// storeCache is a definition.Cache test double backed by a map: it stores
// what Lookup writes and serves it back on Get.
type storeCache struct {
	entries map[string]string
	gets    []string
}

func newStoreCache() *storeCache {
	return &storeCache{entries: map[string]string{}}
}

func (s *storeCache) Get(ctx context.Context, key string) (string, error) {
	s.gets = append(s.gets, key)
	if v, ok := s.entries[key]; ok {
		return v, nil
	}
	return "", errors.New("cache miss")
}

func (s *storeCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	s.entries[key] = value
	return nil
}

func (s *storeCache) Delete(ctx context.Context, key string) error {
	delete(s.entries, key)
	return nil
}

func (s *storeCache) Clear(ctx context.Context) error {
	s.entries = map[string]string{}
	return nil
}

// countingLookupServer serves handler and counts the requests it receives.
func countingLookupServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// echoLookupHandler answers a /lookup request with a record for the
// subscriber and key named in the request body.
func echoLookupHandler(w http.ResponseWriter, r *http.Request) {
	var req model.Subscription
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	resp := []model.Subscription{{
		Subscriber:       model.Subscriber{SubscriberID: req.SubscriberID},
		KeyID:            req.KeyID,
		SigningPublicKey: "key-of-" + req.SubscriberID + "-" + req.KeyID,
		ValidUntil:       time.Now().Add(time.Hour),
	}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// staticLookupHandler answers every /lookup request with a record for
// subscriberID, whatever the request asked for.
func staticLookupHandler(subscriberID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := []model.Subscription{{
			Subscriber:       model.Subscriber{SubscriberID: subscriberID},
			SigningPublicKey: "key-of-" + subscriberID,
			ValidUntil:       time.Now().Add(time.Hour),
		}}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func TestLookupCacheKey(t *testing.T) {
	t.Parallel()

	if got, want := lookupCacheKey("test-np", "key-1"), "lookup_v2_test-np/key-1"; got != want {
		t.Errorf("lookupCacheKey = %q, want %q", got, want)
	}
	// The pair that collided under the old "lookup_<sub>_<key>" format.
	if lookupCacheKey("a_b", "c") == lookupCacheKey("a", "b_c") {
		t.Error("colliding pairs a_b+c and a+b_c share a cache key")
	}
	// A "/" inside an ID must not break the two-segment shape.
	if got, want := lookupCacheKey("a/b", "c"), "lookup_v2_a%2Fb/c"; got != want {
		t.Errorf("lookupCacheKey = %q, want %q", got, want)
	}
}

// TestRegistryClient_Lookup_CacheIdentity verifies one subscriber's cached
// signing key is never returned for a different subscriber, on both the
// cache-hit and the fresh-lookup path.
func TestRegistryClient_Lookup_CacheIdentity(t *testing.T) {
	t.Parallel()

	mustLookup := func(t *testing.T, client *RegistryClient, sub, key string) model.Subscription {
		t.Helper()
		results, err := client.Lookup(context.Background(), &model.Subscription{
			Subscriber: model.Subscriber{SubscriberID: sub},
			KeyID:      key,
		})
		if err != nil {
			t.Fatalf("Lookup(%q, %q) unexpected error: %v", sub, key, err)
		}
		if len(results) != 1 {
			t.Fatalf("Lookup(%q, %q) returned %d results, want 1", sub, key, len(results))
		}
		return results[0]
	}

	newClient := func(t *testing.T, cache *storeCache, url string) *RegistryClient {
		t.Helper()
		client, closer, err := New(context.Background(), cache, &Config{URL: url})
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}
		t.Cleanup(func() { _ = closer() })
		return client
	}

	t.Run("pairs that joined to the same key get separate entries", func(t *testing.T) {
		server, hits := countingLookupServer(t, echoLookupHandler)
		cache := newStoreCache()
		client := newClient(t, cache, server.URL)

		// Victim "bpp.example"+"k_1" and attacker "bpp.example_k"+"1" both
		// became lookup_bpp.example_k_1 under the old key.
		if got := mustLookup(t, client, "bpp.example_k", "1"); got.SubscriberID != "bpp.example_k" {
			t.Fatalf("got the record of %q for a lookup of bpp.example_k", got.SubscriberID)
		}
		if got := mustLookup(t, client, "bpp.example", "k_1"); got.SubscriberID != "bpp.example" {
			t.Fatalf("got the record of %q for a lookup of bpp.example", got.SubscriberID)
		}
		// Each pair must now be served from its own entry. With a shared key,
		// each repeat would find the other's record and go back to the registry.
		mustLookup(t, client, "bpp.example_k", "1")
		mustLookup(t, client, "bpp.example", "k_1")
		if n := atomic.LoadInt32(hits); n != 2 {
			t.Errorf("registry hit %d times, want 2 (one per pair, then cache hits)", n)
		}
		for _, key := range []string{lookupCacheKey("bpp.example_k", "1"), lookupCacheKey("bpp.example", "k_1")} {
			if _, ok := cache.entries[key]; !ok {
				t.Errorf("expected a cache entry under %q, entries are %v", key, cache.entries)
			}
		}
	})

	t.Run("a cached record for another subscriber is ignored", func(t *testing.T) {
		server, hits := countingLookupServer(t, echoLookupHandler)
		cache := newStoreCache()
		key := lookupCacheKey("bpp.example", "k1")
		planted, _ := json.Marshal([]model.Subscription{{
			Subscriber:       model.Subscriber{SubscriberID: "attacker.example"},
			KeyID:            "k1",
			SigningPublicKey: "attacker-key",
		}})
		cache.entries[key] = string(planted)
		client := newClient(t, cache, server.URL)

		got := mustLookup(t, client, "bpp.example", "k1")
		if got.SigningPublicKey == "attacker-key" || got.SubscriberID != "bpp.example" {
			t.Errorf("served the planted record %+v for bpp.example", got)
		}
		if len(cache.gets) == 0 || cache.gets[0] != key {
			t.Errorf("cache reads %v, want the planted key %q read first", cache.gets, key)
		}
		if n := atomic.LoadInt32(hits); n != 1 {
			t.Errorf("registry hit %d times, want 1 (the planted entry refetched)", n)
		}
	})

	t.Run("a fresh record for another subscriber is rejected and not cached", func(t *testing.T) {
		server, _ := countingLookupServer(t, staticLookupHandler("attacker.example"))
		cache := newStoreCache()
		client := newClient(t, cache, server.URL)

		_, err := client.Lookup(context.Background(), &model.Subscription{
			Subscriber: model.Subscriber{SubscriberID: "bpp.example"},
			KeyID:      "k1",
		})
		testutil.RequireCodedErr(t, err, http.StatusUnauthorized, codeAutSubscriberNotFound)
		if len(cache.entries) != 0 {
			t.Errorf("rejected record was cached: %v", cache.entries)
		}
	})

	t.Run("a subscriber_id differing only in case matches and is cached", func(t *testing.T) {
		server, hits := countingLookupServer(t, staticLookupHandler("BPP.Example"))
		cache := newStoreCache()
		client := newClient(t, cache, server.URL)

		if got := mustLookup(t, client, "bpp.example", "k1"); got.SubscriberID != "BPP.Example" {
			t.Errorf("SubscriberID = %q, want the value the registry returned", got.SubscriberID)
		}
		mustLookup(t, client, "bpp.example", "k1")
		if n := atomic.LoadInt32(hits); n != 1 {
			t.Errorf("registry hit %d times, want 1 (second lookup served from cache)", n)
		}
	})
}
