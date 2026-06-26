package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

// noopPluginManager satisfies PluginManager with nil plugins (unused loaders are never invoked when config is omitted).
type noopPluginManager struct{}

func TestExtractBecknAction(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{
			name:     "Valid body",
			body:     `{"context": {"action": "search"}}`,
			expected: "search",
		},
		{
			name:     "Different valid action",
			body:     `{"context": {"action": "select"}}`,
			expected: "select",
		},
		{
			name:     "Missing context",
			body:     `{"other": "data"}`,
			expected: "",
		},
		{
			name:     "Missing action",
			body:     `{"context": {"other": "data"}}`,
			expected: "",
		},
		{
			name:     "Malformed JSON",
			body:     `{"context": {"action": "search"`,
			expected: "",
		},
		{
			name:     "Empty body",
			body:     "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBecknAction([]byte(tt.body))
			if got != tt.expected {
				t.Errorf("extractBecknAction() = %v, want %v", got, tt.expected)
			}
		})
	}
}

type mockSpan struct {
	embedded.Span
	attributes []attribute.KeyValue
}

func (m *mockSpan) SetAttributes(attrs ...attribute.KeyValue) {
	m.attributes = append(m.attributes, attrs...)
}
func (m *mockSpan) End(options ...trace.SpanEndOption)                  {}
func (m *mockSpan) AddEvent(name string, options ...trace.EventOption) {}
func (m *mockSpan) AddLink(link trace.Link)                             {}
func (m *mockSpan) IsRecording() bool                                   { return true }
func (m *mockSpan) RecordError(err error, options ...trace.EventOption) {}
func (m *mockSpan) SpanContext() trace.SpanContext                      { return trace.SpanContext{} }
func (m *mockSpan) SetStatus(code codes.Code, description string)       {}
func (m *mockSpan) SetName(name string)                                 {}
func (m *mockSpan) TracerProvider() trace.TracerProvider                { return nil }

func TestSetBecknAttr(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "test-sub",
		moduleName:   "test-module",
	}
	span := &mockSpan{}
	req, _ := http.NewRequest("POST", "/test", nil)
	action := "search"

	setBecknAttr(span, req, h, action)

	found := false
	for _, attr := range span.attributes {
		if attr.Key == telemetry.AttrAction {
			found = true
			if attr.Value.AsString() != action {
				t.Errorf("expected action attribute %v, got %v", action, attr.Value.AsString())
			}
		}
	}
	if !found {
		t.Error("action attribute not found in span")
	}
}

func (noopPluginManager) Middleware(context.Context, *plugin.Config) (func(http.Handler) http.Handler, error) {
	return nil, nil
}
func (noopPluginManager) SignValidator(context.Context, *plugin.Config) (definition.SignValidator, error) {
	return nil, nil
}
func (noopPluginManager) Validator(context.Context, *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}
func (noopPluginManager) Router(context.Context, *plugin.Config) (definition.Router, error) {
	return nil, nil
}
func (noopPluginManager) Publisher(context.Context, *plugin.Config) (definition.Publisher, error) {
	return nil, nil
}
func (noopPluginManager) Signer(context.Context, *plugin.Config) (definition.Signer, error) {
	return nil, nil
}
func (noopPluginManager) Step(context.Context, *plugin.Config) (definition.Step, error) {
	return nil, nil
}
func (noopPluginManager) PolicyChecker(context.Context, definition.ManifestLoader, *plugin.Config) (definition.PolicyChecker, error) {
	return nil, nil
}
func (noopPluginManager) Cache(context.Context, *plugin.Config) (definition.Cache, error) {
	return nil, nil
}
func (noopPluginManager) Registry(context.Context, definition.Cache, *plugin.Config) (definition.RegistryLookup, error) {
	return nil, nil
}
func (noopPluginManager) KeyManager(context.Context, definition.RegistryLookup, *plugin.Config) (definition.KeyManager, error) {
	return nil, nil
}
func (noopPluginManager) ManifestLoader(context.Context, definition.Cache, definition.RegistryMetadataLookup, *plugin.Config) (definition.ManifestLoader, error) {
	return nil, nil
}
func (noopPluginManager) TransportWrapper(context.Context, *plugin.Config) (definition.TransportWrapper, error) {
	return nil, nil
}
func (noopPluginManager) SchemaValidator(context.Context, *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}
func (noopPluginManager) PayloadStore(_ context.Context, _ definition.Cache, _ string, _ *plugin.Config) (definition.PayloadStore, error) {
	return nil, nil
}

