package reqpreprocessor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewUUIDSetterSuccessCases(t *testing.T) {
	tests := []struct {
		name         string
		config       *Config
		requestBody  map[string]any
		expectedKeys []string
		role         string
	}{
		{
			name: "Valid keys, update missing keys with bap role",
			config: &Config{
				ContextKeys: []string{"transaction_id", "message_id"},
				Role:        "bap",
			},
			requestBody: map[string]any{
				"context": map[string]any{
					"transaction_id": "",
					"message_id":     nil,
					"bap_id":         "bap-123",
				},
			},
			expectedKeys: []string{"transaction_id", "message_id", "bap_id"},
			role:         "bap",
		},
		{
			name: "Valid keys, do not update existing keys with bpp role",
			config: &Config{
				ContextKeys: []string{"transaction_id", "message_id"},
				Role:        "bpp",
			},
			requestBody: map[string]any{
				"context": map[string]any{
					"transaction_id": "existing-transaction",
					"message_id":     "existing-message",
					"bpp_id":         "bpp-456",
				},
			},
			expectedKeys: []string{"transaction_id", "message_id", "bpp_id"},
			role:         "bpp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware, err := NewPreProcessor(tt.config)
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

				subID, ok := ctx.Value(subscriberIDKey).(string)
				if !ok {
					http.Error(w, "Subscriber ID not found", http.StatusInternalServerError)
					return
				}

				response := map[string]any{"subscriber_id": subID}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					return
				}
			})

			middleware(dummyHandler).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Expected status code 200, but got %d", rec.Code)
				return
			}

			var responseBody map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &responseBody); err != nil {
				t.Fatal("Failed to unmarshal response body:", err)
			}

			expectedSubIDKey := "bap_id"
			if tt.role == "bpp" {
				expectedSubIDKey = "bpp_id"
			}

			subID, ok := responseBody["subscriber_id"].(string)
			if !ok {
				t.Error("subscriber_id not found in response")
				return
			}

			expectedSubID := tt.requestBody["context"].(map[string]any)[expectedSubIDKey]
			if subID != expectedSubID {
				t.Errorf("Expected subscriber_id %v, but got %v", expectedSubID, subID)
			}
		})
	}
}

func TestNewUUIDSetterErrorCases(t *testing.T) {
	tests := []struct {
		name         string
		config       *Config
		requestBody  map[string]any
		expectedCode int
	}{
		{
			name: "Missing context key",
			config: &Config{
				ContextKeys: []string{"transaction_id"},
			},
			requestBody: map[string]any{
				"otherKey": "value",
			},
			expectedCode: http.StatusBadRequest,
		},
		{
			name: "Invalid context type",
			config: &Config{
				ContextKeys: []string{"transaction_id"},
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
			middleware, err := NewPreProcessor(tt.config)
			if tt.config == nil {
				if err == nil {
					t.Error("Expected an error for nil config, but got none")
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
				w.WriteHeader(http.StatusOK)
			})

			middleware(dummyHandler).ServeHTTP(rec, req)

			if rec.Code != tt.expectedCode {
				t.Errorf("Expected status code %d, but got %d", tt.expectedCode, rec.Code)
			}
		})
	}
}
