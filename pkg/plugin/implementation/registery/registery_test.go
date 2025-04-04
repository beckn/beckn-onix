package registery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/hashicorp/go-retryablehttp"
)

func TestRegistryLookupSuccess(t *testing.T) {
	tests := []struct {
		name           string
		subscription   *model.Subscription
		mockResponse   string
		expectedResult []model.Subscription
	}{
		{
			name:           "Success - Valid subscription",
			subscription:   &model.Subscription{KeyID: "1"},
			mockResponse:   `[{"subscriber_id": "", "url": "", "type": "", "domain": "", "key_id": "1", "signing_public_key": "", "encr_public_key": "", "valid_from": "0001-01-01T00:00:00Z", "valid_until": "0001-01-01T00:00:00Z", "status": "", "created": "0001-01-01T00:00:00Z", "updated": "0001-01-01T00:00:00Z", "nonce": ""}]`,
			expectedResult: []model.Subscription{{KeyID: "1"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock server to simulate the /lookUp endpoint
			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.mockResponse))
			}))
			defer mockServer.Close()

			// Create a RegistryLookup instance with the mock server URL
			config := &Config{RegistryURL: mockServer.URL}
			lookup, _, err := New(context.Background(), config)
			if err != nil {
				t.Fatalf("Failed to create RegistryLookup: %v", err)
			}

			// Call the Lookup method
			results, err := lookup.Lookup(context.Background(), tt.subscription)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Check if the results match the expected result
			if !equalSubscriptions(results, tt.expectedResult) {
				t.Errorf("Expected %v, got %v", tt.expectedResult, results)
			}
		})
	}
}

// Helper function to compare two slices of Subscription
func equalSubscriptions(a, b []model.Subscription) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Helper function to create a pointer to a string
func stringPtr(s string) *string {
	return &s
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
		mockResponse   *string
		mockStatusCode int
		expectedError  string
	}{
		{
			name:           "Failed to send request with retry",
			subscription:   subscription,
			mockResponse:   stringPtr(`[]`),
			mockStatusCode: http.StatusInternalServerError,
			expectedError:  "failed to send request with retry",
		},
		{
			name:           "Failed to unmarshal response body",
			subscription:   subscription,
			mockResponse:   stringPtr(`failed to unmarshal response body`),
			mockStatusCode: http.StatusOK,
			expectedError:  "failed to unmarshal response body: invalid character 'i' in literal false (expecting 'l')",
		},
		{
			name:           "Lookup request failed with status",
			subscription:   subscription,
			mockResponse:   stringPtr(`[]`),
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
				_, _ = w.Write([]byte(*tt.mockResponse))
			}))
			defer mockServer.Close()

			// Create a RegistryLookup instance with the mock server URL
			config := &Config{RegistryURL: mockServer.URL}
			lookup := &RegistryLookup{
				Client: retryablehttp.NewClient(),
				Config: config,
			}

			// Call the Lookup method
			_, err := lookup.Lookup(context.Background(), tt.subscription)
			if err != nil {
				// Compare the entire error message
				if err.Error() != tt.expectedError {
					t.Fatalf("Expected error '%s', got '%s'", tt.expectedError, err.Error())
				}
				return // Exit the test if the error matches
			}

			// If no error occurred, fail the test
			t.Fatal("Expected error but got none")
		})
	}
}