type registryWithoutMetadata struct{}

func (registryWithoutMetadata) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return nil, errors.New("not implemented")
}

type stubCache struct{}

func (stubCache) Get(context.Context, string) (string, error)              { return "", errors.New("cache miss") }
func (stubCache) Set(context.Context, string, string, time.Duration) error { return nil }
func (stubCache) Delete(context.Context, string) error                     { return nil }
func (stubCache) Clear(context.Context) error                              { return nil }


func TestNewStdHandler_CheckPolicyStepWithoutPluginFails(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		Plugins: PluginCfg{},
		Steps:   []string{"checkPolicy"},
	}
	_, err := NewStdHandler(ctx, noopPluginManager{}, cfg, "testModule")
	if err == nil {
		t.Fatal("expected error when steps list checkPolicy but checkPolicy plugin is omitted")
	}
	if !strings.Contains(err.Error(), "failed to initialize steps") {
		t.Fatalf("expected steps init failure, got: %v", err)
	}
	if !strings.Contains(err.Error(), "PolicyChecker plugin not configured") {
		t.Fatalf("expected explicit PolicyChecker config error, got: %v", err)
	}
}

func TestLoadManifestLoader_RequiresCache(t *testing.T) {
	_, err := loadManifestLoader(context.Background(), noopPluginManager{}, nil, registryWithoutMetadata{}, &plugin.Config{ID: "manifestloader"})
	if err == nil || !strings.Contains(err.Error(), "Cache plugin not configured") {
		t.Fatalf("expected cache requirement error, got %v", err)
	}
}

func TestLoadManifestLoader_RequiresRegistry(t *testing.T) {
	_, err := loadManifestLoader(context.Background(), noopPluginManager{}, stubCache{}, nil, &plugin.Config{ID: "manifestloader"})
	if err == nil || !strings.Contains(err.Error(), "Registry plugin not configured") {
		t.Fatalf("expected registry requirement error, got %v", err)
	}
}

func TestLoadManifestLoader_RequiresRegistryMetadataLookup(t *testing.T) {
	_, err := loadManifestLoader(context.Background(), noopPluginManager{}, stubCache{}, registryWithoutMetadata{}, &plugin.Config{ID: "manifestloader"})
	if err == nil || !strings.Contains(err.Error(), "does not implement RegistryMetadataLookup") {
		t.Fatalf("expected RegistryMetadataLookup error, got %v", err)
	}
}

