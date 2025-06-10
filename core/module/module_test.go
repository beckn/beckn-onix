package module

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beckn/beckn-onix/core/module/handler"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/stretchr/testify/assert"
)

// mockPluginManager is a mock implementation of the PluginManager interface
// with support for dynamically setting behavior.
type mockPluginManager struct {
	middlewareFunc func(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error)
	keyManagerFunc func(ctx context.Context, cache, lookup any, cfg *plugin.Config) (definition.KeyManager, error)
	signerFunc     func(ctx context.Context, cfg *plugin.Config) (definition.Signer, error)
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
	if m.signerFunc != nil {
		return m.signerFunc(ctx, cfg)
	}
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
	if m.keyManagerFunc != nil {
		return m.keyManagerFunc(ctx, cache, rLookup, cfg)
	}
	return nil, nil
}

// SchemaValidator returns a mock schema validator implementation.
func (m *mockPluginManager) SchemaValidator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}

// ////////////////

type mockKeyManager struct{}

func (m *mockKeyManager) EncrPublicKey(ctx context.Context, messageID string, additionalParam string) (string, error) {
	return "mock-encrypted-public-key", nil
}

func (m *mockKeyManager) EncrPrivateKey(ctx context.Context, messageID string) (string, string, error) {
	return "mock-encrypted-private-key", "mock-additional-value", nil
}

func (m *mockKeyManager) DeletePrivateKeys(ctx context.Context, messageID string) error {
	return nil
}

func (m *mockKeyManager) GenerateKeyPairs() (*model.Keyset, error) { return nil, nil }
func (m *mockKeyManager) StorePrivateKeys(ctx context.Context, id string, keySet *model.Keyset) error {
	return nil
}
func (m *mockKeyManager) SigningPrivateKey(ctx context.Context, keyID string) (string, string, error) {
	return "", "", nil
}
func (m *mockKeyManager) SigningPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error) {
	return "mockKey", nil
}

type mockSigner struct{}

func (m *mockSigner) Sign(ctx context.Context, payload []byte, privateKey string, validFrom, validUntil int64) (string, error) {
	return "signed", nil
}

/////////////////

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

func TestSubscribeHandlerProvider(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *handler.Config
		manager     *mockPluginManager
		expectError string
	}{
		{
			name: "Missing KeyManager plugin",
			cfg: &handler.Config{
				Plugins: handler.PluginCfg{
					KeyManager: nil,
					Signer:     &plugin.Config{ID: "signer"},
				},
			},
			manager:     &mockPluginManager{},
			expectError: "KeyManager plugin not configured",
		},
		{
			name: "Failed to load KeyManager",
			cfg: &handler.Config{
				Plugins: handler.PluginCfg{
					KeyManager: &plugin.Config{ID: "km"},
					Signer:     &plugin.Config{ID: "signer"},
				},
			},
			manager: &mockPluginManager{
				keyManagerFunc: func(ctx context.Context, _, _ any, cfg *plugin.Config) (definition.KeyManager, error) {
					return nil, errors.New("km error")
				},
			},
			expectError: "failed to get KeyManager: km error",
		},
		{
			name: "Missing Signer plugin",
			cfg: &handler.Config{
				Plugins: handler.PluginCfg{
					KeyManager: &plugin.Config{ID: "km"},
					Signer:     nil,
				},
			},
			manager:     &mockPluginManager{},
			expectError: "Signer plugin not configured",
		},
		{
			name: "Failed to load Signer plugin",
			cfg: &handler.Config{
				Plugins: handler.PluginCfg{
					KeyManager: &plugin.Config{ID: "km"},
					Signer:     &plugin.Config{ID: "signer"},
				},
			},
			manager: &mockPluginManager{
				keyManagerFunc: func(ctx context.Context, _, _ any, cfg *plugin.Config) (definition.KeyManager, error) {
					return &mockKeyManager{}, nil
				},
				signerFunc: func(ctx context.Context, cfg *plugin.Config) (definition.Signer, error) {
					return nil, errors.New("signer error")
				},
			},
			expectError: "failed to get Signer: signer error",
		},
		{
			name: "Successful handler creation",
			cfg: &handler.Config{
				RegistryURL: "http://registry.test",
				Plugins: handler.PluginCfg{
					KeyManager: &plugin.Config{ID: "km"},
					Signer:     &plugin.Config{ID: "signer"},
				},
			},
			manager: &mockPluginManager{
				keyManagerFunc: func(ctx context.Context, _, _ any, cfg *plugin.Config) (definition.KeyManager, error) {
					return &mockKeyManager{}, nil
				},
				signerFunc: func(ctx context.Context, cfg *plugin.Config) (definition.Signer, error) {
					return &mockSigner{}, nil
				},
			},
			expectError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerFunc := handlerProviders[handler.HandlerTypeSubscribe]
			h, err := handlerFunc(context.Background(), tt.manager, tt.cfg)

			if tt.expectError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectError)
				assert.Nil(t, h)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, h)
			}
		})
	}
}
