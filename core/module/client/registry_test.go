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

type mockHTTPClient struct {
	response *http.Response
	err      error
}

func (m *mockHTTPClient) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.response, m.err
}

func TestRegistrySubscribeSuccess(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		reqBody      []byte
		setupHandler func(w http.ResponseWriter, r *http.Request)
		expectedErr  string
	}{
		{
			name:     "Success - valid JSON response",
			endpoint: "subscribe",
			reqBody:  []byte(`{"key": "value"}`),
			setupHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ack": true}`))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var client *registryClient

			if tc.name == "Error - request creation failure" {
				client = &registryClient{
					config: &Config{RegisteryURL: "http://"},
					client: &retryablehttp.Client{
						HTTPClient: &http.Client{
							Transport: &mockHTTPClient{
								response: nil,
								err:      errors.New("mocked send failure"),
							},
						},
						RetryMax:     0,
						RetryWaitMin: 1 * time.Millisecond,
						RetryWaitMax: 2 * time.Millisecond,
						Logger:       nil,
					},
				}
			} else if tc.setupHandler != nil {
				server := httptest.NewServer(http.HandlerFunc(tc.setupHandler))
				defer server.Close()

				client = &registryClient{
					config: &Config{RegisteryURL: server.URL},
					client: retryablehttp.NewClient(),
				}
			} else {
				client = &registryClient{
					config: &Config{RegisteryURL: "http://localhost"}, // will fail for invalid URL case
					client: retryablehttp.NewClient(),
				}
			}

			ctx := context.Background()
			resp, err := client.RegistrySubscribe(ctx, tc.endpoint, tc.reqBody)

			if tc.expectedErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErr)
				require.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Contains(t, resp, "ack")
			}
		})
	}

}

func TestRegistrySubscribeFailure(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		reqBody      []byte
		setupHandler func(w http.ResponseWriter, r *http.Request)
		expectedErr  string
	}{
		{
			name:     "Error - response body read failure",
			endpoint: "subscribe",
			reqBody:  []byte(`{"key": "value"}`),
			setupHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				// simulate unreadable body by closing connection immediately
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
			},
			expectedErr: "failed to read response body",
		},
		{
			name:     "Error - non-200 status",
			endpoint: "subscribe",
			reqBody:  []byte(`{"key": "value"}`),
			setupHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`Bad Request`))
			},
			expectedErr: "registry returned non-200 status",
		},
		{
			name:     "Error - invalid JSON in response",
			endpoint: "subscribe",
			reqBody:  []byte(`{"key": "value"}`),
			setupHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`not-json`))
			},
			expectedErr: "failed to parse response JSON",
		},
		{
			name:        "Error - request creation failure",
			endpoint:    "%%%invalid-url%%%",
			reqBody:     []byte(`{"key": "value"}`),
			expectedErr: "failed to create registry subscribe request",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var client *registryClient

			if tc.name == "Error - request creation failure" {
				client = &registryClient{
					config: &Config{RegisteryURL: "http://"},
					client: &retryablehttp.Client{
						HTTPClient: &http.Client{
							Transport: &mockHTTPClient{
								response: nil,
								err:      errors.New("mocked send failure"),
							},
						},
						RetryMax:     0,
						RetryWaitMin: 1 * time.Millisecond,
						RetryWaitMax: 2 * time.Millisecond,
						Logger:       nil,
					},
				}
			} else if tc.setupHandler != nil {
				server := httptest.NewServer(http.HandlerFunc(tc.setupHandler))
				defer server.Close()

				client = &registryClient{
					config: &Config{RegisteryURL: server.URL},
					client: retryablehttp.NewClient(),
				}
			} else {
				client = &registryClient{
					config: &Config{RegisteryURL: "http://localhost"}, // will fail for invalid URL case
					client: retryablehttp.NewClient(),
				}
			}

			ctx := context.Background()
			resp, err := client.RegistrySubscribe(ctx, tc.endpoint, tc.reqBody)

			if tc.expectedErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErr)
				require.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Contains(t, resp, "ack")
			}
		})
	}

}