func TestNewHTTPClient(t *testing.T) {
	tests := []struct {
		name     string
		config   HttpClientConfig
		expected struct {
			maxIdleConns          int
			maxIdleConnsPerHost   int
			idleConnTimeout       time.Duration
			responseHeaderTimeout time.Duration
		}
	}{
		{
			name: "all values configured",
			config: HttpClientConfig{
				MaxIdleConns:          1000,
				MaxIdleConnsPerHost:   200,
				IdleConnTimeout:       300 * time.Second,
				ResponseHeaderTimeout: 5 * time.Second,
			},
			expected: struct {
				maxIdleConns          int
				maxIdleConnsPerHost   int
				idleConnTimeout       time.Duration
				responseHeaderTimeout time.Duration
			}{
				maxIdleConns:          1000,
				maxIdleConnsPerHost:   200,
				idleConnTimeout:       300 * time.Second,
				responseHeaderTimeout: 5 * time.Second,
			},
		},
		{
			name:   "zero values use defaults",
			config: HttpClientConfig{},
			expected: struct {
				maxIdleConns          int
				maxIdleConnsPerHost   int
				idleConnTimeout       time.Duration
				responseHeaderTimeout time.Duration
			}{
				maxIdleConns:          100, // Go default
				maxIdleConnsPerHost:   0,   // Go default (unlimited per host)
				idleConnTimeout:       90 * time.Second,
				responseHeaderTimeout: 0,
			},
		},
		{
			name: "partial configuration",
			config: HttpClientConfig{
				MaxIdleConns:    500,
				IdleConnTimeout: 180 * time.Second,
			},
			expected: struct {
				maxIdleConns          int
				maxIdleConnsPerHost   int
				idleConnTimeout       time.Duration
				responseHeaderTimeout time.Duration
			}{
				maxIdleConns:          500,
				maxIdleConnsPerHost:   0, // Go default (unlimited per host)
				idleConnTimeout:       180 * time.Second,
				responseHeaderTimeout: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newHTTPClient(&tt.config, nil)

			if client == nil {
				t.Fatal("newHTTPClient returned nil")
			}

			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatal("client transport is not *http.Transport")
			}

			if transport.MaxIdleConns != tt.expected.maxIdleConns {
				t.Errorf("MaxIdleConns = %d, want %d", transport.MaxIdleConns, tt.expected.maxIdleConns)
			}

			if transport.MaxIdleConnsPerHost != tt.expected.maxIdleConnsPerHost {
				t.Errorf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, tt.expected.maxIdleConnsPerHost)
			}

			if transport.IdleConnTimeout != tt.expected.idleConnTimeout {
				t.Errorf("IdleConnTimeout = %v, want %v", transport.IdleConnTimeout, tt.expected.idleConnTimeout)
			}

			if transport.ResponseHeaderTimeout != tt.expected.responseHeaderTimeout {
				t.Errorf("ResponseHeaderTimeout = %v, want %v", transport.ResponseHeaderTimeout, tt.expected.responseHeaderTimeout)
			}
		})
	}
}

func TestHttpClientConfigDefaults(t *testing.T) {
	// Test that zero config values don't override defaults
	config := &HttpClientConfig{}
	client := newHTTPClient(config, nil)

	transport := client.Transport.(*http.Transport)

	// Verify defaults are preserved when config values are zero
	if transport.MaxIdleConns == 0 {
		t.Error("MaxIdleConns should not be zero when using defaults")
	}

	// MaxIdleConnsPerHost default is 0 (unlimited), which is correct
	if transport.MaxIdleConns != 100 {
		t.Errorf("Expected default MaxIdleConns=100, got %d", transport.MaxIdleConns)
	}
}

func TestHttpClientConfigPerformanceValues(t *testing.T) {
	// Test the specific performance-optimized values from the document
	config := &HttpClientConfig{
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   200,
		IdleConnTimeout:       300 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}

	client := newHTTPClient(config, nil)
	transport := client.Transport.(*http.Transport)

	// Verify performance-optimized values
	if transport.MaxIdleConns != 1000 {
		t.Errorf("Expected MaxIdleConns=1000, got %d", transport.MaxIdleConns)
	}

	if transport.MaxIdleConnsPerHost != 200 {
		t.Errorf("Expected MaxIdleConnsPerHost=200, got %d", transport.MaxIdleConnsPerHost)
	}

	if transport.IdleConnTimeout != 300*time.Second {
		t.Errorf("Expected IdleConnTimeout=300s, got %v", transport.IdleConnTimeout)
	}

	if transport.ResponseHeaderTimeout != 5*time.Second {
		t.Errorf("Expected ResponseHeaderTimeout=5s, got %v", transport.ResponseHeaderTimeout)
	}
}

