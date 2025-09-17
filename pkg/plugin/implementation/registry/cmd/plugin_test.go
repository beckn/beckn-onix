package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryProvider_New(t *testing.T) {
	tests := []struct {
		name        string
		config      map[string]string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config with all parameters",
			config: map[string]string{
				"url":            "http://localhost:8080",
				"retry_max":      "3",
				"retry_wait_min": "100ms",
				"retry_wait_max": "500ms",
			},
			expectError: false,
		},
		{
			name: "minimal valid config",
			config: map[string]string{
				"url": "http://localhost:8080",
			},
			expectError: false,
		},
		{
			name:        "missing URL",
			config:      map[string]string{},
			expectError: true,
			errorMsg:    "registry URL cannot be empty",
		},
		{
			name: "invalid retry_max",
			config: map[string]string{
				"url":       "http://localhost:8080",
				"retry_max": "invalid",
			},
			expectError: false, // Invalid values are ignored, not errors
		},
		{
			name: "invalid retry_wait_min",
			config: map[string]string{
				"url":            "http://localhost:8080",
				"retry_wait_min": "invalid",
			},
			expectError: false, // Invalid values are ignored, not errors
		},
		{
			name: "invalid retry_wait_max",
			config: map[string]string{
				"url":            "http://localhost:8080",
				"retry_wait_max": "invalid",
			},
			expectError: false, // Invalid values are ignored, not errors
		},
		{
			name: "empty URL",
			config: map[string]string{
				"url": "",
			},
			expectError: true,
			errorMsg:    "registry URL cannot be empty",
		},
	}

	provider := registryProvider{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			registry, closer, err := provider.New(ctx, tt.config)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, registry)
				assert.Nil(t, closer)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, registry)
				assert.NotNil(t, closer)

				// Test that closer works
				err = closer()
				assert.NoError(t, err)
			}
		})
	}
}

func TestRegistryProvider_NilContext(t *testing.T) {
	provider := registryProvider{}
	config := map[string]string{
		"url": "http://localhost:8080",
	}

	registry, closer, err := provider.New(context.TODO(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context cannot be nil")
	assert.Nil(t, registry)
	assert.Nil(t, closer)
}

func TestRegistryProvider_IntegrationTest(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subscribe":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{}"))
		case "/lookup":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider := registryProvider{}
	config := map[string]string{
		"url":            server.URL,
		"retry_max":      "2",
		"retry_wait_min": "10ms",
		"retry_wait_max": "20ms",
	}

	ctx := context.Background()
	registry, closer, err := provider.New(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, registry)
	require.NotNil(t, closer)
	defer closer()

	// Test Subscribe
	subscription := &model.Subscription{
		Subscriber: model.Subscriber{
			SubscriberID: "test-subscriber",
			URL:          "https://example.com",
			Type:         "BAP",
			Domain:       "mobility",
		},
		KeyID:            "test-key",
		SigningPublicKey: "test-signing-key",
		EncrPublicKey:    "test-encryption-key",
		ValidFrom:        time.Now(),
		ValidUntil:       time.Now().Add(24 * time.Hour),
		Status:           "SUBSCRIBED",
	}

	err = registry.Subscribe(ctx, subscription)
	require.NoError(t, err)

	// Test Lookup
	results, err := registry.Lookup(ctx, subscription)
	require.NoError(t, err)
	assert.NotNil(t, results)
	assert.Len(t, results, 0) // Empty array response from test server
}

func TestRegistryProvider_ConfigurationParsing(t *testing.T) {
	tests := []struct {
		name           string
		config         map[string]string
		expectedConfig map[string]interface{}
	}{
		{
			name: "all parameters set",
			config: map[string]string{
				"url":            "http://localhost:8080",
				"retry_max":      "5",
				"retry_wait_min": "200ms",
				"retry_wait_max": "1s",
			},
			expectedConfig: map[string]interface{}{
				"url":            "http://localhost:8080",
				"retry_max":      5,
				"retry_wait_min": 200 * time.Millisecond,
				"retry_wait_max": 1 * time.Second,
			},
		},
		{
			name: "only required parameters",
			config: map[string]string{
				"url": "https://registry.example.com",
			},
			expectedConfig: map[string]interface{}{
				"url": "https://registry.example.com",
			},
		},
		{
			name: "invalid numeric values ignored",
			config: map[string]string{
				"url":       "http://localhost:8080",
				"retry_max": "not-a-number",
			},
			expectedConfig: map[string]interface{}{
				"url": "http://localhost:8080",
			},
		},
		{
			name: "invalid duration values ignored",
			config: map[string]string{
				"url":            "http://localhost:8080",
				"retry_wait_min": "not-a-duration",
				"retry_wait_max": "also-not-a-duration",
			},
			expectedConfig: map[string]interface{}{
				"url": "http://localhost:8080",
			},
		},
	}

	// Create a test server that just returns OK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	provider := registryProvider{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Override URL with test server URL for testing
			testConfig := make(map[string]string)
			for k, v := range tt.config {
				testConfig[k] = v
			}
			testConfig["url"] = server.URL

			ctx := context.Background()
			registry, closer, err := provider.New(ctx, testConfig)
			require.NoError(t, err)
			require.NotNil(t, registry)
			require.NotNil(t, closer)
			defer closer()

			// The registry should work regardless of invalid config values
			subscription := &model.Subscription{
				KeyID:            "test-key",
				SigningPublicKey: "test-signing-key",
				EncrPublicKey:    "test-encryption-key",
				ValidFrom:        time.Now(),
				ValidUntil:       time.Now().Add(24 * time.Hour),
				Status:           "SUBSCRIBED",
			}

			err = registry.Subscribe(ctx, subscription)
			assert.NoError(t, err)
		})
	}
}
