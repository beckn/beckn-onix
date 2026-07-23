package schemav2validator

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/getkin/kin-openapi/openapi3"
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
		{"empty type with location set", &Config{Type: "", Location: "http://example.com"}, true},
		{"empty location with type set", &Config{Type: "url", Location: ""}, true},
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
			// endpointAction is not used for body-bearing requests; pass "" for clarity.
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

// TestValidate_SchemaErrorDetails confirms extractSchemaErrors only attaches
// Details when the underlying validation cause has a non-empty path — never
// a non-nil Details with an empty Path (the fix for issue #862's finding #5).
func TestValidate_SchemaErrorDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	err = validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"select"},"message":{}}`))

	var schemaErr *model.SchemaValidationErr
	if !errors.As(err, &schemaErr) {
		t.Fatalf("expected *model.SchemaValidationErr, got %T: %v", err, err)
	}
	if len(schemaErr.Errors) == 0 {
		t.Fatal("expected at least one schema error")
	}

	hasDetails := false
	for _, got := range schemaErr.Errors {
		t.Logf("Code = %s, Details = %+v", got.Code, got.Details)
		if got.Details != nil && got.Details.Path == "" {
			t.Errorf("Details = %+v, want either nil or a non-empty Path — never a non-nil Details with an empty Path", got.Details)
		}
		if got.Details != nil && got.Details.Path != "" {
			hasDetails = true
		}
	}
	if !hasDetails {
		t.Error("expected at least one schema error with a non-nil Details and a non-empty Path, got none")
	}

	// "message.order" is required and missing — SchemaField "required" must
	// map to SCH_REQUIRED_FIELD_MISSING.
	if schemaErr.Errors[0].Code != "SCH_REQUIRED_FIELD_MISSING" {
		t.Errorf("Code = %s, want SCH_REQUIRED_FIELD_MISSING", schemaErr.Errors[0].Code)
	}

	beErr := schemaErr.BecknError()
	if beErr.Code != "SCH_REQUIRED_FIELD_MISSING" {
		t.Errorf("aggregate Code = %s, want SCH_REQUIRED_FIELD_MISSING (first non-empty per-cause code)", beErr.Code)
	}
}

func TestSchemaFieldToCode(t *testing.T) {
	tests := []struct {
		field string
		want  string
	}{
		{"required", "SCH_REQUIRED_FIELD_MISSING"},
		{"properties", "SCH_FIELD_NOT_ALLOWED"},
		{"enum", "SCH_INVALID_ENUM"},
		{"const", "SCH_INVALID_ENUM"},
		{"format", "SCH_INVALID_FORMAT"},
		{"type", "SCH_TYPE_NOT_SUPPORTED"},
		{"allOf", "SCH_SCHEMA_VALIDATION_FAILED"},
		{"oneOf", "SCH_SCHEMA_VALIDATION_FAILED"},
		{"anyOf", "SCH_SCHEMA_VALIDATION_FAILED"},
		{"pattern", "SCH_SCHEMA_VALIDATION_FAILED"},
		{"", "SCH_SCHEMA_VALIDATION_FAILED"},
		{"unknown-keyword", "SCH_SCHEMA_VALIDATION_FAILED"},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			if got := schemaFieldToCode(tt.field); got != tt.want {
				t.Errorf("schemaFieldToCode(%q) = %s, want %s", tt.field, got, tt.want)
			}
		})
	}
}

func TestValidate_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	err = validator.Validate(context.Background(), nil, []byte(`{"context":`))

	var schemaErr *model.SchemaValidationErr
	if !errors.As(err, &schemaErr) {
		t.Fatalf("expected *model.SchemaValidationErr, got %T: %v", err, err)
	}
	if len(schemaErr.Errors) != 1 || schemaErr.Errors[0].Code != "SCH_INVALID_JSON" {
		t.Errorf("Errors = %+v, want one entry with Code=SCH_INVALID_JSON", schemaErr.Errors)
	}

	beErr := schemaErr.BecknError()
	if beErr.Code != "SCH_INVALID_JSON" {
		t.Errorf("aggregate Code = %s, want SCH_INVALID_JSON", beErr.Code)
	}
}

// TestValidate_NumericOverflowFailsSecondUnmarshal exercises Validate()'s
// second json.Unmarshal call (into `any`, for schema validation) failing
// independently of the first (into the narrow `payload` struct, used only to
// extract context.action). A numeric literal outside float64 range decodes
// fine into `payload` — which ignores unrelated fields — but fails when
// decoded into `any`, which must convert every value.
func TestValidate_NumericOverflowFailsSecondUnmarshal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	err = validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"select"},"message":{"order":{}},"extra":1e400}`))

	var schemaErr *model.SchemaValidationErr
	if !errors.As(err, &schemaErr) {
		t.Fatalf("expected *model.SchemaValidationErr, got %T: %v", err, err)
	}
	if len(schemaErr.Errors) != 1 || schemaErr.Errors[0].Code != "SCH_INVALID_JSON" {
		t.Errorf("Errors = %+v, want one entry with Code=SCH_INVALID_JSON", schemaErr.Errors)
	}
}

