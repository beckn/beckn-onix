package module

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// mockPluginManager is a mock implementation of the PluginManager interface
// with support for dynamically setting behavior.
type mockPluginManager struct {
	middlewareFunc func(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error)
}

// Middleware returns a mock middleware function based on the provided configuration.
func (m *mockPluginManager) Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
	return m.middlewareFunc(ctx, cfg)
}

// SignValidator returns a mock verifier implementation.
func (m *mockPluginManager) SignValidator(ctx context.Context, cfg *plugin.Config) (definition.SignValidator, error) {
	return nil, nil
}

// Validator returns a mock schema validator implementation.
func (m *mockPluginManager) Validator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}

// Router returns a mock router implementation.
func (m *mockPluginManager) Router(ctx context.Context, cfg *plugin.Config) (definition.Router, error) {
	return nil, nil
}

// Publisher returns a mock publisher implementation.
func (m *mockPluginManager) Publisher(ctx context.Context, cfg *plugin.Config) (definition.Publisher, error) {
	return nil, nil
}

// Signer returns a mock signer implementation.
func (m *mockPluginManager) Signer(ctx context.Context, cfg *plugin.Config) (definition.Signer, error) {
	return nil, nil
}

// Step returns a mock step implementation.
func (m *mockPluginManager) Step(ctx context.Context, cfg *plugin.Config) (definition.Step, error) {
	return nil, nil
}

// Cache returns a mock cache implementation.
func (m *mockPluginManager) Cache(ctx context.Context, cfg *plugin.Config) (definition.Cache, error) {
	return nil, nil
}

// KeyManager returns a mock key manager implementation.
func (m *mockPluginManager) KeyManager(ctx context.Context, cache definition.Cache, rLookup definition.RegistryLookup, cfg *plugin.Config) (definition.KeyManager, error) {
	return nil, nil
}

// SchemaValidator returns a mock schema validator implementation.
func (m *mockPluginManager) SchemaValidator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}

// TestRegisterSuccess tests scenarios where the handler registration should succeed.
func TestRegisterSuccess(t *testing.T) {
	mCfgs := []Config{
		{
			Name: "test-module",
			Path: "/test",
			Handler: handler.Config{
				Type: handler.HandlerTypeStd,
				Plugins: handler.PluginCfg{
					Middleware: []plugin.Config{{ID: "mock-middleware"}},
				},
			},
		},
	}

	mockManager := &mockPluginManager{
		middlewareFunc: func(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
			return func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r)
				})
			}, nil
		},
	}

	mux := http.NewServeMux()
	err := Register(context.Background(), mCfgs, mux, mockManager)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Create a request and a response recorder
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	// Create a handler that extracts context
	var capturedModuleName any
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedModuleName = r.Context().Value(model.ContextKeyModuleID)
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := moduleCtxMiddleware("test-module", testHandler)
	wrappedHandler.ServeHTTP(rec, req)

	// Now verify if module name exists in context
	if capturedModuleName != "test-module" {
		t.Errorf("expected module_id in context to be 'test-module', got %v", capturedModuleName)
	}
	// Verifying /health endpoint registration
	reqHealth := httptest.NewRequest(http.MethodGet, "/health", nil)
	recHealth := httptest.NewRecorder()
	mux.ServeHTTP(recHealth, reqHealth)

	if status := recHealth.Code; status != http.StatusOK {
		t.Errorf("handler for /health returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}
}

// TestRegisterFailure tests scenarios where the handler registration should fail.
func TestRegisterFailure(t *testing.T) {
	tests := []struct {
		name        string
		mCfgs       []Config
		mockManager *mockPluginManager
	}{
		{
			name: "invalid handler type",
			mCfgs: []Config{
				{
					Name: "invalid-module",
					Path: "/invalid",
					Handler: handler.Config{
						Type: "invalid-type",
					},
				},
			},
			mockManager: &mockPluginManager{},
		},
		{
			name: "middleware error",
			mCfgs: []Config{
				{
					Name: "test-module",
					Path: "/test",
					Handler: handler.Config{
						Type: handler.HandlerTypeStd,
						Plugins: handler.PluginCfg{
							Middleware: []plugin.Config{{ID: "mock-middleware"}},
						},
					},
				},
			},
			mockManager: &mockPluginManager{
				middlewareFunc: func(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
					return nil, errors.New("middleware error")
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			err := Register(context.Background(), tt.mCfgs, mux, tt.mockManager)
			if err == nil {
				t.Errorf("expected an error but got nil")
			}
		})
	}
}