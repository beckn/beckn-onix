package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubscribeSuccess verifies that the Subscribe function succeeds when the server responds with HTTP 200.
func TestSubscribeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("{}")); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewRegisteryClient(&Config{
		RegisteryURL: server.URL,
		RetryMax:     3,
		RetryWaitMin: time.Millisecond * 100,
		RetryWaitMax: time.Millisecond * 500,
	})

	subscription := &model.Subscription{
		KeyID:            "test-key",
		SigningPublicKey: "test-signing-key",
		EncrPublicKey:    "test-encryption-key",
		ValidFrom:        time.Now(),
		ValidUntil:       time.Now().Add(24 * time.Hour),
		Status:           "SUBSCRIBED",
	}
	err := client.Subscribe(context.Background(), subscription)
	if err != nil {
		t.Fatalf("Subscribe() failed with error: %v", err)
	}
}

// TestSubscribeFailure tests different failure scenarios using a mock client.
func TestSubscribeFailure(t *testing.T) {
	tests := []struct {
		name      string
		mockError error
	}{
		{
			name:      "Failed subscription - Internal Server Error",
			mockError: errors.New("internal server error"),
		},
		{
			name:      "Failed subscription - Bad Request",
			mockError: errors.New("bad request"),
		},
		{
			name:      "Request Timeout",
			mockError: context.DeadlineExceeded,
		},
		{
			name:      "Network Failure",
			mockError: errors.New("network failure"),
		},
		{
			name:      "JSON Marshalling Failure",
			mockError: errors.New("json marshalling failure"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewRegisteryClient(&Config{
				RetryMax:     1,
				RetryWaitMin: 1 * time.Millisecond,
				RetryWaitMax: 2 * time.Millisecond,
			})

			subscription := &model.Subscription{
				KeyID:            "test-key",
				SigningPublicKey: "test-signing-key",
				EncrPublicKey:    "test-encryption-key",
				ValidFrom:        time.Now(),
				ValidUntil:       time.Now().Add(24 * time.Hour),
				Status:           "SUBSCRIBED",
			}

			if tt.name == "JSON Marshalling Failure" {
				subscription = &model.Subscription{} // Example of an invalid object
			}

			err := client.Subscribe(context.Background(), subscription)
			require.Error(t, err) // Directly checking for an error since all cases should fail
		})
	}
}

// TestLookupSuccess tests successful lookup scenarios.
func TestLookupSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		response := []model.Subscription{
			{
				Subscriber: model.Subscriber{
					SubscriberID: "123",
				},
				KeyID:            "test-key",
				SigningPublicKey: "test-signing-key",
				EncrPublicKey:    "test-encryption-key",
				ValidFrom:        time.Now(),
				ValidUntil:       time.Now().Add(24 * time.Hour),
				Status:           "SUBSCRIBED",
			},
		}
		bodyBytes, _ := json.Marshal(response)
		if _, err := w.Write(bodyBytes); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		RegisteryURL: server.URL,
		RetryMax:     1,
		RetryWaitMin: 1 * time.Millisecond,
		RetryWaitMax: 2 * time.Millisecond,
	}
	rClient := NewRegisteryClient(config)
	ctx := context.Background()
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

	result, err := rClient.Lookup(ctx, subscription)
	require.NoError(t, err)
	require.NotEmpty(t, result)
	require.Equal(t, subscription.Subscriber.SubscriberID, result[0].Subscriber.SubscriberID)
}