// TestExtractSchemaErrors_CompositeOriginFallsBackToGeneric exercises the
// Origin != nil branch (oneOf/anyOf/allOf composite failures), previously
// uncovered by any test. Since kin-openapi attributes the failure to the
// composite keyword itself (not the nested cause), classification correctly
// falls back to the generic SCH_SCHEMA_VALIDATION_FAILED rather than
// guessing at whichever nested branch failed.
func TestExtractSchemaErrors_CompositeOriginFallsBackToGeneric(t *testing.T) {
	sub := openapi3.NewObjectSchema().WithProperty("name", openapi3.NewStringSchema())
	sub.Required = []string{"name"}

	parent := openapi3.NewObjectSchema()
	parent.AllOf = openapi3.SchemaRefs{openapi3.NewSchemaRef("", sub)}

	err := parent.VisitJSON(map[string]interface{}{})
	if err == nil {
		t.Fatal("expected a validation error")
	}
	schemaErr, ok := err.(*openapi3.SchemaError)
	if !ok {
		t.Fatalf("expected *openapi3.SchemaError, got %T: %v", err, err)
	}
	if schemaErr.SchemaField != "allOf" || schemaErr.Origin == nil {
		t.Fatalf("expected SchemaField=allOf with a non-nil Origin, got SchemaField=%s, Origin=%v", schemaErr.SchemaField, schemaErr.Origin)
	}

	v := &schemav2Validator{}
	var schemaErrors []model.Error
	v.extractSchemaErrors(err, &schemaErrors)

	if len(schemaErrors) != 1 {
		t.Fatalf("expected exactly one extracted error, got %d: %+v", len(schemaErrors), schemaErrors)
	}
	got := schemaErrors[0]
	t.Logf("extracted = %+v", got)
	if got.Code != "SCH_SCHEMA_VALIDATION_FAILED" {
		t.Errorf("Code = %s, want SCH_SCHEMA_VALIDATION_FAILED", got.Code)
	}
	if got.Message == "" {
		t.Error("Message = \"\", want a non-empty message describing the nested cause")
	}
}

// TestExtractSchemaErrors_CompositeOriginParsesNestedFieldPath exercises the
// "Error at \"/path\": reason" string-parsing branch specifically — triggered
// when the nested cause inside an allOf/oneOf failure occurred on a property
// deep enough to have a tagged reversePath. Previously uncovered by any test.
func TestExtractSchemaErrors_CompositeOriginParsesNestedFieldPath(t *testing.T) {
	innerSub := openapi3.NewObjectSchema().WithProperty("name", openapi3.NewStringSchema())
	innerSub.Required = []string{"name"}
	sub := openapi3.NewObjectSchema().WithProperty("nested", innerSub)

	parent := openapi3.NewObjectSchema()
	parent.AllOf = openapi3.SchemaRefs{openapi3.NewSchemaRef("", sub)}

	err := parent.VisitJSON(map[string]interface{}{"nested": map[string]interface{}{}})
	if err == nil {
		t.Fatal("expected a validation error")
	}
	schemaErr, ok := err.(*openapi3.SchemaError)
	if !ok {
		t.Fatalf("expected *openapi3.SchemaError, got %T: %v", err, err)
	}
	if schemaErr.Origin == nil || !strings.Contains(schemaErr.Origin.Error(), `Error at "`) {
		t.Fatalf(`expected Origin.Error() to contain 'Error at "', got: %v`, schemaErr.Origin)
	}

	v := &schemav2Validator{}
	var schemaErrors []model.Error
	v.extractSchemaErrors(err, &schemaErrors)

	if len(schemaErrors) != 1 {
		t.Fatalf("expected exactly one extracted error, got %d: %+v", len(schemaErrors), schemaErrors)
	}
	got := schemaErrors[0]
	t.Logf("extracted = %+v", got)
	if got.Details == nil || got.Details.Path != "nested/name" {
		t.Errorf("Details = %+v, want Path=nested/name", got.Details)
	}
	// kin-openapi's SchemaError.Error() appends a verbose schema/value dump
	// after the reason — check the extracted reason is the prefix, not an
	// exact match against that library-formatted tail.
	if !strings.HasPrefix(got.Message, `property "name" is missing`) {
		t.Errorf("Message = %q, want it to start with %q", got.Message, `property "name" is missing`)
	}
}

