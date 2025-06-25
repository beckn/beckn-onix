package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthHandler tests the successful GET request to the /health endpoint.
func TestHealthHandler(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	HealthHandler(rr, req)

	expContentType := "application/json"
	expStatus := "ok"
	expService := "beckn-adapter"

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("HealthHandler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}
	if contentType := rr.Header().Get("Content-Type"); contentType != expContentType {
		t.Errorf("HealthHandler returned wrong Content-Type: got %v want %v",
			contentType, expContentType)
	}

	var response healthCheckResponse
	err = json.NewDecoder(rr.Body).Decode(&response)
	if err != nil {
		t.Fatalf("Failed to decode response body: %v", err)
	}

	if response.Status != expStatus {
		t.Errorf("HealthHandler returned wrong status in JSON: got %v want %v",
			response.Status, expStatus)
	}
	if response.Service != expService {
		t.Errorf("HealthHandler returned wrong service in JSON: got %v want %v",
			response.Service, expService)
	}
}

// mockResponseWriter is a custom http.ResponseWriter that can simulate an error on Write.
type mockResponseWriter struct {
	httptest.ResponseRecorder
	writeFail bool
}

func (m *mockResponseWriter) Write(p []byte) (n int, err error) {
	if m.writeFail {
		m.writeFail = false
		return 0, fmt.Errorf("simulated write error")
	}
	return m.ResponseRecorder.Write(p)
}

// TestHealthHandlerErrors tests error scenarios for the HealthHandler.
func TestHealthHandlerErrors(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		recorder  *mockResponseWriter
		expStatus int
		expBody   string
	}{
		{
			name:   "Method Not Allowed",
			method: http.MethodPost,
			recorder: &mockResponseWriter{
				ResponseRecorder: *httptest.NewRecorder(),
			},
			expStatus: http.StatusMethodNotAllowed,
			expBody:   "Method not allowed\n",
		},
		{
			name:   "JSON Encoding Error",
			method: http.MethodGet,
			recorder: &mockResponseWriter{
				ResponseRecorder: *httptest.NewRecorder(),
				writeFail:        true,
			},
			expStatus: http.StatusInternalServerError,
			expBody:   "Error encoding response\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, "/health", nil)
			if err != nil {
				t.Fatalf("Failed to create request for %s: %v", tt.name, err)
			}

			HealthHandler(tt.recorder, req)

			if status := tt.recorder.Code; status != tt.expStatus {
				t.Errorf("handler returned wrong status code: got %v want %v", status, tt.expStatus)
			}

			if body := tt.recorder.Body.String(); body != tt.expBody {
				t.Errorf("handler returned unexpected body: got %q want %q", body, tt.expBody)
			}
		})
	}
}