func TestNewHTTPClientWithTransportWrapper(t *testing.T) {
	wrappedTransport := &mockRoundTripper{}
	wrapper := &mockTransportWrapper{
		returnTransport: wrappedTransport,
	}

	client := newHTTPClient(&HttpClientConfig{}, wrapper)

	if !wrapper.wrapCalled {
		t.Fatal("expected transport wrapper to be invoked")
	}

	if wrapper.wrappedTransport == nil {
		t.Fatal("expected base transport to be passed to wrapper")
	}

	if client.Transport != wrappedTransport {
		t.Errorf("expected client transport to use wrapper transport")
	}
}

func TestServeHTTP_ActionResolution(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		path           string
		expectedStatus int
	}{
		{
			name:           "Valid Beckn body",
			body:           `{"context": {"action": "search"}}`,
			path:           "/v1/search",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Empty body - fallback to path",
			body:           "",
			path:           "/v1/search",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Non-Beckn JSON - fallback to path",
			body:           `{"other": "data"}`,
			path:           "/v1/callback",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &stdHandler{
				SubscriberID: "test-sub",
				role:         model.RoleBAP,
				moduleName:   "test-module",
				steps:        []definition.Step{}, // No steps to avoid further logic
			}

			req, _ := http.NewRequest("POST", tt.path, strings.NewReader(tt.body))
			rr := httptest.NewRecorder()

			h.ServeHTTP(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %v, got %v", tt.expectedStatus, rr.Code)
			}
		})
	}
}

type errReader struct{}

func (e *errReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("forced read error")
}

func TestServeHTTP_BodyReadError(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "test-sub",
		role:         model.RoleBAP,
		moduleName:   "test-module",
	}

	req, _ := http.NewRequest("POST", "/test", &errReader{})
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 on body read error, got %v", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "failed to read request body") {
		t.Errorf("expected error message in body, got %v", rr.Body.String())
	}
}

type mockTransportWrapper struct {
	wrapCalled       bool
	wrappedTransport http.RoundTripper
	returnTransport  http.RoundTripper
}

func (m *mockTransportWrapper) Wrap(base http.RoundTripper) http.RoundTripper {
	m.wrapCalled = true
	m.wrappedTransport = base
	if m.returnTransport != nil {
		return m.returnTransport
	}
	return base
}

type mockRoundTripper struct{}

func (m *mockRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, nil
}

// mockResponseStep is a test double for definition.ResponseStep.
type mockResponseStep struct {
	called bool
	err    error
}

func (m *mockResponseStep) RunOnResponse(_ *model.StepContext, _ *model.ResponseStepContext) error {
	m.called = true
	return m.err
}