// TestExtractSchemaErrors_ConstViolation confirms a JSON-schema "const"
// violation — SchemaField "const", no dedicated SCH_* code in the taxonomy —
// classifies as SCH_INVALID_ENUM, since const is enum-of-one.
func TestExtractSchemaErrors_ConstViolation(t *testing.T) {
	sub := openapi3.NewStringSchema()
	sub.Const = "search"

	err := sub.VisitJSON("select")
	if err == nil {
		t.Fatal("expected a validation error")
	}
	schemaErr, ok := err.(*openapi3.SchemaError)
	if !ok {
		t.Fatalf("expected *openapi3.SchemaError, got %T: %v", err, err)
	}
	if schemaErr.SchemaField != "const" {
		t.Fatalf("expected SchemaField=const, got %s", schemaErr.SchemaField)
	}

	v := &schemav2Validator{}
	var schemaErrors []model.Error
	v.extractSchemaErrors(err, &schemaErrors)

	if len(schemaErrors) != 1 {
		t.Fatalf("expected exactly one extracted error, got %d: %+v", len(schemaErrors), schemaErrors)
	}
	if got := schemaErrors[0].Code; got != "SCH_INVALID_ENUM" {
		t.Errorf("Code = %s, want SCH_INVALID_ENUM", got)
	}
}

// TestExtractSchemaErrors_BecknErrorPassthrough closes the coverage gap on
// extractSchemaErrors' *model.Error passthrough branch (used to carry
// validateReferencedObject's already-classified domain/JSON-LD/@type errors
// into the aggregate SchemaValidationErr) — previously exercised by no test.
func TestExtractSchemaErrors_BecknErrorPassthrough(t *testing.T) {
	original := model.NewCodedError("SCH_INVALID_JSONLD_CONTEXT", "domain not allowed: malicious.com")

	v := &schemav2Validator{}
	var schemaErrors []model.Error
	v.extractSchemaErrors(original, &schemaErrors)

	if len(schemaErrors) != 1 {
		t.Fatalf("expected exactly one extracted error, got %d: %+v", len(schemaErrors), schemaErrors)
	}
	if schemaErrors[0] != *original {
		t.Errorf("extractSchemaErrors did not pass through *model.Error as-is: got %+v, want %+v", schemaErrors[0], *original)
	}
}

