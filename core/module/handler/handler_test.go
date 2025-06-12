package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/stretchr/testify/mock"
)

// MockPluginManager implements handler.PluginManager for testing.
type MockPluginManager struct {
	mock.Mock
}

// Middleware returns a middleware function based on the provided configuration.
func (m *MockPluginManager) Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
	return nil, nil
}

// SignValidator returns a mock implementation of the Verifier interface.
func (m *MockPluginManager) SignValidator(ctx context.Context, cfg *plugin.Config) (definition.SignValidator, error) {
	return nil, nil
}

// Validator returns a mock implementation of the SchemaValidator interface.
func (m *MockPluginManager) Validator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}

// Router returns a mock implementation of the Router interface.
func (m *MockPluginManager) Router(ctx context.Context, cfg *plugin.Config) (definition.Router, error) {
	return nil, nil
}

// Publisher returns a mock implementation of the Publisher interface.
func (m *MockPluginManager) Publisher(ctx context.Context, cfg *plugin.Config) (definition.Publisher, error) {
	return nil, nil
}

// Signer returns a mock implementation of the Signer interface.
func (m *MockPluginManager) Signer(ctx context.Context, cfg *plugin.Config) (definition.Signer, error) {
	return nil, nil
}

// Step returns a mock implementation of the Step interface.
func (m *MockPluginManager) Step(ctx context.Context, cfg *plugin.Config) (definition.Step, error) {
	return nil, nil
}

// Cache returns a mock implementation of the Cache interface.
func (m *MockPluginManager) Cache(ctx context.Context, cfg *plugin.Config) (definition.Cache, error) {
	return nil, nil
}

// KeyManager returns a mock implementation of the KeyManager interface.
func (m *MockPluginManager) KeyManager(ctx context.Context, cache definition.Cache, rLookup definition.RegistryLookup, cfg *plugin.Config) (definition.KeyManager, error) {
	return nil, nil
}

// SchemaValidator returns a mock implementation of the SchemaValidator interface.
func (m *MockPluginManager) SchemaValidator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}

func TestNewStdHandler_Success(t *testing.T) {
	cfg := &Config{
		SubscriberID: "test-sub-id",
		Role:         model.RoleBAP,
		Plugins:      PluginCfg{},
		Steps:        []string{},
	}
	mgr := &MockPluginManager{}

	h, err := NewStdHandler(context.Background(), mgr, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("expected handler to be non-nil")
	}
}

func TestServeHTTP_MethodNotAllowed(t *testing.T) {
	handler := &stdHandler{}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}
	t.Logf("Raw response body: %s", bodyBytes)

	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected HTTP 500, got %d", res.StatusCode)
	}

	var respBody struct {
		Message struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"message"`
	}
	if err := json.Unmarshal(bodyBytes, &respBody); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}

	expectedCode := "Internal Server Error"
	if respBody.Message.Error.Code != expectedCode {
		t.Errorf("Expected error code %q, got %q", expectedCode, respBody.Message.Error.Code)
	}
}

// mockHandler overrides stepCtx for controlled testing.
type mockHandler struct {
	*stdHandler
	mockStepCtx func(r *http.Request, h http.Header) (*model.StepContext, error)
}

func TestServeHTTP_AckRouteNil(t *testing.T) {
	handler := &mockHandler{
		stdHandler: &stdHandler{
			steps:        []definition.Step{},
			SubscriberID: "mock-subscriber",
			role:         model.RoleBAP,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer([]byte(`{"test":"data"}`)))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte(`"status":"ACK"`)) {
		t.Errorf("Expected ACK response, got: %s", string(body))
	}
}

// brokenReader is a mock implementation of io.Reader that always returns an error.
type brokenReader struct{}

func (b *brokenReader) Read(p []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

func TestServeHTTP_InvalidBody(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "sub-id",
	}
	// Reader that returns error on Read
	badBody := io.NopCloser(&brokenReader{})
	req := httptest.NewRequest(http.MethodPost, "/", badBody)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestServeHTTP_MissingSubscriberID(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "",
	}
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// stepFunc is a helper type to mock the Step interface.
type stepFunc func(ctx *model.StepContext) error

// Run implements the Step interface for stepFunc.
func (f stepFunc) Run(ctx *model.StepContext) error {
	return f(ctx)
}

func TestServeHTTP_StepFails(t *testing.T) {
	handler := &stdHandler{
		steps: []definition.Step{
			stepFunc(func(ctx *model.StepContext) error {
				return &model.Error{
					Code:    "BadRequest",
					Message: "step failed",
				}
			}),
		},
		SubscriberID: "test-subscriber-id",
	}

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer([]byte(`{"message": "test"}`)))
	req = req.WithContext(context.WithValue(req.Context(), model.ContextKeySubscriberID, "test-subscriber-id"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}

}
