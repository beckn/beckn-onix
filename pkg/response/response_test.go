package response

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beckn/beckn-onix/pkg/model"
)

func TestSendAck(t *testing.T) {
	_, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err) // For tests
	}
	rr := httptest.NewRecorder()

	SendAck(rr)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status code %d, got %d", http.StatusOK, rr.Code)
	}

	expected := `{"message":{"ack":{"status":"ACK"}}}`
	if rr.Body.String() != expected {
		t.Errorf("expected body %s, got %s", expected, rr.Body.String())
	}
}

func TestNack(t *testing.T) {
	tests := []struct {
		name     string
		err      *model.Error
		status   int
		expected string
	}{
		{
			name: "Schema Validation Error",
			err: &model.Error{
				Code:    "BAD_REQUEST",
				Paths:   "/test/path",
				Message: "Invalid schema",
			},
			status:   http.StatusBadRequest,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"BAD_REQUEST","paths":"/test/path","message":"Invalid schema"}}}`,
		},
		{
			name: "Internal Server Error",
			err: &model.Error{
				Code:    "INTERNAL_SERVER_ERROR",
				Message: "Something went wrong",
			},
			status:   http.StatusInternalServerError,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"INTERNAL_SERVER_ERROR","message":"Something went wrong"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err) // For tests
			}
			rr := httptest.NewRecorder()

			nack(rr, tt.err, tt.status)

			if rr.Code != tt.status {
				t.Errorf("expected status code %d, got %d", tt.status, rr.Code)
			}

			body := rr.Body.String()
			if body != tt.expected {
				t.Errorf("expected body %s, got %s", tt.expected, body)
			}
		})
	}
}

func TestSendNack(t *testing.T) {
	ctx := context.WithValue(context.Background(), model.MsgIDKey, "123456")

	tests := []struct {
		name     string
		err      error
		expected string
		status   int
	}{
		{
			name: "SchemaValidationErr",
			err: &model.SchemaValidationErr{
				Errors: []model.Error{
					{Paths: "/path1", Message: "Error 1"},
					{Paths: "/path2", Message: "Error 2"},
				},
			},
			status:   http.StatusBadRequest,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Bad Request","paths":"/path1;/path2","message":"Error 1; Error 2"}}}`,
		},
		{
			name:     "SignValidationErr",
			err:      model.NewSignValidationErr(errors.New("signature invalid")),
			status:   http.StatusUnauthorized,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Unauthorized","message":"Signature Validation Error: signature invalid"}}}`,
		},
		{
			name:     "BadReqErr",
			err:      model.NewBadReqErr(errors.New("bad request error")),
			status:   http.StatusBadRequest,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Bad Request","message":"BAD Request: bad request error"}}}`,
		},
		{
			name:     "NotFoundErr",
			err:      model.NewNotFoundErr(errors.New("endpoint not found")),
			status:   http.StatusNotFound,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Not Found","message":"Endpoint not found: endpoint not found"}}}`,
		},
		{
			name:     "InternalServerError",
			err:      errors.New("unexpected error"),
			status:   http.StatusInternalServerError,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Internal Server Error","message":"Internal server error, MessageID: 123456"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err) // For tests
			}
			rr := httptest.NewRecorder()

			SendNack(ctx, rr, tt.err)

			if rr.Code != tt.status {
				t.Errorf("expected status code %d, got %d", tt.status, rr.Code)
			}

			var actual map[string]interface{}
			err = json.Unmarshal(rr.Body.Bytes(), &actual)
			if err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			var expected map[string]interface{}
			err = json.Unmarshal([]byte(tt.expected), &expected)
			if err != nil {
				t.Fatalf("failed to unmarshal expected response: %v", err)
			}

			if !compareJSON(expected, actual) {
				t.Errorf("expected body %s, got %s", tt.expected, rr.Body.String())
			}
		})
	}
}

func compareJSON(expected, actual map[string]interface{}) bool {
	expectedBytes, _ := json.Marshal(expected)
	actualBytes, _ := json.Marshal(actual)
	return bytes.Equal(expectedBytes, actualBytes)
}