// TestValidate_BodylessRequest covers GET/DELETE requests (Beckn 2.0
// catalog/subscription verbs) that carry no body. The step layer has already
// stripped the module base path and extracted the action string before calling
// Validate, so endpointAction is a clean key like "catalog/subscription".
func TestValidate_BodylessRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpecBodyless))
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		urlPath string // set to "" to pass nil URL
		wantErr bool
		errMsg  string
	}{
		{
			// Multi-segment action — indexed in bodylessActions, must pass.
			name:    "GET catalog/subscription — empty body passes",
			urlPath: "catalog/subscription",
			wantErr: false,
		},
		{
			// DELETE on the same action — same key, passes for the same reason.
			name:    "DELETE catalog/subscription — empty body passes",
			urlPath: "catalog/subscription",
			wantErr: false,
		},
		{
			// The plugin itself is method-agnostic; empty-body POST rejection is
			// enforced upstream by validateSchemaStep before Validate is called.
			name:    "empty-body POST to bodyless-indexed action passes — method enforcement is upstream",
			urlPath: "catalog/subscription",
			wantErr: false,
		},
		{
			// Nil URL with empty body must return an error.
			// The step layer passes nil when ctx.Request.URL is nil.
			name:    "nil URL with empty body returns error",
			urlPath: "",
			wantErr: true,
			errMsg:  "request URL is required for bodyless validation",
		},
		{
			// /search only has a POST with requestBody — not in bodylessActions.
			name:    "POST-only endpoint search with empty body is rejected",
			urlPath: "search",
			wantErr: true,
			errMsg:  "unsupported bodyless request for endpoint: search",
		},
		{
			// Action absent from spec entirely.
			name:    "unknown action nonsense with empty body is rejected",
			urlPath: "nonsense",
			wantErr: true,
			errMsg:  "unsupported bodyless request for endpoint: nonsense",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqURL *url.URL
			if tt.urlPath != "" {
				reqURL = &url.URL{Path: tt.urlPath}
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

	if len(validator.actionSchemas) == 0 {
		t.Error("Spec not loaded from local file — action index is empty")
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

// testSpecV2 is a replacement spec used in TTL-refresh tests; it exposes only the "confirm" action (distinct from testSpec).
const testSpecV2 = `openapi: 3.1.0
info:
  title: Test API v2
  version: 2.0.0
paths:
  /confirm:
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
                      const: confirm
                message:
                  type: object
`

// testSpecAux is an OpenAPI 3.1 spec used as an auxiliary spec in tests; it defines the "subscribe" and "renew" actions.
const testSpecAux = `openapi: 3.1.0
info:
  title: Auxiliary API
  version: 1.0.0
paths:
  /subscribe:
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
                  properties:
                    action:
                      const: subscribe
                message:
                  type: object
  /renew:
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
                  properties:
                    action:
                      const: renew
                message:
                  type: object
`

// testSpecConflict defines "search" — same action as testSpec — to trigger hard-reject.
const testSpecConflict = `openapi: 3.1.0
info:
  title: Conflict API
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
              properties:
                context:
                  type: object
                  properties:
                    action:
                      const: search
`

func TestAuxiliary_AddsNewActions(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer primary.Close()

	auxiliary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpecAux))
	}))
	defer auxiliary.Close()

	validator, _, err := New(context.Background(), &Config{
		Type:     "url",
		Location: primary.URL,
		CacheTTL: 3600,
		Auxiliary: []AuxSpec{
			{Type: "url", Location: auxiliary.URL},
		},
	})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	cases := []struct {
		action  string
		payload string
	}{
		{"search", `{"context":{"action":"search","domain":"retail"},"message":{}}`},
		{"select", `{"context":{"action":"select"},"message":{"order":{}}}`},
		{"subscribe", `{"context":{"action":"subscribe"},"message":{}}`},
		{"renew", `{"context":{"action":"renew"},"message":{}}`},
	}
	for _, c := range cases {
		if err := validator.Validate(context.Background(), nil, []byte(c.payload)); err != nil {
			t.Errorf("action %q: unexpected validation error: %v", c.action, err)
		}
	}
}

func TestAuxiliary_ShadowPrimaryHardRejects(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer primary.Close()

	conflict := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpecConflict))
	}))
	defer conflict.Close()

	_, _, err := New(context.Background(), &Config{
		Type:     "url",
		Location: primary.URL,
		CacheTTL: 3600,
		Auxiliary: []AuxSpec{
			{Type: "url", Location: conflict.URL},
		},
	})
	if err == nil {
		t.Fatal("New() expected error for action collision, got nil")
	}
	if !contains(err.Error(), "already defined") {
		t.Errorf("New() error = %v, want it to mention 'already defined'", err)
	}
}

