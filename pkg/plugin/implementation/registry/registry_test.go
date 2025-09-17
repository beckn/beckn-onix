package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew tests the New function for creating RegistryClient instances
func TestNew(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: &Config{
				URL:          "http://localhost:8080",
				RetryMax:     3,
				RetryWaitMin: time.Millisecond * 100,
				RetryWaitMax: time.Millisecond * 500,
			},
			expectError: false,
		},
		{
			name:        "nil config",
			config:      nil,
			expectError: true,
			errorMsg:    "registry config cannot be nil",
		},
		{
			name: "empty URL",
			config: &Config{
				URL: "",
			},
			expectError: true,
			errorMsg:    "registry URL cannot be empty",
		},
		{
			name: "minimal valid config",
			config: &Config{
				URL: "http://example.com",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client, closer, err := New(ctx, tt.config)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, client)
				assert.Nil(t, closer)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, client)
				assert.NotNil(t, closer)

				// Test that closer works without error
				err = closer()
				assert.NoError(t, err)

				// Verify config is set correctly
				assert.Equal(t, tt.config.URL, client.config.URL)
			}
		})
	}
}

// TestSubscribeSuccess verifies that the Subscribe function succeeds when the server responds with HTTP 200.
func TestSubscribeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and headers
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Contains(t, r.URL.Path, "/subscribe")

		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("{}")); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		URL:          server.URL,
		RetryMax:     3,
		RetryWaitMin: time.Millisecond * 100,
		RetryWaitMax: time.Millisecond * 500,
	}

	ctx := context.Background()
	client, closer, err := New(ctx, config)
	require.NoError(t, err)
	defer closer()

	subscription := &model.Subscription{
		KeyID:            "test-key",
		SigningPublicKey: "test-signing-key",
		EncrPublicKey:    "test-encryption-key",
		ValidFrom:        time.Now(),
		ValidUntil:       time.Now().Add(24 * time.Hour),
		Status:           "SUBSCRIBED",
	}

	err = client.Subscribe(context.Background(), subscription)
	require.NoError(t, err)
}

// TestSubscribeFailure tests different failure scenarios for Subscribe.
func TestSubscribeFailure(t *testing.T) {
	tests := []struct {
		name          string
		responseCode  int
		responseBody  string
		expectError   bool
		errorContains string
		setupServer   func() *httptest.Server
		config        *Config
	}{
		{
			name:          "Internal Server Error",
			responseCode:  http.StatusInternalServerError,
			responseBody:  "Internal Server Error",
			expectError:   true,
			errorContains: "subscribe request failed with status",
		},
		{
			name:          "Bad Request",
			responseCode:  http.StatusBadRequest,
			responseBody:  "Bad Request",
			expectError:   true,
			errorContains: "subscribe request failed with status",
		},
		{
			name:          "Not Found",
			responseCode:  http.StatusNotFound,
			responseBody:  "Not Found",
			expectError:   true,
			errorContains: "subscribe request failed with status",
		},
		{
			name: "Connection Refused",
			setupServer: func() *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
				server.Close() // Close immediately to simulate connection refused
				return server
			},
			expectError:   true,
			errorContains: "failed to send subscribe request with retry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server *httptest.Server

			if tt.setupServer != nil {
				server = tt.setupServer()
			} else {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tt.responseCode)
					if _, err := w.Write([]byte(tt.responseBody)); err != nil {
						t.Errorf("failed to write response: %v", err)
					}
				}))
				defer server.Close()
			}

			config := &Config{
				URL:          server.URL,
				RetryMax:     1,
				RetryWaitMin: 1 * time.Millisecond,
				RetryWaitMax: 2 * time.Millisecond,
			}

			ctx := context.Background()
			client, closer, err := New(ctx, config)
			require.NoError(t, err)
			defer closer()

			subscription := &model.Subscription{
				KeyID:            "test-key",
				SigningPublicKey: "test-signing-key",
				EncrPublicKey:    "test-encryption-key",
				ValidFrom:        time.Now(),
				ValidUntil:       time.Now().Add(24 * time.Hour),
				Status:           "SUBSCRIBED",
			}

			err = client.Subscribe(context.Background(), subscription)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestLookupSuccess tests successful lookup scenarios.
