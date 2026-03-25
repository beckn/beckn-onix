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
				Role:     "bap",
				ParentID: "bap:bap-123",
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
				Role:     "bpp",
				ParentID: "bap:bap-123",
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

// TestSnakeToCamel tests the snakeToCamel conversion helper.
func TestSnakeToCamel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"transaction_id", "transactionId"},
		{"message_id", "messageId"},
		{"bap_id", "bapId"},
		{"bpp_id", "bppId"},
		{"bap_uri", "bapUri"},
		{"bpp_uri", "bppUri"},
		{"domain", "domain"},   // no underscore — unchanged
		{"version", "version"}, // no underscore — unchanged
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := snakeToCamel(tt.input)
			if got != tt.want {
				t.Errorf("snakeToCamel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestCamelCaseSubscriberID tests that bapId / bppId are resolved when the payload
// uses camelCase context attribute names (new beckn spec).
func TestCamelCaseSubscriberID(t *testing.T) {
	tests := []struct {
		name        string
		role        string
		contextBody map[string]interface{}
		wantSubID   string
		wantCaller  string
	}{
		{
			name: "BAP role — camelCase bapId resolved as subscriber",
			role: "bap",
			contextBody: map[string]interface{}{
				"bapId": "bap.example.com",
				"bppId": "bpp.example.com",
			},
			wantSubID:  "bap.example.com",
			wantCaller: "bpp.example.com",
		},
		{
			name: "BPP role — camelCase bppId resolved as subscriber",
			role: "bpp",
			contextBody: map[string]interface{}{
				"bapId": "bap.example.com",
				"bppId": "bpp.example.com",
			},
			wantSubID:  "bpp.example.com",
			wantCaller: "bap.example.com",
		},
		{
			name: "snake_case still takes precedence over camelCase",
			role: "bap",
			contextBody: map[string]interface{}{
				"bap_id": "bap-snake.example.com",
				"bapId":  "bap-camel.example.com",
				"bpp_id": "bpp-snake.example.com",
				"bppId":  "bpp-camel.example.com",
			},
			wantSubID:  "bap-snake.example.com",
			wantCaller: "bpp-snake.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Role: tt.role}
			middleware, err := NewPreProcessor(cfg)
			if err != nil {
				t.Fatalf("NewPreProcessor() error = %v", err)
			}

			body, _ := json.Marshal(map[string]interface{}{"context": tt.contextBody})
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))

			var gotSubID, gotCaller interface{}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotSubID = r.Context().Value(model.ContextKeySubscriberID)
				gotCaller = r.Context().Value(model.ContextKeyRemoteID)
				w.WriteHeader(http.StatusOK)
			})

			middleware(handler).ServeHTTP(httptest.NewRecorder(), req)

			if gotSubID != tt.wantSubID {
				t.Errorf("subscriber ID: got %v, want %v", gotSubID, tt.wantSubID)
			}
			if gotCaller != tt.wantCaller {
				t.Errorf("caller ID: got %v, want %v", gotCaller, tt.wantCaller)
			}
		})
	}
}

// TestCamelCaseContextKeys tests that generic context keys (e.g. transaction_id)
// are resolved from their camelCase equivalents (transactionId) when the
// snake_case key is absent from the payload.
func TestCamelCaseContextKeys(t *testing.T) {
	cfg := &Config{
		Role:        "bap",
		ContextKeys: []string{"transaction_id", "message_id"},
	}
	middleware, err := NewPreProcessor(cfg)
	if err != nil {
		t.Fatalf("NewPreProcessor() error = %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"context": map[string]interface{}{
			"bapId":         "bap.example.com",
			"transactionId": "txn-abc",
			"messageId":     "msg-xyz",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))

	var gotTxnID, gotMsgID interface{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTxnID = r.Context().Value(model.ContextKeyTxnID)
		gotMsgID = r.Context().Value(model.ContextKeyMsgID)
		w.WriteHeader(http.StatusOK)
	})

	middleware(handler).ServeHTTP(httptest.NewRecorder(), req)

	if gotTxnID != "txn-abc" {
		t.Errorf("transaction_id: got %v, want txn-abc", gotTxnID)
	}
	if gotMsgID != "msg-xyz" {
		t.Errorf("message_id: got %v, want msg-xyz", gotMsgID)
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