func TestAuxiliary_DirType(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "aux1.yaml"), []byte(testSpecAux), 0644); err != nil {
		t.Fatalf("failed to write aux1.yaml: %v", err)
	}

	// A second spec with a unique action to confirm both files are loaded.
	spec2 := `openapi: 3.1.0
info:
  title: Dir Spec 2
  version: 1.0.0
paths:
  /cancel:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                context:
                  type: object
                  properties:
                    action:
                      const: cancel
`
	if err := os.WriteFile(filepath.Join(dir, "aux2.yaml"), []byte(spec2), 0644); err != nil {
		t.Fatalf("failed to write aux2.yaml: %v", err)
	}

	validator, _, err := New(context.Background(), &Config{
		CacheTTL: 3600,
		Auxiliary: []AuxSpec{
			{Type: "dir", Location: dir},
		},
	})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	for _, payload := range []string{
		`{"context":{"action":"subscribe"},"message":{}}`,
		`{"context":{"action":"renew"},"message":{}}`,
		`{"context":{"action":"cancel"}}`,
	} {
		if err := validator.Validate(context.Background(), nil, []byte(payload)); err != nil {
			t.Errorf("dir type validation failed for %s: %v", payload, err)
		}
	}
}

func TestAuxiliary_NoPrimary_NonBeckn(t *testing.T) {
	auxiliary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpecAux))
	}))
	defer auxiliary.Close()

	validator, _, err := New(context.Background(), &Config{
		CacheTTL: 3600,
		Auxiliary: []AuxSpec{
			{Type: "url", Location: auxiliary.URL},
		},
	})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"subscribe"},"message":{}}`)); err != nil {
		t.Errorf("non-Beckn deployment: unexpected validation error: %v", err)
	}
}

func TestAuxiliary_BadLocationSkipped(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer primary.Close()

	// Adapter should still start; the bad auxiliary is skipped.
	validator, _, err := New(context.Background(), &Config{
		Type:     "url",
		Location: primary.URL,
		CacheTTL: 3600,
		Auxiliary: []AuxSpec{
			{Type: "url", Location: "http://invalid-domain-99999.example.com/spec.yaml"},
		},
	})
	if err != nil {
		t.Fatalf("New() unexpected error for bad auxiliary: %v", err)
	}

	// Primary actions still available.
	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"search","domain":"retail"},"message":{}}`)); err != nil {
		t.Errorf("primary action failed after bad auxiliary skipped: %v", err)
	}
}

