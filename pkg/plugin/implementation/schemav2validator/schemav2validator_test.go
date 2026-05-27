package schemav2validator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

// testSpecBodyless mirrors the Beckn 2.0 /catalog/subscription path which
// carries GET and DELETE (both bodyless) alongside POST (body-bearing).
// It also includes /search (POST-only) for rejection-case coverage.
const testSpecBodyless = `openapi: 3.1.0
info:
  title: Test API (bodyless)
  version: 1.0.0
paths:
  /catalog/subscription:
    get:
      summary: Get subscriptions (no body — query params only)
      parameters:
        - in: query
          name: subscriptionId
          schema:
            type: string
      responses:
        "200":
          description: OK
    delete:
      summary: Deactivate subscription (no body — query param only)
      parameters:
        - in: query
          name: subscriptionId
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
    post:
      summary: Create subscription (body required)
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [context, message]
              properties:
                context:
                  type: object
                  properties:
                    action:
                      const: catalog/subscription
                message:
                  type: object
      responses:
        "200":
          description: OK
  /search:
    post:
      summary: Search (body required — no GET/DELETE defined)
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [context]
              properties:
                context:
                  type: object
                  properties:
                    action:
                      const: search
      responses:
        "200":
          description: OK
`

const testSpec = `openapi: 3.1.0
info:
  title: Test API
  version: 1.0.0
paths:
  /search:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [context, message]
              properties:
                context:
                  type: object
                  required: [action]
                  properties:
                    action:
                      const: search
                    domain:
                      type: string
                message:
                  type: object
  /select:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [context, message]
              properties:
                context:
                  allOf:
                    - type: object
                      properties:
                        action:
                          enum: [select]
                message:
                  type: object
                  required: [order]
                  properties:
                    order:
                      type: object
`

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{"nil config", nil, true},
		{"empty type", &Config{Type: "", Location: "http://example.com"}, true},
		{"empty location", &Config{Type: "url", Location: ""}, true},
		{"invalid type", &Config{Type: "invalid", Location: "http://example.com"}, true},
		{"invalid URL", &Config{Type: "url", Location: "http://invalid-domain-12345.com/spec.yaml"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := New(context.Background(), tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidate_ActionExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		payload string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid search action",
			payload: `{"context":{"action":"search","domain":"retail"},"message":{}}`,
			wantErr: false,
		},
		{
			name:    "valid select action with allOf",
			payload: `{"context":{"action":"select"},"message":{"order":{}}}`,
			wantErr: false,
		},
		{
			name:    "missing action",
			payload: `{"context":{},"message":{}}`,
			wantErr: true,
			errMsg:  "missing field Action",
		},
		{
			name:    "unsupported action",
			payload: `{"context":{"action":"unknown"},"message":{}}`,
			wantErr: true,
			errMsg:  "unsupported action: unknown",
		},
		{
			name:    "action as number",
			payload: `{"context":{"action":123},"message":{}}`,
			wantErr: true,
			errMsg:  "failed to parse JSON payload",
		},
		{
			name:    "invalid JSON",
			payload: `{invalid json}`,
			wantErr: true,
			errMsg:  "failed to parse JSON payload",
		},
		{
			name:    "missing required field",
			payload: `{"context":{"action":"search"}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(context.Background(), nil, []byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errMsg != "" && err != nil {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestValidate_NestedValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{
			name:    "select missing required order",
			payload: `{"context":{"action":"select"},"message":{}}`,
			wantErr: true,
		},
		{
			name:    "select with order",
			payload: `{"context":{"action":"select"},"message":{"order":{}}}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(context.Background(), nil, []byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidate_BodylessRequest covers GET/DELETE requests (Beckn 2.0
// catalog/subscription verbs) that carry no body. Validate() detects
// len(data)==0, looks up the path in the pre-built bodylessActions index,
// and either passes (known bodyless endpoint) or rejects (unknown/body-only).
func TestValidate_BodylessRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpecBodyless))
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	mustURL := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", raw, err)
		}
		return u
	}

	// NOTE: bodylessActions is keyed by path only — it does not distinguish HTTP
	// methods. GET, DELETE, and even an empty-body POST to the same path all resolve
	// to the same map key. Method-aware validation requires the Option C StepContext
	// refactor. The "known limitation" case below documents this current behaviour.
	tests := []struct {
		name    string
		path    string
		nilURL  bool // pass nil *url.URL instead of parsing path
		wantErr bool
		errMsg  string
	}{
		{
			// Multi-segment path with GET — indexed in bodylessActions, must pass.
			name:    "GET /catalog/subscription — empty body passes",
			path:    "/catalog/subscription",
			wantErr: false,
		},
		{
			// Same path, DELETE is also indexed as bodyless (path-only key, no
			// method distinction at this layer).
			name:    "DELETE /catalog/subscription — empty body passes",
			path:    "/catalog/subscription",
			wantErr: false,
		},
		{
			// Known limitation (Option A): bodylessActions is path-only. An empty-body
			// POST to a bodyless-indexed path passes through without body validation.
			// This will become wantErr:true once the Option C StepContext refactor lands.
			name:    "empty-body POST to bodyless-indexed path passes (known limitation)",
			path:    "/catalog/subscription",
			wantErr: false,
		},
		{
			// Query string must not pollute the path key — reqURL.Path strips RawQuery,
			// so /catalog/subscription?subscriptionId=abc resolves to the same map key
			// as /catalog/subscription and must pass.
			name:    "GET with query string — query params do not affect path match",
			path:    "/catalog/subscription?subscriptionId=abc-123",
			wantErr: false,
		},
		{
			// nil reqURL with empty body must return an error, not panic.
			// Guards the reqURL == nil check added in the self-review.
			name:    "nil URL with empty body returns error",
			nilURL:  true,
			wantErr: true,
			errMsg:  "request URL is required for bodyless validation",
		},
		{
			// /search only has a POST with requestBody — not in bodylessActions.
			name:    "POST-only endpoint /search with empty body is rejected",
			path:    "/search",
			wantErr: true,
			errMsg:  "unsupported bodyless request for endpoint: search",
		},
		{
			// Path absent from spec entirely.
			name:    "unknown endpoint /nonsense with empty body is rejected",
			path:    "/nonsense",
			wantErr: true,
			errMsg:  "unsupported bodyless request for endpoint: nonsense",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqURL *url.URL
			if !tt.nilURL {
				reqURL = mustURL(tt.path)
			}
			err := validator.Validate(context.Background(), reqURL, []byte{})
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errMsg != "" && err != nil {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %q, want it to contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestLoadSpec_LocalFile(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-spec-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte(testSpec)); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	tmpFile.Close()

	validator, _, err := New(context.Background(), &Config{Type: "file", Location: tmpFile.Name(), CacheTTL: 3600})
	if err != nil {
		t.Fatalf("Failed to load local spec: %v", err)
	}

	validator.specMutex.RLock()
	defer validator.specMutex.RUnlock()

	if validator.spec == nil || validator.spec.doc == nil {
		t.Error("Spec not loaded from local file")
	}
}

func TestCacheTTL_DefaultValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	if validator.config.CacheTTL != 3600 {
		t.Errorf("Expected default CacheTTL 3600, got %d", validator.config.CacheTTL)
	}
}

func TestValidate_EdgeCases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{
			name:    "empty payload",
			payload: `{}`,
			wantErr: true,
		},
		{
			name:    "null context",
			payload: `{"context":null,"message":{}}`,
			wantErr: true,
		},
		{
			name:    "empty string action",
			payload: `{"context":{"action":""},"message":{}}`,
			wantErr: true,
		},
		{
			name:    "action with whitespace",
			payload: `{"context":{"action":" search "},"message":{}}`,
			wantErr: true,
		},
		{
			name:    "case sensitive action",
			payload: `{"context":{"action":"Search"},"message":{}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(context.Background(), nil, []byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
