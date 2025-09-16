package reqpreprocessor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// ToDo Separate Middleware creation and execution.
func TestNewPreProcessorSuccessCases(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		requestBody map[string]any
		expectedID  string
	}{
		{
			name: "BAP role with valid context",
			config: &Config{
				Role: "bap",
			},
			requestBody: map[string]interface{}{
				"context": map[string]interface{}{
					"bap_id":     "bap-123",
					"message_id": "msg-123",
				},
				"message": map[string]interface{}{
					"key": "value",
				},
			},
			expectedID: "bap-123",
		},
		{
			name: "BPP role with valid context",
			config: &Config{
				Role: "bpp",
			},
			requestBody: map[string]interface{}{
				"context": map[string]interface{}{
					"bpp_id":     "bpp-456",
					"message_id": "msg-456",
				},
				"message": map[string]interface{}{
					"key": "value",
				},
			},
			expectedID: "bpp-456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware, err := NewPreProcessor(tt.config)
			if err != nil {
				t.Fatalf("NewPreProcessor() error = %v", err)
			}

			bodyBytes, err := json.Marshal(tt.requestBody)
			if err != nil {
				t.Fatalf("Failed to marshal request body: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()

			var gotSubID interface{}

			dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				gotSubID = ctx.Value(model.ContextKeySubscriberID)
				w.WriteHeader(http.StatusOK)

				// Verify subscriber ID
				subID := ctx.Value(model.ContextKeySubscriberID)
				if subID == nil {
					t.Errorf("Expected subscriber ID but got none %s", ctx)
					return
				}

				// Verify the correct ID was set based on role
				expectedKey := "bap_id"
				if tt.config.Role == "bpp" {
					expectedKey = "bpp_id"
				}
				expectedID := tt.requestBody["context"].(map[string]interface{})[expectedKey]
				if subID != expectedID {
					t.Errorf("Expected subscriber ID %v, got %v", expectedID, subID)
				}
			})

			middleware(dummyHandler).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Expected status code 200, but got %d", rec.Code)
				return
			}

			// Verify subscriber ID
			if gotSubID == nil {
				t.Error("Expected subscriber_id to be set in context but got nil")
				return
			}

			subID, ok := gotSubID.(string)
			if !ok {
				t.Errorf("Expected subscriber_id to be string, got %T", gotSubID)
				return
			}

			if subID != tt.expectedID {
				t.Errorf("Expected subscriber_id %q, got %q", tt.expectedID, subID)
			}
		})
	}
}

func TestNewPreProcessorErrorCases(t *testing.T) {
	tests := []struct {
		name         string
		config       *Config
		requestBody  interface{}
		expectedCode int
		expectErr    bool
		errMsg       string
	}{
		{
			name: "Missing context",
			config: &Config{
				Role: "bap",
			},
			requestBody: map[string]any{
				"otherKey": "value",
			},
			expectedCode: http.StatusBadRequest,
			expectErr:    false,
			errMsg:       "context field not found or invalid",
		},
		{
			name: "Invalid context type",
			config: &Config{
				Role: "bap",
			},
			requestBody: map[string]any{
				"context": "not-a-map",
			},
			expectedCode: http.StatusBadRequest,
			expectErr:    false,
			errMsg:       "context field not found or invalid",
		},
		{
			name:         "Nil config",
			config:       nil,
			requestBody:  map[string]any{},
			expectedCode: http.StatusInternalServerError,
			expectErr:    true,
			errMsg:       "config cannot be nil",
		},
		{
			name: "Invalid role",
			config: &Config{
				Role: "invalid-role",
			},
			requestBody: map[string]interface{}{
				"context": map[string]interface{}{
					"bap_id": "bap-123",
				},
			},
			expectedCode: http.StatusInternalServerError,
			expectErr:    true,
			errMsg:       "role must be either 'bap' or 'bpp'",
		},
		{
			name: "Missing subscriber ID",
			config: &Config{
				Role: "bap",
			},
			requestBody: map[string]interface{}{
				"context": map[string]interface{}{
					"message_id": "msg-123",
				},
			},
			expectedCode: http.StatusOK,
			expectErr:    false,
		},
		{
			name: "Invalid JSON body",
			config: &Config{
				Role: "bap",
			},
			requestBody:  "{invalid-json}",
			expectedCode: http.StatusBadRequest,
			expectErr:    false,
			errMsg:       "failed to decode request body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware, err := NewPreProcessor(tt.config)
			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected an error for NewPreProcessor(%s), but got none", tt.config)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Expected error to contain %q, got %v", tt.errMsg, err)
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

func TestNewPreProcessorAddsSubscriberIDToContext(t *testing.T) {
	cfg := &Config{Role: "bap"}
	middleware, err := NewPreProcessor(cfg)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	samplePayload := map[string]interface{}{
		"context": map[string]interface{}{
			"bap_id": "bap.example.com",
		},
	}
	bodyBytes, _ := json.Marshal(samplePayload)

	var receivedSubscriberID interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSubscriberID = r.Context().Value(model.ContextKeySubscriberID)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	middleware(handler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected status 200 OK, got %d", rr.Code)
	}
	if receivedSubscriberID != "bap.example.com" {
		t.Errorf("Expected subscriber ID 'bap.example.com', got %v", receivedSubscriberID)
	}
}