func TestLookupSuccess(t *testing.T) {
	expectedResponse := []model.Subscription{
		{
			Subscriber: model.Subscriber{
				SubscriberID: "123",
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
		},
		{
			Subscriber: model.Subscriber{
				SubscriberID: "456",
				URL:          "https://example2.com",
				Type:         "BPP",
				Domain:       "retail",
			},
			KeyID:            "test-key-2",
			SigningPublicKey: "test-signing-key-2",
			EncrPublicKey:    "test-encryption-key-2",
			ValidFrom:        time.Now(),
			ValidUntil:       time.Now().Add(48 * time.Hour),
			Status:           "SUBSCRIBED",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and headers
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Contains(t, r.URL.Path, "/lookup")

		w.WriteHeader(http.StatusOK)
		bodyBytes, _ := json.Marshal(expectedResponse)
		if _, err := w.Write(bodyBytes); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		URL:          server.URL,
		RetryMax:     1,
		RetryWaitMin: 1 * time.Millisecond,
		RetryWaitMax: 2 * time.Millisecond,
	}

	ctx := context.Background()
	client, closer, err := New(ctx, config)
	require.NoError(t, err)
	defer closer()

	subscription := &model.Subscription{
		Subscriber: model.Subscriber{
			SubscriberID: "123",
		},
		KeyID:            "test-key",
		SigningPublicKey: "test-signing-key",
		EncrPublicKey:    "test-encryption-key",
		ValidFrom:        time.Now(),
		ValidUntil:       time.Now().Add(24 * time.Hour),
		Status:           "SUBSCRIBED",
	}

	result, err := client.Lookup(ctx, subscription)
	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Len(t, result, 2)
	assert.Equal(t, expectedResponse[0].Subscriber.SubscriberID, result[0].Subscriber.SubscriberID)
	assert.Equal(t, expectedResponse[1].Subscriber.SubscriberID, result[1].Subscriber.SubscriberID)
}

// TestLookupFailure tests failure scenarios for the Lookup function.
func TestLookupFailure(t *testing.T) {
	tests := []struct {
		name          string
		responseBody  interface{}
		responseCode  int
		setupServer   func() *httptest.Server
		expectError   bool
		errorContains string
	}{
		{
			name:          "Non-200 status code",
			responseBody:  "Internal Server Error",
			responseCode:  http.StatusInternalServerError,
			expectError:   true,
			errorContains: "lookup request failed with status",
		},
		{
			name:          "Invalid JSON response",
			responseBody:  "Invalid JSON",
			responseCode:  http.StatusOK,
			expectError:   true,
			errorContains: "failed to unmarshal response body",
		},
		{
			name: "Connection error",
			setupServer: func() *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
				server.Close() // Close immediately to simulate connection error
				return server
			},
			expectError:   true,
			errorContains: "failed to send lookup request with retry",
		},
		{
			name:          "Empty response body with 200 status",
			responseBody:  "",
			responseCode:  http.StatusOK,
			expectError:   true,
			errorContains: "failed to unmarshal response body",
		},
		{
			name:         "Valid empty array response",
			responseBody: []model.Subscription{},
			responseCode: http.StatusOK,
			expectError:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var server *httptest.Server

			if tc.setupServer != nil {
				server = tc.setupServer()
			} else {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.responseCode != 0 {
						w.WriteHeader(tc.responseCode)
					}
					if tc.responseBody != nil {
						if str, ok := tc.responseBody.(string); ok {
							if _, err := w.Write([]byte(str)); err != nil {
								t.Errorf("failed to write response: %v", err)
							}
						} else {
							bodyBytes, _ := json.Marshal(tc.responseBody)
							if _, err := w.Write(bodyBytes); err != nil {
								t.Errorf("failed to write response: %v", err)
							}
						}
					}
				}))
				defer server.Close()
			}

			config := &Config{
				URL:          server.URL,
				RetryMax:     0,
				RetryWaitMin: 1 * time.Millisecond,
				RetryWaitMax: 2 * time.Millisecond,
			}

			ctx := context.Background()
			client, closer, err := New(ctx, config)
			require.NoError(t, err)
			defer closer()

			subscription := &model.Subscription{
				Subscriber:       model.Subscriber{},
				KeyID:            "test-key",
				SigningPublicKey: "test-signing-key",
				EncrPublicKey:    "test-encryption-key",
				ValidFrom:        time.Now(),
				ValidUntil:       time.Now().Add(24 * time.Hour),
				Status:           "SUBSCRIBED",
			}

			result, err := client.Lookup(ctx, subscription)
			if tc.expectError {
				require.Error(t, err)
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
				assert.Empty(t, result)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

// TestContextCancellation tests that operations respect context cancellation
func TestContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow server
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	config := &Config{
		URL:          server.URL,
		RetryMax:     0,
		RetryWaitMin: 1 * time.Millisecond,
		RetryWaitMax: 2 * time.Millisecond,
	}

	ctx := context.Background()
	client, closer, err := New(ctx, config)
	require.NoError(t, err)
	defer closer()

	subscription := &model.Subscription{
		KeyID:            "test-key",
		SigningPublicKey: "test-signing-key",
		EncrPublicKey:    "test-encryption-key",
		ValidFrom:        time.Now(),
		ValidUntil:       time.Now().Add(24 * time.Hour),
		Status:           "SUBSCRIBED",
	}

	t.Run("Subscribe with cancelled context", func(t *testing.T) {
		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		err := client.Subscribe(cancelledCtx, subscription)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})

	t.Run("Lookup with cancelled context", func(t *testing.T) {
		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		result, err := client.Lookup(cancelledCtx, subscription)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
		assert.Empty(t, result)
	})
}

// TestRetryConfiguration tests that retry configuration is properly applied
func TestRetryConfiguration(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Server Error"))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{}"))
		}
	}))
	defer server.Close()

	config := &Config{
		URL:          server.URL,
		RetryMax:     3,
		RetryWaitMin: 1 * time.Millisecond,
		RetryWaitMax: 2 * time.Millisecond,
	}

	ctx := context.Background()
	client, closer, err := New(ctx, config)
	require.NoError(t, err)
	defer closer()

	subscription := &model.Subscription{
		KeyID:            "test-key",
		SigningPublicKey: "test-signing-key",
		EncrPublicKey:    "test-encryption-key",
		ValidFrom:        time.Now(),
		ValidUntil:       time.Now().Add(24 * time.Hour),
		Status:           "SUBSCRIBED",
	}

	// This should succeed after retries
	err = client.Subscribe(context.Background(), subscription)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, attempts, 3)
}
