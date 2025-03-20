package reqpreprocessor

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewUUIDSetter(t *testing.T) {
	tests := []struct {
		name         string
		config       *Config
		requestBody  map[string]any
		expectedCode int
		expectedKeys []string
	}{
		{
			name: "Valid keys, update missing keys",
			config: &Config{
				checkKeys: []string{"transaction_id", "message_id"},
			},
			requestBody: map[string]any{
				"context": map[string]any{
					"transaction_id": "",
					"message_id":     nil,
				},
			},
			expectedCode: http.StatusOK,
			expectedKeys: []string{"transaction_id", "message_id"},
		},
		{
			name: "Valid keys, do not update existing keys",
			config: &Config{
				checkKeys: []string{"transaction_id", "message_id"},
			},
			requestBody: map[string]any{
				"context": map[string]any{
					"transaction_id": "existing-transaction",
					"message_id":     "existing-message",
				},
			},
			expectedCode: http.StatusOK,
			expectedKeys: []string{"transaction_id", "message_id"},
		},
		{
			name: "Missing context key",
			config: &Config{
				checkKeys: []string{"transaction_id"},
			},
			requestBody: map[string]any{
				"otherKey": "value",
			},
			expectedCode: http.StatusBadRequest,
		},
		{
			name: "Invalid context type",
			config: &Config{
				checkKeys: []string{"transaction_id"},
			},
			requestBody: map[string]any{
				"context": "not-a-map",
			},
			expectedCode: http.StatusBadRequest,
		},
		{
			name: "Empty checkKeys in config",
			config: &Config{
				checkKeys: []string{},
			},
			requestBody: map[string]any{
				"context": map[string]any{
					"transaction_id": "",
				},
			},
			expectedCode: http.StatusInternalServerError,
		},
		{
			name:         "Nil config",
			config:       nil,
			requestBody:  map[string]any{},
			expectedCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware, err := NewUUIDSetter(tt.config)
			if tt.config == nil || len(tt.config.checkKeys) == 0 {
				if err == nil {
					t.Fatal("Expected an error, but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error while creating middleware: %v", err)
			}

			// Prepare request
			bodyBytes, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			// Define a dummy handler
			dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				if _, err := io.Copy(w, r.Body); err != nil {
					http.Error(w, "Failed to copy request body", http.StatusInternalServerError)
					return
				}
			})

			// Apply middleware
			middleware(dummyHandler).ServeHTTP(rec, req)

			// Check status code
			if rec.Code != tt.expectedCode {
				t.Errorf("Expected status code %d, but got %d", tt.expectedCode, rec.Code)
			}

			// If success, check updated keys
			if rec.Code == http.StatusOK {
				var responseBody map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &responseBody); err != nil {
					t.Fatal("Failed to unmarshal response body:", err)
				}

				// Validate updated keys
				contextData, ok := responseBody[contextKey].(map[string]any)
				if !ok {
					t.Fatalf("Expected context to be a map, got %T", responseBody[contextKey])
				}

				for _, key := range tt.expectedKeys {
					value, exists := contextData[key]
					if !exists || isEmpty(value) {
						t.Errorf("Expected key %s to be set, but it's missing or empty", key)
					}
				}
			}
		})
	}
}