// TestLookupFailure tests failure scenarios for the Lookup function.
func TestLookupFailure(t *testing.T) {
	tests := []struct {
		name         string
		responseBody interface{}
		responseCode int
		setupMock    func(*httptest.Server)
	}{
		{
			name:         "Lookup failure - non 200 status",
			responseBody: "Internal Server Error",
			responseCode: http.StatusInternalServerError,
		},
		{
			name:         "Invalid JSON response",
			responseBody: "Invalid JSON",
			responseCode: http.StatusOK,
		},
		{
			name: "Server timeout",
			setupMock: func(server *httptest.Server) {
				server.Config.WriteTimeout = 1 * time.Millisecond // Force timeout
			},
		},
		{
			name:         "Empty response body",
			responseBody: "",
			responseCode: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.responseCode != 0 { // Prevent WriteHeader(0) error
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

			if tc.setupMock != nil {
				tc.setupMock(server)
			}

			config := &Config{
				RegisteryURL: server.URL,
				RetryMax:     0,
				RetryWaitMin: 1 * time.Millisecond,
				RetryWaitMax: 2 * time.Millisecond,
			}
			rClient := NewRegisteryClient(config)
			ctx := context.Background()
			subscription := &model.Subscription{
				Subscriber:       model.Subscriber{},
				KeyID:            "test-key",
				SigningPublicKey: "test-signing-key",
				EncrPublicKey:    "test-encryption-key",
				ValidFrom:        time.Now(),
				ValidUntil:       time.Now().Add(24 * time.Hour),
				Status:           "SUBSCRIBED",
			}

			result, err := rClient.Lookup(ctx, subscription)
			require.Error(t, err)
			require.Empty(t, result)
		})
	}
}

// Mock RegistryClient for testing
type mockHTTPClient struct {
	response *http.Response
	err      error
}

func (m *mockHTTPClient) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}
func TestCreateRequestSuccess(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		endpoint      string
		body          []byte
		mockResponse  *http.Response
		mockError     error
		expectedError string
	}{
		{
			name:     "Successful request",
			method:   "POST",
			endpoint: "test-endpoint",
			body:     []byte(`{"key": "value"}`),
			mockResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
			},
			mockError:     nil,
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockHTTPClient{
				response: tt.mockResponse,
				err:      tt.mockError,
			}

			retryableClient := retryablehttp.NewClient()
			retryableClient.RetryMax = 3
			retryableClient.RetryWaitMin = 100 * time.Millisecond
			retryableClient.RetryWaitMax = 500 * time.Millisecond
			retryableClient.HTTPClient = &http.Client{Transport: mockClient}

			cfg := &Config{RegisteryURL: "http://mock.registry"}

			c := &registryClient{
				config: cfg,
				client: retryableClient,
			}

			resp, err := c.CreateRequest(context.Background(), tt.method, tt.endpoint, tt.body)

			if tt.expectedError != "" {
				assert.Nil(t, resp)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NotNil(t, resp)
				assert.NoError(t, err)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
			}
		})

	}
}

func TestCreateRequestFailure(t *testing.T) {
	tests := []struct {
		name      string
		client    *retryablehttp.Client
		config    *Config
		method    string
		endpoint  string
		body      []byte
		expectErr string
	}{
		{
			name:      "Nil client",
			client:    nil,
			config:    &Config{RegisteryURL: "http://mock.registry"},
			method:    "POST",
			endpoint:  "test-endpoint",
			body:      []byte(`{}`),
			expectErr: "client or config is not initialized",
		},
		{
			name:      "Nil config",
			client:    retryablehttp.NewClient(),
			config:    nil,
			method:    "POST",
			endpoint:  "test-endpoint",
			body:      []byte(`{}`),
			expectErr: "client or config is not initialized",
		},
		{
			name:      "Invalid HTTP method causes request creation failure",
			client:    retryablehttp.NewClient(),
			config:    &Config{RegisteryURL: "http://mock.registry"},
			method:    "INVALID METHOD", // spaces are invalid in HTTP methods
			endpoint:  "test-endpoint",
			body:      []byte(`{}`),
			expectErr: "failed to create request",
		},
		{
			name: "Request send failure",
			client: func() *retryablehttp.Client {
				c := retryablehttp.NewClient()
				c.HTTPClient = &http.Client{
					Transport: &mockHTTPClient{
						response: nil,
						err:      errors.New("mocked send failure"),
					},
				}
				c.RetryMax = 1
				c.RetryWaitMin = 10 * time.Millisecond
				c.RetryWaitMax = 50 * time.Millisecond
				c.Logger = nil
				return c
			}(),
			config:    &Config{RegisteryURL: "http://mock.registry"},
			method:    "POST",
			endpoint:  "test-endpoint",
			body:      []byte(`{}`),
			expectErr: "failed to send request with retry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &registryClient{
				client: tt.client,
				config: tt.config,
			}

			resp, err := client.CreateRequest(context.Background(), tt.method, tt.endpoint, tt.body)
			assert.Nil(t, resp)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectErr)
		})
	}
}
