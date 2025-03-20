package reqpreprocessor

import (
	"bytes"
	"encoding/json"
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
		role         string
	}{
		{
			name: "Valid keys, update missing keys with bap role",
			config: &Config{
				checkKeys: []string{"transaction_id", "message_id"},
				Role:      "bap",
			},
			requestBody: map[string]any{
				"context": map[string]any{
					"transaction_id": "",
					"message_id":     nil,
					"bap_id":         "bap-123",
				},
			},
			expectedCode: http.StatusOK,
			expectedKeys: []string{"transaction_id", "message_id", "bap_id"},
			role:         "bap",
		},
		{
			name: "Valid keys, do not update existing keys with bpp role",
			config: &Config{
				checkKeys: []string{"transaction_id", "message_id"},
				Role:      "bpp",
			},
			requestBody: map[string]any{
				"context": map[string]any{
					"transaction_id": "existing-transaction",
					"message_id":     "existing-message",
					"bpp_id":         "bpp-456",
				},
			},
			expectedCode: http.StatusOK,
			expectedKeys: []string{"transaction_id", "message_id", "bpp_id"},
			role:         "bpp",
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
			bodyBytes, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				w.WriteHeader(http.StatusOK)
				if subID, ok := ctx.Value(subscriberIDKey).(string); ok {
					response := map[string]any{
						"subscriber_id": subID,
					}
					json.NewEncoder(w).Encode(response)
				} else {
					http.Error(w, "Subscriber ID not found", http.StatusInternalServerError)
					return
				}
			})
			middleware(dummyHandler).ServeHTTP(rec, req)
			if rec.Code != tt.expectedCode {
				t.Errorf("Expected status code %d, but got %d", tt.expectedCode, rec.Code)
			}
			if rec.Code == http.StatusOK {
				var responseBody map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &responseBody); err != nil {
					t.Fatal("Failed to unmarshal response body:", err)
				}
				expectedSubIDKey := "bap_id"
				if tt.role == "bpp" {
					expectedSubIDKey = "bpp_id"
				}

				if subID, ok := responseBody["subscriber_id"].(string); ok {
					expectedSubID := tt.requestBody["context"].(map[string]any)[expectedSubIDKey]
					if subID != expectedSubID {
						t.Errorf("Expected subscriber_id %v, but got %v", expectedSubID, subID)
					}
				} else {
					t.Error("subscriber_id not found in response")
				}
			}
		})
	}
}
