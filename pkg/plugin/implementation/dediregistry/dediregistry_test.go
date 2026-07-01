package dediregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

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

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "empty url",
			config: &Config{
				URL: "",
			},
			wantErr: true,
		},
		{
			name: "valid config",
			config: &Config{
				URL:     "https://test.com/dedi",
				Timeout: 30,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNew(t *testing.T) {
	ctx := context.Background()

	validConfig := &Config{
		URL:     "https://test.com/dedi",
		Timeout: 30,
	}

	client, closer, err := New(ctx, nil, validConfig)
	if err != nil {
		t.Errorf("New() error = %v", err)
		return
	}

	if client == nil {
		t.Error("New() returned nil client")
	}

	if closer == nil {
		t.Error("New() returned nil closer")
	}

	// Test cleanup
	if err := closer(); err != nil {
		t.Errorf("closer() error = %v", err)
	}

	t.Run("should apply custom retry settings", func(t *testing.T) {
		cfg := &Config{
			URL:      "http://test.com",
			RetryMax: 10,
			RetryWaitMin: 100 * time.Millisecond,
			RetryWaitMax: 1 * time.Second,
		}
		client, _, err := New(ctx, nil, cfg)
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

func TestExtractStringSlice(t *testing.T) {
	ctx := context.Background()

	t.Run("returns strings from []string", func(t *testing.T) {
		got := extractStringSlice(ctx, "network_memberships", []string{"commerce-network.org/prod", "local-commerce.org/production"})
		want := []string{"commerce-network.org/prod", "local-commerce.org/production"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("expected %v, got %v", want, got)
		}
	})

	t.Run("filters non-string entries from []interface{}", func(t *testing.T) {
		got := extractStringSlice(ctx, "network_memberships", []interface{}{"commerce-network.org/prod", 42, true, "", "local-commerce.org/production"})
		want := []string{"commerce-network.org/prod", "local-commerce.org/production"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("expected %v, got %v", want, got)
		}
	})

	t.Run("returns nil for unsupported type", func(t *testing.T) {
		got := extractStringSlice(ctx, "network_memberships", "commerce-network.org/prod")
		if got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})
}

func TestLookup(t *testing.T) {
	ctx := context.Background()

	// Test successful lookup
	t.Run("successful lookup", func(t *testing.T) {
		// Mock server with successful response
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify request method and path
			if r.Method != "GET" {
				t.Errorf("Expected GET request, got %s", r.Method)
			}
			if r.URL.Path != "/dedi/lookup/dev.np2.com/subscribers.beckn.one/test-key-id" {
				t.Errorf("Unexpected path: %s", r.URL.Path)
			}
			// No authorization header expected

			// Return mock response using new DeDi wrapper format
			response := map[string]interface{}{
				"message": "Record retrieved from registry cache",
				"data": map[string]interface{}{
					"record_id": "76EU8vY9TkuJ9T62Sc3FyQLf5Kt9YAVgbZhryX6mFi56ipefkP9d9a",
					"details": map[string]interface{}{
						"url":                "http://dev.np2.com/beckn/bap",
						"type":               "BAP",
						"domain":             "energy",
						"subscriber_id":      "dev.np2.com",
						"signing_public_key": "384qqkIIpxo71WaJPsWqQNWUDGAFnfnJPxuDmtuBiLo=",
						"encr_public_key":    "test-encr-key",
					},
					"network_memberships": []string{"commerce-network.org/prod", "local-commerce.org/production"},
					"created_at":          "2025-10-27T11:45:27.963Z",
					"updated_at":          "2025-10-27T11:46:23.563Z",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		config := &Config{
			URL:          server.URL + "/dedi",
			Timeout:      30,
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "test-key-id",
		}
		results, err := client.Lookup(ctx, req)
		if err != nil {
			t.Errorf("Lookup() error = %v", err)
			return
		}

		if len(results) != 1 {
			t.Errorf("Expected 1 result, got %d", len(results))
			return
		}

		subscription := results[0]
		if subscription.Subscriber.SubscriberID != "dev.np2.com" {
			t.Errorf("Expected subscriber_id dev.np2.com, got %s", subscription.Subscriber.SubscriberID)
		}
		if subscription.SigningPublicKey != "384qqkIIpxo71WaJPsWqQNWUDGAFnfnJPxuDmtuBiLo=" {
			t.Errorf("Expected signing_public_key 384qqkIIpxo71WaJPsWqQNWUDGAFnfnJPxuDmtuBiLo=, got %s", subscription.SigningPublicKey)
		}

		if subscription.KeyID != "test-key-id" {
			t.Errorf("Expected keyID test-key-id, got %s", subscription.KeyID)
		}
	})

	t.Run("allowed network IDs match", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := map[string]interface{}{
				"message": "Record retrieved from registry cache",
				"data": map[string]interface{}{
					"details": map[string]interface{}{
						"url":                "http://dev.np2.com/beckn/bap",
						"type":               "BAP",
						"domain":             "energy",
						"subscriber_id":      "dev.np2.com",
						"signing_public_key": "384qqkIIpxo71WaJPsWqQNWUDGAFnfnJPxuDmtuBiLo=",
					},
					"network_memberships": []string{"commerce-network.org/prod", "local-commerce.org/production"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		config := &Config{
			URL:               server.URL + "/dedi",
			AllowedNetworkIDs: []string{"commerce-network.org/prod"},
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "test-key-id",
		}
		_, err = client.Lookup(ctx, req)
		if err != nil {
			t.Errorf("Lookup() error = %v", err)
		}
	})

	t.Run("allowed network IDs mismatch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := map[string]interface{}{
				"message": "Record retrieved from registry cache",
				"data": map[string]interface{}{
					"details": map[string]interface{}{
						"url":                "http://dev.np2.com/beckn/bap",
						"type":               "BAP",
						"domain":             "energy",
						"subscriber_id":      "dev.np2.com",
						"signing_public_key": "384qqkIIpxo71WaJPsWqQNWUDGAFnfnJPxuDmtuBiLo=",
					},
					"network_memberships": []string{"local-commerce.org/production"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		config := &Config{
			URL:               server.URL + "/dedi",
			AllowedNetworkIDs: []string{"commerce-network/subscriber-references"},
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "test-key-id",
		}
		_, err = client.Lookup(ctx, req)
		if err == nil {
			t.Error("Expected error for disallowed network memberships, got nil")
		}
		expectedErr := "registry entry with subscriber_id 'dev.np2.com' does not belong to any configured networks (registry.config.allowedNetworkIDs)"
		if err.Error() != expectedErr {
			t.Errorf("Expected error %q, got %q", expectedErr, err.Error())
		}
	})

	t.Run("allowed network IDs match with mixed network membership types", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := map[string]interface{}{
				"message": "Record retrieved from registry cache",
				"data": map[string]interface{}{
					"details": map[string]interface{}{
						"url":                "http://dev.np2.com/beckn/bap",
						"type":               "BAP",
						"domain":             "energy",
						"subscriber_id":      "dev.np2.com",
						"signing_public_key": "384qqkIIpxo71WaJPsWqQNWUDGAFnfnJPxuDmtuBiLo=",
					},
					"network_memberships": []interface{}{123, "commerce-network.org/prod", map[string]interface{}{"invalid": true}},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		config := &Config{
			URL:               server.URL + "/dedi",
			AllowedNetworkIDs: []string{"commerce-network.org/prod"},
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "test-key-id",
		}
		_, err = client.Lookup(ctx, req)
		if err != nil {
			t.Errorf("Lookup() error = %v", err)
		}
	})

	// Test empty subscriber ID
	t.Run("empty subscriber ID", func(t *testing.T) {
		config := &Config{
			URL:          "https://test.com/dedi",
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "",
			},
			KeyID: "test-key-id",
		}
		_, err = client.Lookup(ctx, req)
		if err == nil {
			t.Error("Expected error for empty subscriber ID, got nil")
		}
		if err.Error() != "subscriber_id is required for DeDi lookup" {
			t.Errorf("Expected specific error message, got %v", err)
		}
	})

	// Test empty key ID
	t.Run("empty key ID", func(t *testing.T) {
		config := &Config{
			URL:          "https://test.com/dedi",
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "",
		}
		_, err = client.Lookup(ctx, req)
		if err == nil {
			t.Error("Expected error for empty key ID, got nil")
		}
		if err.Error() != "key_id is required for DeDi lookup" {
			t.Errorf("Expected specific error message, got %v", err)
		}
	})

	// Test HTTP error response
	t.Run("http error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Record not found"))
		}))
		defer server.Close()

		config := &Config{
			URL:          server.URL + "/dedi",
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "test-key-id",
		}
		_, err = client.Lookup(ctx, req)
		if err == nil {
			t.Error("Expected error for 404 response, got nil")
		}
	})

	// Test missing signing_public_key
	t.Run("missing signing_public_key", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"details": map[string]interface{}{
						"subscriber_id": "dev.np2.com",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		config := &Config{
			URL:          server.URL + "/dedi",
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "test-key-id",
		}
		_, err = client.Lookup(ctx, req)
		if err == nil {
			t.Error("Expected error for missing signing_public_key, got nil")
		}
	})

	// Test invalid JSON response
	t.Run("invalid json response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("invalid json"))
		}))
		defer server.Close()

		config := &Config{
			URL:          server.URL + "/dedi",
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "test-key-id",
		}
		_, err = client.Lookup(ctx, req)
		if err == nil {
			t.Error("Expected error for invalid JSON, got nil")
		}
	})

	// Test missing data field
	t.Run("missing data field", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := map[string]interface{}{
				"message": "success",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		config := &Config{
			URL:          server.URL + "/dedi",
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "test-key-id",
		}
		_, err = client.Lookup(ctx, req)
		if err == nil {
			t.Error("Expected error for missing data field, got nil")
		}
	})

	// Test network error
	t.Run("network error", func(t *testing.T) {
		config := &Config{
			URL:          "http://invalid-url-that-does-not-exist.local/dedi",
			Timeout:      1,
		}

		client, closer, err := New(ctx, nil, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		req := &model.Subscription{
			Subscriber: model.Subscriber{
				SubscriberID: "dev.np2.com",
			},
			KeyID: "test-key-id",
		}
		_, err = client.Lookup(ctx, req)
		if err == nil {
			t.Error("Expected network error, got nil")
		}
	})
}

func TestLookupRegistry(t *testing.T) {
	ctx := context.Background()

	t.Run("successful registry metadata lookup", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Errorf("Expected GET request, got %s", r.Method)
			}
			if r.URL.Path != "/dedi/lookup/nfo.example.org/mobility-network" {
				t.Errorf("Unexpected path: %s", r.URL.Path)
			}

			response := map[string]interface{}{
				"message": "Resource retrieved successfully",
				"data": map[string]interface{}{
					"registry_name": "mobility-network",
					"meta": map[string]interface{}{
						"manifestUrl":                  "https://example.org/manifest.yaml",
						"manifestSignatureUrl":        "https://example.org/manifest.yaml.sig",
						"signingPublicKeyLookupUrl": "https://example.org/keys/manifest",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		client, closer, err := New(ctx, nil, &Config{
			URL:          server.URL + "/dedi",
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		got, err := client.LookupRegistry(ctx, "nfo.example.org", "mobility-network")
		if err != nil {
			t.Fatalf("LookupRegistry() error = %v", err)
		}
		if got.NamespaceIdentifier != "nfo.example.org" {
			t.Fatalf("expected NamespaceIdentifier %q, got %q", "nfo.example.org", got.NamespaceIdentifier)
		}
		if got.RegistryName != "mobility-network" {
			t.Fatalf("expected RegistryName %q, got %q", "mobility-network", got.RegistryName)
		}
		if got.RawMeta["manifestUrl"] != "https://example.org/manifest.yaml" {
			t.Fatalf("expected manifest_url metadata to be preserved")
		}
	})

	t.Run("missing meta returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"registry_name": "mobility-network",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		client, closer, err := New(ctx, nil, &Config{
			URL:          server.URL,
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		if _, err := client.LookupRegistry(ctx, "nfo.example.org", "mobility-network"); err == nil {
			t.Fatal("expected error for missing meta")
		}
	})

	t.Run("non-string meta values are ignored", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"meta": map[string]interface{}{
						"manifestUrl":   "https://example.org/manifest.yaml",
						"non_string_key": true,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		client, closer, err := New(ctx, nil, &Config{
			URL:          server.URL,
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		got, err := client.LookupRegistry(ctx, "nfo.example.org", "mobility-network")
		if err != nil {
			t.Fatalf("LookupRegistry() error = %v", err)
		}
		if _, ok := got.RawMeta["non_string_key"]; ok {
			t.Fatal("expected non-string metadata value to be omitted")
		}
	})

	t.Run("http error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Record not found"))
		}))
		defer server.Close()

		client, closer, err := New(ctx, nil, &Config{
			URL:          server.URL + "/dedi",
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		if _, err := client.LookupRegistry(ctx, "nfo.example.org", "mobility-network"); err == nil {
			t.Error("expected error for 404 response, got nil")
		}
	})
}

func dediLookupResponse(ttl float64) map[string]interface{} {
	resp := map[string]interface{}{
		"message": "ok",
		"data": map[string]interface{}{
			"details": map[string]interface{}{
				"url":                "http://sub.example.com",
				"type":               "BAP",
				"domain":             "retail",
				"subscriber_id":      "sub.example.com",
				"signing_public_key": "test-signing-key",
				"encr_public_key":    "test-encr-key",
			},
			"network_memberships": []string{"commerce.org/prod"},
			"created_at":          "2025-01-01T00:00:00Z",
			"updated_at":          "2025-01-01T00:00:00Z",
		},
	}
	if ttl > 0 {
		resp["data"].(map[string]interface{})["ttl"] = ttl
	}
	return resp
}

func TestDeDiRegistryClient_Lookup_Cache(t *testing.T) {
	ctx := context.Background()
	sub := &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: "sub.example.com"},
		KeyID:      "key-1",
	}
	expectedCacheKey := "dedi_lookup_sub.example.com_key-1"

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
		client, closer, err := New(ctx, cache, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		results, err := client.Lookup(ctx, sub)
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

	t.Run("cache miss writes to cache using ttl from response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(dediLookupResponse(600))
		}))
		defer server.Close()

		cache := &mockCache{}
		client, closer, err := New(ctx, cache, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		results, err := client.Lookup(ctx, sub)
		if err != nil {
			t.Fatalf("Lookup() unexpected error: %v", err)
		}
		if len(results) != 1 || results[0].SigningPublicKey != "test-signing-key" {
			t.Errorf("unexpected result: %+v", results)
		}
		if cache.setKey != expectedCacheKey {
			t.Errorf("expected cache key %q, got %q", expectedCacheKey, cache.setKey)
		}
		if cache.setTTL != 600*time.Second {
			t.Errorf("expected TTL 600s from response, got %v", cache.setTTL)
		}
	})

	t.Run("nil cache — no cache operations", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(dediLookupResponse(600))
		}))
		defer server.Close()

		client, closer, err := New(ctx, nil, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		results, err := client.Lookup(ctx, sub)
		if err != nil {
			t.Fatalf("Lookup() unexpected error: %v", err)
		}
		if len(results) != 1 || results[0].SigningPublicKey != "test-signing-key" {
			t.Errorf("unexpected result: %+v", results)
		}
	})

	t.Run("cache set error does not fail lookup", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(dediLookupResponse(600))
		}))
		defer server.Close()

		cache := &mockCache{setErr: errors.New("redis down")}
		client, closer, err := New(ctx, cache, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		results, err := client.Lookup(ctx, sub)
		if err != nil {
			t.Fatalf("Lookup() must not fail when cache.Set errors, got: %v", err)
		}
		if len(results) != 1 || results[0].SigningPublicKey != "test-signing-key" {
			t.Errorf("unexpected result: %+v", results)
		}
	})

	t.Run("corrupt cache value falls through to HTTP", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(dediLookupResponse(0))
		}))
		defer server.Close()

		cache := &mockCache{
			getFunc: func(ctx context.Context, key string) (string, error) {
				return "this is not valid json{{{{", nil
			},
		}
		client, closer, err := New(ctx, cache, &Config{URL: server.URL})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		results, err := client.Lookup(ctx, sub)
		if err != nil {
			t.Fatalf("Lookup() unexpected error: %v", err)
		}
		if len(results) != 1 || results[0].SigningPublicKey != "test-signing-key" {
			t.Errorf("expected HTTP result after corrupt cache, got %+v", results)
		}
	})

	t.Run("cache hit enforces allowedNetworkIDs", func(t *testing.T) {
		cached := []model.Subscription{{
			Subscriber:         model.Subscriber{SubscriberID: "sub.example.com"},
			SigningPublicKey:    "cached-key",
			NetworkMemberships: []string{"commerce.org/prod"},
		}}
		cachedJSON, _ := json.Marshal(cached)

		cache := &mockCache{
			getFunc: func(ctx context.Context, key string) (string, error) {
				return string(cachedJSON), nil
			},
		}
		client, closer, err := New(ctx, cache, &Config{
			URL:               "http://unused",
			AllowedNetworkIDs: []string{"other.org/prod"},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		_, err = client.Lookup(ctx, sub)
		if err == nil {
			t.Error("expected error when cached memberships do not match allowedNetworkIDs")
		}
	})
}

// makeStepCtx returns a *model.StepContext with network_id stored as a context value
// (matching the production flow where reqpreprocessor sets it before any OTel wrapping).
func makeStepCtx(networkID string) *model.StepContext {
	goCtx := context.Background()
	if networkID != "" {
		goCtx = context.WithValue(goCtx, model.ContextKeyNetworkID, networkID)
	}
	return &model.StepContext{Context: goCtx}
}

// registryHandlerWithMemberships returns an httptest.HandlerFunc that responds with
// a DeDi-shaped payload carrying the given subscriber and network_memberships.
func registryHandlerWithMemberships(subscriberID, signingKey string, memberships []string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"message": "Record retrieved from registry cache",
			"data": map[string]interface{}{
				"details": map[string]interface{}{
					"url":                "http://example.com/beckn",
					"type":               "BAP",
					"domain":             "retail",
					"subscriber_id":      subscriberID,
					"signing_public_key": signingKey,
				},
				"network_memberships": memberships,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}

func TestContextNetworkIDValidation(t *testing.T) {
	const (
		sub     = "np.example.com"
		key     = "test-signing-key-base64="
		net1    = "nfo1.com/retail"
		net2    = "nfo2.com/retail"
	)

	req := &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: sub},
		KeyID:      "key-001",
	}

	type tc struct {
		name        string
		memberships []string
		allowlist   []string
		networkID   string // empty string means absent from body
		wantErr     bool
	}

	cases := []tc{
		// allow cases
		{
			name:        "allow — network_id matches memberships and allowlist",
			memberships: []string{net1},
			allowlist:   []string{net1},
			networkID:   net1,
			wantErr:     false,
		},
		{
			name:        "allow — no allowlist, network_id matches memberships",
			memberships: []string{net1},
			allowlist:   nil,
			networkID:   net1,
			wantErr:     false,
		},
		{
			name:        "allow — no network_id present, allowlist set",
			memberships: []string{net1},
			allowlist:   []string{net1},
			networkID:   "",
			wantErr:     false,
		},
		{
			name:        "allow — no network_id present, no allowlist",
			memberships: []string{net1},
			allowlist:   nil,
			networkID:   "",
			wantErr:     false,
		},
		{
			name:        "allow — network_id in memberships and in multi-entry allowlist",
			memberships: []string{net1},
			allowlist:   []string{net1, net2},
			networkID:   net1,
			wantErr:     false,
		},
		// block cases
		{
			name:        "block — network_id not in memberships (allowlist set)",
			memberships: []string{net1},
			allowlist:   []string{net1},
			networkID:   net2,
			wantErr:     true,
		},
		{
			name:        "block — network_id not in memberships (no allowlist)",
			memberships: []string{net1},
			allowlist:   nil,
			networkID:   net2,
			wantErr:     true,
		},
		{
			name:        "block — multi-membership, network_id not in allowlist",
			memberships: []string{net1, net2},
			allowlist:   []string{net1},
			networkID:   net2,
			wantErr:     true,
		},
		{
			name:        "block — empty memberships, no allowlist, network_id present",
			memberships: nil,
			allowlist:   nil,
			networkID:   net1,
			wantErr:     true,
		},
		{
			name:        "block — network_id in memberships but not in allowlist",
			memberships: []string{net1},
			allowlist:   []string{net1, net2},
			networkID:   net2,
			wantErr:     true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(registryHandlerWithMemberships(sub, key, tt.memberships))
			defer server.Close()

			cfg := &Config{
				URL:               server.URL + "/dedi",
				AllowedNetworkIDs: tt.allowlist,
			}
			client, closer, err := New(context.Background(), nil, cfg)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer closer()

			ctx := makeStepCtx(tt.networkID)
			_, err = client.Lookup(ctx, req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Lookup() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestExtractContextNetworkID(t *testing.T) {
	// Primary path: context value set by reqpreprocessor.
	t.Run("context value (primary path)", func(t *testing.T) {
		goCtx := context.WithValue(context.Background(), model.ContextKeyNetworkID, "nfo1.com/retail")
		sc := &model.StepContext{Context: goCtx}
		if got := extractContextNetworkID(sc); got != "nfo1.com/retail" {
			t.Errorf("got %q, want %q", got, "nfo1.com/retail")
		}
	})

	t.Run("context value on plain context (no StepContext)", func(t *testing.T) {
		goCtx := context.WithValue(context.Background(), model.ContextKeyNetworkID, "nfo1.com/retail")
		if got := extractContextNetworkID(goCtx); got != "nfo1.com/retail" {
			t.Errorf("got %q, want %q", got, "nfo1.com/retail")
		}
	})

	t.Run("context value takes precedence over body", func(t *testing.T) {
		goCtx := context.WithValue(context.Background(), model.ContextKeyNetworkID, "nfo1.com/retail")
		sc := &model.StepContext{
			Context: goCtx,
			Body:    []byte(`{"context":{"network_id":"nfo2.com/retail"}}`),
		}
		if got := extractContextNetworkID(sc); got != "nfo1.com/retail" {
			t.Errorf("context value should win: got %q, want %q", got, "nfo1.com/retail")
		}
	})

	// Fallback path: body parsing when context value is absent.
	t.Run("snake_case network_id in body (fallback)", func(t *testing.T) {
		sc := &model.StepContext{
			Context: context.Background(),
			Body:    []byte(`{"context":{"network_id":"nfo1.com/retail"}}`),
		}
		if got := extractContextNetworkID(sc); got != "nfo1.com/retail" {
			t.Errorf("got %q, want %q", got, "nfo1.com/retail")
		}
	})

	t.Run("camelCase networkId in body (fallback)", func(t *testing.T) {
		sc := &model.StepContext{
			Context: context.Background(),
			Body:    []byte(`{"context":{"networkId":"nfo2.com/retail"}}`),
		}
		if got := extractContextNetworkID(sc); got != "nfo2.com/retail" {
			t.Errorf("got %q, want %q", got, "nfo2.com/retail")
		}
	})

	// Absent / empty cases.
	t.Run("absent from both context value and body", func(t *testing.T) {
		sc := &model.StepContext{
			Context: context.Background(),
			Body:    []byte(`{"context":{}}`),
		}
		if got := extractContextNetworkID(sc); got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("plain context with no context value returns empty", func(t *testing.T) {
		if got := extractContextNetworkID(context.Background()); got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}