func TestServeHTTP_ResponseStepCalledAfterSteps(t *testing.T) {
	respStep := &mockResponseStep{}
	h := &stdHandler{
		SubscriberID:  "test-sub",
		role:          model.RoleBAP,
		moduleName:    "test-module",
		steps:         []definition.Step{},
		responseSteps: []definition.ResponseStep{respStep},
	}

	req, _ := http.NewRequest("POST", "/search", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !respStep.called {
		t.Error("expected ResponseStep.RunOnResponse to be called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestServeHTTP_ResponseStepErrorSendsNack(t *testing.T) {
	respStep := &mockResponseStep{err: fmt.Errorf("response step failed")}
	h := &stdHandler{
		SubscriberID:  "test-sub",
		role:          model.RoleBAP,
		moduleName:    "test-module",
		steps:         []definition.Step{},
		responseSteps: []definition.ResponseStep{respStep},
	}

	req, _ := http.NewRequest("POST", "/search", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on response step error, got %d", rr.Code)
	}
}

func TestServeHTTP_ResponseStepRunsAfterAllInboundSteps(t *testing.T) {
	order := []string{}

	inboundStep := &mockOrderStep{name: "inbound", order: &order}
	respStep := &mockOrderResponseStep{name: "response", order: &order}

	h := &stdHandler{
		SubscriberID:  "test-sub",
		role:          model.RoleBAP,
		moduleName:    "test-module",
		steps:         []definition.Step{inboundStep},
		responseSteps: []definition.ResponseStep{respStep},
	}

	req, _ := http.NewRequest("POST", "/search", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if len(order) != 2 || order[0] != "inbound" || order[1] != "response" {
		t.Errorf("unexpected execution order: %v", order)
	}
}

// TestServeHTTP_PipelineNack_SignedByAckSigner verifies that when the ackSigner
// is configured (signAck step), pipeline NACKs carry a Signature header — per
// NFH-007 CON-004-02 all synchronous responses MUST be signed.
func TestServeHTTP_PipelineNack_SignedByAckSigner(t *testing.T) {
	signer := &mockSigner{returnSig: "nackPipelineSig=="}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "k1", SigningPrivate: "priv"}}

	failingStep := &mockFailStep{err: model.NewBadReqErr(fmt.Errorf("schema error"))}

	ackSignerConc := &ackSignerStep{signer: signer, km: km}
	h := &stdHandler{
		SubscriberID:  "bpp.example.com",
		role:          model.RoleBPP,
		moduleName:    "test",
		steps:         []definition.Step{failingStep},
		responseSteps: []definition.ResponseStep{ackSignerConc},
		ackSigner:     ackSignerConc,
	}

	body := `{"context":{"version":"2.0.0","messageId":"m1","action":"search"}}`
	req, _ := http.NewRequest("POST", "/search", strings.NewReader(body))
	req.Header.Set("X-Subscriber-Id", "bpp.example.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	sig := rr.Header().Get("Signature")
	if sig == "" {
		t.Error("expected Signature header on pipeline NACK response — was unsigned")
	}
}

type mockFailStep struct{ err error }

func (m *mockFailStep) Run(_ *model.StepContext) error { return m.err }

type mockOrderStep struct {
	name  string
	order *[]string
}

func (m *mockOrderStep) Run(_ *model.StepContext) error {
	*m.order = append(*m.order, m.name)
	return nil
}

type mockOrderResponseStep struct {
	name  string
	order *[]string
}

func (m *mockOrderResponseStep) RunOnResponse(_ *model.StepContext, _ *model.ResponseStepContext) error {
	*m.order = append(*m.order, m.name)
	return nil
}

func TestExtractAuthSignature(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{
			name:     "valid Authorization header",
			header:   `Signature keyId="bpp.example.com|key-1|ed25519",algorithm="ed25519",created="1714000000",expires="1714000300",headers="(created) (expires) digest",signature="abc123=="`,
			expected: "abc123==",
		},
		{
			name:     "signature at end without trailing comma",
			header:   `Signature keyId="sub|key|ed25519",algorithm="ed25519",signature="xyz+/base64=="`,
			expected: "xyz+/base64==",
		},
		{
			name:     "no signature attribute",
			header:   `Signature keyId="sub|key|ed25519",algorithm="ed25519"`,
			expected: "",
		},
		{
			name:     "empty header",
			header:   "",
			expected: "",
		},
		{
			name:     "malformed — signature marker present but no closing quote",
			header:   `Signature signature="unclosed`,
			expected: "",
		},
		{
			name:     "empty signature value",
			header:   `Signature keyId="sub|key|ed25519",signature=""`,
			expected: "",
		},
		{
			// Regression: request-signature field appears AFTER signature in our
			// generated headers (normal ordering); must return the signature value.
			name:     "callback header — signature before request-signature",
			header:   `Signature keyId="bpp.example.com|key-1|ed25519",algorithm="ed25519",created="1",expires="2",headers="(created) (expires) digest request-signature",signature="correctSig==",request-signature="reqSig=="`,
			expected: "correctSig==",
		},
		{
			// Regression: if a peer implementation places request-signature BEFORE
			// signature, the old bare-string match would have returned the value
			// inside request-signature instead.  The comma-prefixed search must
			// return the correct standalone signature value.
			name:     "callback header — request-signature before signature (peer ordering)",
			header:   `Signature keyId="bpp.example.com|key-1|ed25519",algorithm="ed25519",created="1",expires="2",headers="(created) (expires) digest request-signature",request-signature="reqSig==",signature="correctSig=="`,
			expected: "correctSig==",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAuthSignature(tt.header)
			if got != tt.expected {
				t.Errorf("extractAuthSignature() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStepCtx_ProtocolVersion(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		authHeader  string
		wantVer     string
		wantMsgID   string
		wantAuthSig string
	}{
		{
			name:      "v2 version with messageId",
			body:      `{"context":{"version":"2.0.0","messageId":"550e8400-e29b-41d4-a716-446655440000"}}`,
			wantVer:   "2.0.0",
			wantMsgID: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:      "RC escape hatch 2.0.0-rc",
			body:      `{"context":{"version":"2.0.0-rc","messageId":"abc-123"}}`,
			wantVer:   "2.0.0-rc",
			wantMsgID: "abc-123",
		},
		{
			name:    "pre-v2 version 1.1.0",
			body:    `{"context":{"version":"1.1.0"}}`,
			wantVer: "1.1.0",
		},
		{
			name:    "version field missing",
			body:    `{"context":{}}`,
			wantVer: "",
		},
		{
			name:    "context field missing",
			body:    `{}`,
			wantVer: "",
		},
		{
			name:    "invalid JSON",
			body:    `not-json`,
			wantVer: "",
		},
		{
			name:        "Authorization header with signature extracted",
			body:        `{"context":{"version":"2.0.0","messageId":"msg-789"}}`,
			authHeader:  `Signature keyId="bpp.example.com|key-1|ed25519",algorithm="ed25519",created="1714000000",expires="1714000300",headers="(created) (expires) digest",signature="sigBase64=="`,
			wantVer:     "2.0.0",
			wantMsgID:   "msg-789",
			wantAuthSig: "sigBase64==",
		},
		{
			name:        "no Authorization header — empty signature",
			body:        `{"context":{"version":"2.0.0","messageId":"msg-000"}}`,
			authHeader:  "",
			wantVer:     "2.0.0",
			wantMsgID:   "msg-000",
			wantAuthSig: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &stdHandler{
				role:         model.RoleBAP,
				SubscriberID: "test-subscriber",
			}
			req := httptest.NewRequest(http.MethodPost, "/search", bytes.NewBufferString(tt.body))
			if tt.authHeader != "" {
				req.Header.Set(model.AuthHeaderSubscriber, tt.authHeader)
			}
			sctx, err := h.stepCtx(req, http.Header{})
			if err != nil {
				t.Fatalf("stepCtx() returned unexpected error: %v", err)
			}
			if sctx.ProtocolVersion != tt.wantVer {
				t.Errorf("ProtocolVersion = %q, want %q", sctx.ProtocolVersion, tt.wantVer)
			}
			if sctx.MessageID != tt.wantMsgID {
				t.Errorf("MessageID = %q, want %q", sctx.MessageID, tt.wantMsgID)
			}
			// Verify messageId is also propagated into Go context for response functions.
			if ctxMsgID, _ := sctx.Value(model.ContextKeyMsgID).(string); ctxMsgID != tt.wantMsgID {
				t.Errorf("context ContextKeyMsgID = %q, want %q", ctxMsgID, tt.wantMsgID)
			}
			if sctx.InboundAuthSignature != tt.wantAuthSig {
				t.Errorf("InboundAuthSignature = %q, want %q", sctx.InboundAuthSignature, tt.wantAuthSig)
			}
		})
	}
}

// TestProxy_ModifyResponse_202_InvokesResponseSteps verifies that the thin
// ModifyResponse dispatcher in proxy() fires responseSteps on a 202 upstream
// response. This covers the AckNoCallback relay path: app sends 202, ONIX
// relays it, ackSigner (or any ResponseStep) runs via ModifyResponse.
func TestProxy_ModifyResponse_202_InvokesResponseSteps(t *testing.T) {
	const ackBody = `{"message":{"status":"ACK","error":{"code":"NO_CATALOG","message":"no catalog"}}}`

	// Upstream app: returns 202 with AckNoCallback body.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, ackBody)
	}))
	defer upstream.Close()

	// ResponseStep that records being called and sets a sentinel header.
	respStep := &mockResponseStep{}

	upstreamURL, _ := url.Parse(upstream.URL)
	stepCtx := &model.StepContext{
		Context: context.Background(),
		Route:   &model.Route{TargetType: "url", URL: upstreamURL},
	}

	req := httptest.NewRequest(http.MethodPost, "/bpp/receiver/", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()

	var responseBody []byte
	proxy(stepCtx, req, rr, http.DefaultClient, []definition.ResponseStep{respStep}, &responseBody)

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected upstream 202 to be relayed, got %d", rr.Code)
	}
	if !respStep.called {
		t.Error("expected ResponseStep to be called via ModifyResponse on 202 upstream response")
	}
	if rr.Body.String() != ackBody {
		t.Errorf("expected upstream body to be relayed unchanged: got %q", rr.Body.String())
	}
}

// stubPayloadStore is a minimal PayloadStore for storePayloadStep unit tests.
type stubPayloadStore struct {
	stored   []*model.StepContext
	storeErr error
}

func (s *stubPayloadStore) Store(ctx *model.StepContext) error {
	if s.storeErr != nil {
		return s.storeErr
	}
	s.stored = append(s.stored, ctx)
	return nil
}

func (s *stubPayloadStore) Exists(_ context.Context, _ string) (bool, error) { return false, nil }
func (s *stubPayloadStore) GetByTransactionID(_ context.Context, _ string) ([]definition.PayloadEntry, error) {
	return nil, nil
}
func (s *stubPayloadStore) GetByMessageID(_ context.Context, _, _ string) (*definition.PayloadEntry, error) {
	return nil, nil
}

func TestNewStorePayloadStep_NilPayloadStoreFails(t *testing.T) {
	_, err := newStorePayloadStep(nil)
	if err == nil {
		t.Fatal("expected error for nil PayloadStore")
	}
}

func TestStorePayloadStep_Run_DelegatesToStore(t *testing.T) {
	ps := &stubPayloadStore{}
	step, err := newStorePayloadStep(ps)
	if err != nil {
		t.Fatalf("newStorePayloadStep: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	ctx := &model.StepContext{
		Context: context.Background(),
		Request: req,
		Body:    []byte(`{"context":{"message_id":"m1","transaction_id":"t1","action":"search"}}`),
	}

	if err := step.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ps.stored) != 1 || ps.stored[0] != ctx {
		t.Errorf("expected Store called once with the same context; got %d calls", len(ps.stored))
	}
}

func TestStorePayloadStep_Run_PropagatesError(t *testing.T) {
	ps := &stubPayloadStore{storeErr: errors.New("cache down")}
	step, _ := newStorePayloadStep(ps)

	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	ctx := &model.StepContext{Context: context.Background(), Request: req}

	if err := step.Run(ctx); err == nil {
		t.Fatal("expected error from Run when Store fails")
	}
}

// TestProxy_QueryParamsForwardedToUpstream verifies that the proxy director
// forwards RawQuery from the route URL verbatim to the upstream server.
// The companion router tests (TestRouteQueryParamsForwarded) verify that the
// router correctly populates route.URL.RawQuery from the inbound request.
func TestProxy_QueryParamsForwardedToUpstream(t *testing.T) {
	var capturedRawQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Simulate what the router produces after the fix: a route URL that already
	// carries the inbound query string on its RawQuery field.
	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamURL.RawQuery = "subscriptionId=test123&page=2"

	stepCtx := &model.StepContext{
		Context: context.Background(),
		Route:   &model.Route{TargetType: "url", URL: upstreamURL},
	}

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()

	var responseBody []byte
	proxy(stepCtx, req, rr, http.DefaultClient, nil, &responseBody)

	if capturedRawQuery != "subscriptionId=test123&page=2" {
		t.Errorf("upstream received RawQuery = %q, want %q", capturedRawQuery, "subscriptionId=test123&page=2")
	}
}
