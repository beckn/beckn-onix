package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
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
	expectedCacheKey := "lookup_test-np_key-1"

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
