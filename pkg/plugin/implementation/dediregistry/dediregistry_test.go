package dediregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
			name: "empty baseURL",
			config: &Config{
				BaseURL:      "",
				ApiKey:       "test-key",
				NamespaceID:  "test-namespace",
				RegistryName: "test-registry",
				RecordName:   "test-record",
			},
			wantErr: true,
		},
		{
			name: "empty apiKey",
			config: &Config{
				BaseURL:      "https://test.com",
				ApiKey:       "",
				NamespaceID:  "test-namespace",
				RegistryName: "test-registry",
				RecordName:   "test-record",
			},
			wantErr: true,
		},
		{
			name: "valid config",
			config: &Config{
				BaseURL:      "https://test.com",
				ApiKey:       "test-key",
				NamespaceID:  "test-namespace",
				RegistryName: "test-registry",
				RecordName:   "test-record",
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
		BaseURL:      "https://test.com",
		ApiKey:       "test-key",
		NamespaceID:  "test-namespace",
		RegistryName: "test-registry",
		RecordName:   "test-record",
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
			if r.URL.Path != "/dedi/lookup/test-namespace/test-registry/test-record" {
				t.Errorf("Unexpected path: %s", r.URL.Path)
			}
			// Verify Authorization header
			if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
				t.Errorf("Expected Bearer test-key, got %s", auth)
			}

			// Return mock response
			response := model.DeDiResponse{
				Data: model.DeDiRecord{
					Schema: model.DeDiSchema{
						EntityName:  "test.example.com",
						PublicKey:   "test-public-key",
						KeyType:     "ed25519",
						KeyFormat:   "base64",
					},
					State: "active",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		config := &Config{
			BaseURL:      server.URL,
			ApiKey:       "test-key",
			NamespaceID:  "test-namespace",
			RegistryName: "test-registry",
			RecordName:   "test-record",
			Timeout:      30,
		}

		client, closer, err := New(ctx, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		record, err := client.Lookup(ctx)
		if err != nil {
			t.Errorf("Lookup() error = %v", err)
			return
		}

		if record.Schema.EntityName != "test.example.com" {
			t.Errorf("Expected entity_name test.example.com, got %s", record.Schema.EntityName)
		}
		if record.Schema.PublicKey != "test-public-key" {
			t.Errorf("Expected public_key test-public-key, got %s", record.Schema.PublicKey)
		}
		if record.State != "active" {
			t.Errorf("Expected state active, got %s", record.State)
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
			BaseURL:      server.URL,
			ApiKey:       "test-key",
			NamespaceID:  "test-namespace",
			RegistryName: "test-registry",
			RecordName:   "test-record",
		}

		client, closer, err := New(ctx, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		_, err = client.Lookup(ctx)
		if err == nil {
			t.Error("Expected error for 404 response, got nil")
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
			BaseURL:      server.URL,
			ApiKey:       "test-key",
			NamespaceID:  "test-namespace",
			RegistryName: "test-registry",
			RecordName:   "test-record",
		}

		client, closer, err := New(ctx, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		_, err = client.Lookup(ctx)
		if err == nil {
			t.Error("Expected error for invalid JSON, got nil")
		}
	})

	// Test network error
	t.Run("network error", func(t *testing.T) {
		config := &Config{
			BaseURL:      "http://invalid-url-that-does-not-exist.local",
			ApiKey:       "test-key",
			NamespaceID:  "test-namespace",
			RegistryName: "test-registry",
			RecordName:   "test-record",
			Timeout:      1,
		}

		client, closer, err := New(ctx, config)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer closer()

		_, err = client.Lookup(ctx)
		if err == nil {
			t.Error("Expected network error, got nil")
		}
	})
}