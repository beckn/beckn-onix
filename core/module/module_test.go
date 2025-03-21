package module

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beckn/beckn-onix/core/module/handler"
	"github.com/beckn/beckn-onix/plugin"
	"github.com/beckn/beckn-onix/plugin/definition"
)

// mockPluginManager is a mock implementation of the PluginManager interface
type mockPluginManager struct {
	middlewareFunc func(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error)
}

// Middleware mocks the Middleware method of PluginManager.
func (m *mockPluginManager) Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
	return m.middlewareFunc(ctx, cfg)
}

// SignValidator mocks the SignValidator method of PluginManager.
func (m *mockPluginManager) SignValidator(ctx context.Context, cfg *plugin.Config) (definition.SignValidator, error) {
	return nil, nil
}

// Validator mocks the Validator method of PluginManager.
func (m *mockPluginManager) Validator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}

// Router mocks the Router method of PluginManager.
func (m *mockPluginManager) Router(ctx context.Context, cfg *plugin.Config) (definition.Router, error) {
	return nil, nil
}

// Publisher mocks the Publisher method of PluginManager.
func (m *mockPluginManager) Publisher(ctx context.Context, cfg *plugin.Config) (definition.Publisher, error) {
	return nil, nil
}

// Signer mocks the Signer method of PluginManager.
func (m *mockPluginManager) Signer(ctx context.Context, cfg *plugin.Config) (definition.Signer, error) {
	return nil, nil
}

// Step mocks the Step method of PluginManager.
func (m *mockPluginManager) Step(ctx context.Context, cfg *plugin.Config) (definition.Step, error) {
	return nil, nil
}

// TestRegister tests the Register function for different scenarios.
func TestRegister(t *testing.T) {
	// Preserve the original handler provider and restore it after tests.
	originalHandlerProviders := getHandlerProviders
	defer func() { getHandlerProviders = originalHandlerProviders }()

	// Override handler providers for testing purposes.
	getHandlerProviders = func() map[handler.HandlerType]handler.Provider {
		return GetDummyHandlerProviders()
	}

	tests := []struct {
		name        string
		mCfgs       []Config
		mockManager *mockPluginManager
		expectError bool
	}{
		{
			name: "successful registration",
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
					return func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							next.ServeHTTP(w, r)
						})
					}, nil
				},
			},
			expectError: false,
		},
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
			expectError: true,
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
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			err := Register(context.Background(), tt.mCfgs, mux, tt.mockManager)

			// Check if an error occurred when it was expected.
			if (err != nil) != tt.expectError {
				t.Errorf("expected error: %v, got: %v", tt.expectError, err)
			}

			// If no error, test if the registered handler responds correctly.
			if !tt.expectError {
				req := httptest.NewRequest(http.MethodGet, "/test", nil)
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, req)

				if status := rr.Code; status != http.StatusOK {
					t.Errorf("expected status OK, got: %v", status)
				}
			}
		})
	}
}
