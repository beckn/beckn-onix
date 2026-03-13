package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderNewSuccess(t *testing.T) {
	p := provider{}
	mappingFile := "../testdata/mappings.yaml"
	middleware, err := p.New(context.Background(), map[string]string{"role": "bap", "mappingsFile": mappingFile})
	if err != nil {
		t.Fatalf("provider.New returned unexpected error: %v", err)
	}
	if middleware == nil {
		t.Fatalf("provider.New returned nil middleware")
	}

	payload := map[string]interface{}{
		"context": map[string]interface{}{
			"action":         "search",
			"domain":         "retail",
			"version":        "1.1.0",
			"bap_id":         "bap.example",
			"bap_uri":        "https://bap.example/api",
			"transaction_id": "txn-1",
			"message_id":     "msg-1",
			"timestamp":      "2023-01-01T10:00:00Z",
		},
		"message": map[string]interface{}{
			"intent": map[string]interface{}{
				"fulfillment": map[string]interface{}{
					"start": map[string]interface{}{
						"location": map[string]interface{}{"gps": "0,0"},
					},
					"end": map[string]interface{}{
						"location": map[string]interface{}{"gps": "1,1"},
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	middleware(next).ServeHTTP(rec, req)

	if !called {
		t.Fatalf("expected downstream handler to be invoked")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected response code: got %d want %d", rec.Code, http.StatusNoContent)
	}
}

func TestProviderNewMissingRole(t *testing.T) {
	p := provider{}
	if _, err := p.New(context.Background(), map[string]string{}); err == nil {
		t.Fatalf("expected error when role is missing")
	}
}

func TestProviderNewInvalidRole(t *testing.T) {
	p := provider{}
	_, err := p.New(context.Background(), map[string]string{"role": "invalid"})
	if err == nil {
		t.Fatalf("expected error for invalid role")
	}
}
