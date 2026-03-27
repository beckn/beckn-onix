package dediregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

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
				URL:          "https://test.com/dedi",
				RegistryName: "subscribers.beckn.one",
				Timeout:      30,
			},
			wantErr: false,
		},
		{
			name: "missing registry name",
			config: &Config{
				URL:     "https://test.com/dedi",
				Timeout: 30,
			},
			wantErr: true,
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
		URL:          "https://test.com/dedi",
		RegistryName: "subscribers.beckn.one",
		Timeout:      30,
	}

	client, closer, err := New(ctx, validConfig)
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
			URL:          "http://test.com",
			RegistryName: "subscribers.beckn.one",
			RetryMax:     10,
			RetryWaitMin: 100 * time.Millisecond,
			RetryWaitMax: 1 * time.Second,
		}
		client, _, err := New(ctx, cfg)
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
					"network_memberships": []string{"commerce-network/subscriber-references", "local-commerce/subscriber-references"},
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
			RegistryName: "subscribers.beckn.one",
			Timeout:      30,
		}

		client, closer, err := New(ctx, config)
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
					"network_memberships": []string{"commerce-network/subscriber-references", "local-commerce/subscriber-references"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		config := &Config{
			URL:               server.URL + "/dedi",
			RegistryName:      "subscribers.beckn.one",
			AllowedNetworkIDs: []string{"commerce-network/subscriber-references"},
		}

		client, closer, err := New(ctx, config)
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
					"network_memberships": []string{"local-commerce/subscriber-references"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		config := &Config{
			URL:               server.URL + "/dedi",
			RegistryName:      "subscribers.beckn.one",
			AllowedNetworkIDs: []string{"commerce-network/subscriber-references"},
		}

		client, closer, err := New(ctx, config)
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

	// Test empty subscriber ID
	t.Run("empty subscriber ID", func(t *testing.T) {
		config := &Config{
			URL:          "https://test.com/dedi",
			RegistryName: "subscribers.beckn.one",
		}

		client, closer, err := New(ctx, config)
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
			RegistryName: "subscribers.beckn.one",
		}

		client, closer, err := New(ctx, config)
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
			RegistryName: "subscribers.beckn.one",
		}

		client, closer, err := New(ctx, config)
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
			RegistryName: "subscribers.beckn.one",
		}

		client, closer, err := New(ctx, config)
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
			RegistryName: "subscribers.beckn.one",
		}

		client, closer, err := New(ctx, config)
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
			RegistryName: "subscribers.beckn.one",
		}

		client, closer, err := New(ctx, config)
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
			RegistryName: "subscribers.beckn.one",
			Timeout:      1,
		}

		client, closer, err := New(ctx, config)
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