// TestAuxiliary_RefreshRetainsIndexOnAuxFailure verifies that when an auxiliary
// spec becomes unavailable during a TTL refresh, the old merged index is retained
// so that all previously-valid actions remain available.
func TestAuxiliary_RefreshRetainsIndexOnAuxFailure(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testSpec))
	}))
	defer primary.Close()

	// Auxiliary starts healthy. Use atomic.Bool to avoid a data race between
	// the test goroutine (which sets the flag) and the httptest handler goroutine.
	var auxHealthy atomic.Bool
	auxHealthy.Store(true)
	aux := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auxHealthy.Load() {
			w.Write([]byte(testSpecAux))
		} else {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
	}))
	defer aux.Close()

	validator, _, err := New(context.Background(), &Config{
		Type:     "url",
		Location: primary.URL,
		CacheTTL: 3600,
		Auxiliary: []AuxSpec{
			{Type: "url", Location: aux.URL},
		},
	})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	// Auxiliary actions should be available after startup.
	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"subscribe"},"message":{}}`)); err != nil {
		t.Fatalf("auxiliary action unavailable at startup: %v", err)
	}

	// Auxiliary goes down — the reload hits the server, gets a 503, and the old index is retained.
	auxHealthy.Store(false)

	// Force a TTL refresh.
	validator.reloadAllSpecs(context.Background())

	// Old index must be retained: both primary and auxiliary actions stay available.
	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"subscribe"},"message":{}}`)); err != nil {
		t.Errorf("auxiliary action dropped after failed refresh — old index should have been retained: %v", err)
	}

	// Primary actions must also still work.
	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"search","domain":"retail"},"message":{}}`)); err != nil {
		t.Errorf("primary action dropped after failed refresh: %v", err)
	}
}

func TestAuxiliary_DirType_IntraDirCollisionHardRejects(t *testing.T) {
	dir := t.TempDir()

	// Both files define "subscribe" — within-dir collision must cause New() to fail.
	// The specific collision error is logged; New() surfaces "no actions indexed"
	// because the colliding dir spec is skipped (same skip policy as load failures)
	// and no other spec is configured.
	for _, name := range []string{"a.yaml", "b.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(testSpecAux), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	_, _, err := New(context.Background(), &Config{
		CacheTTL: 3600,
		Auxiliary: []AuxSpec{
			{Type: "dir", Location: dir},
		},
	})
	if err == nil {
		t.Fatal("New() expected error for intra-dir action collision, got nil")
	}
	// The adapter refuses to start; the specific collision detail appears in error logs.
	if !contains(err.Error(), "no actions indexed") {
		t.Errorf("New() error = %v, want it to mention 'no actions indexed'", err)
	}
}

func TestAuxiliary_AllSpecsFail_HardRejects(t *testing.T) {
	// No primary, no valid auxiliary — adapter must refuse to start.
	_, _, err := New(context.Background(), &Config{
		CacheTTL: 3600,
		Auxiliary: []AuxSpec{
			{Type: "url", Location: "http://invalid-domain-99999.example.com/spec.yaml"},
		},
	})
	if err == nil {
		t.Fatal("New() expected error when all specs fail to load, got nil")
	}
	if !contains(err.Error(), "no actions indexed") {
		t.Errorf("New() error = %v, want it to mention 'no actions indexed'", err)
	}
}

func TestFreshReadFromURI_BodyTooLarge(t *testing.T) {
	old := maxSpecBodyBytes
	maxSpecBodyBytes = 10
	defer func() { maxSpecBodyBytes = old }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("this body is definitely more than ten bytes"))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	_, err := freshReadFromURI(newFreshLoader(), u)
	if err == nil {
		t.Fatal("expected error for oversized response body, got nil")
	}
}

func TestTTLRefresh_PicksUpChangedURLSpec(t *testing.T) {
	var serveV2 atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveV2.Load() {
			w.Write([]byte(testSpecV2))
		} else {
			w.Write([]byte(testSpec))
		}
	}))
	defer server.Close()

	validator, _, err := New(context.Background(), &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"search","domain":"retail"},"message":{}}`)); err != nil {
		t.Fatalf("search action unavailable at startup: %v", err)
	}

	serveV2.Store(true)
	validator.reloadAllSpecs(context.Background())

	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"confirm"},"message":{}}`)); err != nil {
		t.Errorf("confirm action unavailable after TTL refresh — v2 spec not picked up: %v", err)
	}
}

func TestTTLRefresh_PicksUpChangedDirSpec(t *testing.T) {
	dir := t.TempDir()
	specFile := filepath.Join(dir, "spec.yaml")

	if err := os.WriteFile(specFile, []byte(testSpec), 0644); err != nil {
		t.Fatalf("failed to write initial spec: %v", err)
	}

	validator, _, err := New(context.Background(), &Config{Type: "dir", Location: dir, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"search","domain":"retail"},"message":{}}`)); err != nil {
		t.Fatalf("search action unavailable at startup: %v", err)
	}

	// Overwrite the existing file in-place — this is the case issue #795 highlighted
	// as silently ignored before the fix.
	if err := os.WriteFile(specFile, []byte(testSpecV2), 0644); err != nil {
		t.Fatalf("failed to overwrite spec file: %v", err)
	}

	validator.reloadAllSpecs(context.Background())

	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"confirm"},"message":{}}`)); err != nil {
		t.Errorf("confirm action unavailable after TTL refresh — in-place file edit not picked up: %v", err)
	}
}

func TestTTLRefresh_PicksUpChangedFileSpec(t *testing.T) {
	f, err := os.CreateTemp("", "spec-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(testSpec); err != nil {
		t.Fatalf("failed to write v1 spec: %v", err)
	}
	f.Close()

	validator, _, err := New(context.Background(), &Config{Type: "file", Location: f.Name(), CacheTTL: 3600})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"search","domain":"retail"},"message":{}}`)); err != nil {
		t.Fatalf("search action unavailable at startup: %v", err)
	}

	if err := os.WriteFile(f.Name(), []byte(testSpecV2), 0644); err != nil {
		t.Fatalf("failed to overwrite spec file: %v", err)
	}

	validator.reloadAllSpecs(context.Background())

	if err := validator.Validate(context.Background(), nil, []byte(`{"context":{"action":"confirm"},"message":{}}`)); err != nil {
		t.Errorf("confirm action unavailable after TTL refresh — v2 spec not picked up: %v", err)
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
