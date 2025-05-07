package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-retryablehttp"
)

func TestRegistryLookupSuccess(t *testing.T) {
	// Define the input subscription and expected result
	subscription := &model.Subscription{KeyID: "1"}
	mockResponse := `[{"subscriber_id": "", "url": "", "type": "", "domain": "", "key_id": "1", "signing_public_key": "", "encr_public_key": "", "valid_from": "0001-01-01T00:00:00Z", "valid_until": "0001-01-01T00:00:00Z", "status": "", "created": "0001-01-01T00:00:00Z", "updated": "0001-01-01T00:00:00Z", "nonce": ""}]`
	expectedResult := []model.Subscription{{KeyID: "1"}}

	// Create a mock server to simulate the /lookUp endpoint
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer mockServer.Close()

	// Create a RegistryLookup instance with the mock server URL
	config := &Config{LookupURL: mockServer.URL}
	lookup, _, err := New(context.Background(), config)
	if err != nil {
		t.Fatalf("Failed to create RegistryLookup: %v", err)
	}

	// Call the Lookup method
	results, err := lookup.Lookup(context.Background(), subscription)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Check if the results match the expected result using cmp.Diff
	if diff := cmp.Diff(expectedResult, results); diff != "" {
		t.Errorf("Mismatch (-expected +got):\n%s", diff)
	}
}

// Failure test cases for the RegistryLookup Lookup method
func TestRegistryLookupFailure(t *testing.T) {
	subscription := &model.Subscription{
		Subscriber:       model.Subscriber{},
		KeyID:            "test-key",
		SigningPublicKey: "test-signing-key",
		EncrPublicKey:    "test-encryption-key",
		ValidFrom:        time.Now(),
		ValidUntil:       time.Now().Add(24 * time.Hour),
		Status:           "SUBSCRIBED",
	}

	tests := []struct {
		name           string
		subscription   *model.Subscription
		mockResponse   []byte
		mockStatusCode int
		expectedError  string
	}{
		{
			name:           "Failed to send request with retry",
			subscription:   subscription,
			mockResponse:   []byte{},
			mockStatusCode: http.StatusInternalServerError,
			expectedError:  "failed to send request with retry",
		},
		{
			name:           "Failed to unmarshal response body",
			subscription:   subscription,
			mockResponse:   []byte(`failed to unmarshal response body`),
			mockStatusCode: http.StatusOK,
			expectedError:  "failed to unmarshal response body: invalid character 'i' in literal false (expecting 'l')",
		},
		{
			name:           "Lookup request failed with status",
			subscription:   subscription,
			mockResponse:   []byte(`[]`),
			mockStatusCode: http.StatusBadRequest,
			expectedError:  "lookup request failed with status: 400 Bad Request",
		},
	}

	// Loop over each test case
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the mock server
			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.mockStatusCode)
				_, _ = w.Write([]byte(tt.mockResponse))
			}))
			defer mockServer.Close()

			// Create a RegistryLookup instance with the mock server URL
			config := &Config{LookupURL: mockServer.URL}
			lookup := &registryLookup{
				client: retryablehttp.NewClient(),
				config: config,
			}

			// Call the Lookup method
			_, err := lookup.Lookup(context.Background(), tt.subscription)
			if err != nil {
				// Compare the entire error message
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Fatalf("Expected error to contain '%s', got '%s'", tt.expectedError, err.Error())
				}
				return // Exit the test if the error matches
			}

			// If no error occurred, fail the test
			t.Fatal("Expected error but got none")
		})
	}
}

func TestValidateConfigSuccess(t *testing.T) {
	config := &Config{
		LookupURL:    "http://example.com/lookup",
		RetryWaitMin: 1,
		RetryWaitMax: 5,
		RetryMax:     3,
	}

	err := validateConfig(config)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func TestValidateConfigErrors(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		expectedErr string
	}{
		{
			name:        "Empty LookupURL",
			config:      &Config{LookupURL: ""},
			expectedErr: "RegistryURL cannot be empty",
		},
		{
			name:        "Negative RetryWaitMin",
			config:      &Config{LookupURL: "http://example.com/lookup", RetryWaitMin: -1},
			expectedErr: "RetryWaitMin must be non-negative",
		},
		{
			name:        "Negative RetryWaitMax",
			config:      &Config{LookupURL: "http://example.com/lookup", RetryWaitMin: 0, RetryWaitMax: -1},
			expectedErr: "RetryWaitMax must be non-negative",
		},
		{
			name:        "RetryWaitMin > RetryWaitMax",
			config:      &Config{LookupURL: "http://example.com/lookup", RetryWaitMin: 5, RetryWaitMax: 3},
			expectedErr: "RetryWaitMin cannot be greater than RetryWaitMax",
		},
		{
			name:        "Negative RetryMax",
			config:      &Config{LookupURL: "http://example.com/lookup", RetryWaitMin: 1, RetryWaitMax: 2, RetryMax: -1},
			expectedErr: "RetryMax must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.config)
			if err == nil || err.Error() != tt.expectedErr {
				t.Fatalf("Expected error: %s, got: %v", tt.expectedErr, err)
			}
		})
	}
}
