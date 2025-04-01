package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TODO: Will Split this into success and fail (two test cases)
func TestProviderNew(t *testing.T) {
	testCases := []struct {
		name           string
		config         map[string]string
		expectedError  bool
		expectedStatus int
		prepareRequest func(req *http.Request)
	}{
		{
			name:           "No Config",
			config:         map[string]string{},
			expectedError:  true,
			expectedStatus: http.StatusOK,
			prepareRequest: func(req *http.Request) {
				// Add minimal required headers.
				req.Header.Set("context", "test-context")
				req.Header.Set("transaction_id", "test-transaction")
			},
		},
		{
			name: "With Check Keys",
			config: map[string]string{
				"contextKeys": "message_id,transaction_id",
			},
			expectedError:  false,
			expectedStatus: http.StatusOK,
			prepareRequest: func(req *http.Request) {
				// Add headers matching the check keys.
				req.Header.Set("context", "test-context")
				req.Header.Set("transaction_id", "test-transaction")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			requestBody := `{
				"context": {
					"transaction_id": "abc"
				}
			}`

			p := provider{}
			middleware, err := p.New(context.Background(), tc.config)
			if tc.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, middleware)

			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest("POST", "/", strings.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")
			if tc.prepareRequest != nil {
				tc.prepareRequest(req)
			}

			w := httptest.NewRecorder()
			middlewaredHandler := middleware(testHandler)
			middlewaredHandler.ServeHTTP(w, req)
			assert.Equal(t, tc.expectedStatus, w.Code, "Unexpected response status")
			responseBody := w.Body.String()
			t.Logf("Response Body: %s", responseBody)

		})
	}
}
