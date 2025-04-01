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

type errorResponseWriter struct{}

// TODO: Optimize the cases by removing these
func (e *errorResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("write error")
}
func (e *errorResponseWriter) WriteHeader(statusCode int) {}

func (e *errorResponseWriter) Header() http.Header {
	return http.Header{}
}

func TestSendAck(t *testing.T) {
	_, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err) // For tests
	}
	rr := httptest.NewRecorder()

	SendAck(rr)

	if rr.Code != http.StatusOK {
		t.Errorf("wanted status code %d, got %d", http.StatusOK, rr.Code)
	}

	expected := `{"message":{"ack":{"status":"ACK"}}}`
	if rr.Body.String() != expected {
		t.Errorf("err.Error() = %s, want %s",
			rr.Body.String(), expected)

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
				t.Errorf("wanted status code %d, got %d", tt.status, rr.Code)
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
				t.Errorf("err.Error() = %s, want %s",
					actual, expected)
			}

		})
	}
}

func compareJSON(expected, actual map[string]interface{}) bool {
	expectedBytes, _ := json.Marshal(expected)
	actualBytes, _ := json.Marshal(actual)
	return bytes.Equal(expectedBytes, actualBytes)
}

func TestSendAck_WriteError(t *testing.T) {
	w := &errorResponseWriter{}
	SendAck(w)
}

// Mock struct to force JSON marshalling error
type badMessage struct{}

func (b *badMessage) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal error")
}

func TestNack_1(t *testing.T) {
	tests := []struct {
		name        string
		err         *model.Error
		status      int
		expected    string
		useBadJSON  bool
		useBadWrite bool
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
		{
			name:       "JSON Marshal Error",
			err:        nil, // This will be overridden to cause marshaling error
			status:     http.StatusInternalServerError,
			expected:   `Internal server error, MessageID: 12345`,
			useBadJSON: true,
		},
		{
			name: "Write Error",
			err: &model.Error{
				Code:    "WRITE_ERROR",
				Message: "Failed to write response",
			},
			status:      http.StatusInternalServerError,
			expected:    `Internal server error, MessageID: 12345`,
			useBadWrite: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(req.Context(), model.MsgIDKey, "12345")

			var w http.ResponseWriter
			if tt.useBadWrite {
				w = &errorResponseWriter{} // Simulate write error
			} else {
				w = httptest.NewRecorder()
			}

			// TODO: Fix this approach , should not be used like this.
			if tt.useBadJSON {
				data, _ := json.Marshal(&badMessage{})
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, err := w.Write(data)
				if err != nil {
					http.Error(w, "Failed to write response", http.StatusInternalServerError)
					return
				}
				return
			}

			nack(ctx, w, tt.err, tt.status)
			if !tt.useBadWrite {
				recorder, ok := w.(*httptest.ResponseRecorder)
				if !ok {
					t.Fatal("Failed to cast response recorder")
				}

				if recorder.Code != tt.status {
					t.Errorf("wanted status code %d, got %d", tt.status, recorder.Code)
				}

				body := recorder.Body.String()
				if body != tt.expected {
					t.Errorf("err.Error() = %s, want %s",
						body, tt.expected)
				}
			}
		})
	}
}
