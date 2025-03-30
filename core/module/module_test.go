package module

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/beckn/beckn-onix/core/module/handler"
	"github.com/beckn/beckn-onix/pkg/plugin"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
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
	tests := []struct {
		name        string
		mCfgs       []Config
		mockManager *mockPluginManager
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			err := Register(context.Background(), tt.mCfgs, mux, tt.mockManager)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
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
