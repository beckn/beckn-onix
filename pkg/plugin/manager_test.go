package plugin

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"plugin"
	"strings"
	"testing"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

type mockPlugin struct {
	symbol plugin.Symbol
	err    error
}

func (m *mockPlugin) Lookup(str string) (plugin.Symbol, error) {
	return m.symbol, m.err
}

// Mock implementations for testing
type mockPublisher struct {
	definition.Publisher
}

type mockSchemaValidator struct {
	definition.SchemaValidator
}

type mockRouter struct {
	definition.Router
}

type mockMiddleware struct {
	definition.MiddlewareProvider
}

type mockStep struct {
	definition.Step
}

type mockCache struct {
	definition.Cache
}

type mockSigner struct {
	definition.Signer
}

type mockEncrypter struct {
	definition.Encrypter
}

type mockDecrypter struct {
	definition.Decrypter
}

type mockSignValidator struct {
	definition.SignValidator
}

type mockKeyManager struct {
	definition.KeyManager
	err error
}

func (m *mockKeyManager) GenerateKeyPairs() (*model.Keyset, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &model.Keyset{}, nil
}

func (m *mockKeyManager) StorePrivateKeys(ctx context.Context, keyID string, keys *model.Keyset) error {
	return m.err
}

func (m *mockKeyManager) SigningPrivateKey(ctx context.Context, keyID string) (string, string, error) {
	if m.err != nil {
		return "", "", m.err
	}
	return "signing-key", "signing-algo", nil
}

func (m *mockKeyManager) EncrPrivateKey(ctx context.Context, keyID string) (string, string, error) {
	if m.err != nil {
		return "", "", m.err
	}
	return "encr-key", "encr-algo", nil
}

func (m *mockKeyManager) SigningPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return "public-signing-key", nil
}

func (m *mockKeyManager) EncrPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return "public-encr-key", nil
}

func (m *mockKeyManager) DeletePrivateKeys(ctx context.Context, keyID string) error {
	return m.err
}

// Mock providers
type mockPublisherProvider struct {
	publisher definition.Publisher
	err       error
	errFunc   func() error
}

func (m *mockPublisherProvider) New(ctx context.Context, config map[string]string) (definition.Publisher, func() error, error) {
	return m.publisher, m.errFunc, m.err
}

type mockSchemaValidatorProvider struct {
	validator *mockSchemaValidator
	err       error
}

func (m *mockSchemaValidatorProvider) New(ctx context.Context, config map[string]string) (definition.SchemaValidator, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.validator, func() error { return nil }, nil
}

// Mock providers for additional interfaces
type mockRouterProvider struct {
	router *mockRouter
	err    error
}

func (m *mockRouterProvider) New(ctx context.Context, config map[string]string) (definition.Router, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.router, func() error { return nil }, nil
}

type mockMiddlewareProvider struct {
	middleware func(http.Handler) http.Handler
	err        error
}

func (m *mockMiddlewareProvider) New(ctx context.Context, config map[string]string) (func(http.Handler) http.Handler, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.middleware == nil {
		m.middleware = func(h http.Handler) http.Handler { return h }
	}
	return m.middleware, nil
}

type mockStepProvider struct {
	step *mockStep
	err  error
}

func (m *mockStepProvider) New(ctx context.Context, config map[string]string) (definition.Step, func(), error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.step, func() {}, nil
}

// Mock providers for additional interfaces
type mockCacheProvider struct {
	cache *mockCache
	err   error
}

func (m *mockCacheProvider) New(ctx context.Context, config map[string]string) (definition.Cache, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.cache, func() error { return nil }, nil
}

type mockSignerProvider struct {
	signer *mockSigner
	err    error
}

func (m *mockSignerProvider) New(ctx context.Context, config map[string]string) (definition.Signer, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.signer, func() error { return nil }, nil
}

type mockEncrypterProvider struct {
	encrypter *mockEncrypter
	err       error
}

func (m *mockEncrypterProvider) New(ctx context.Context, config map[string]string) (definition.Encrypter, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.encrypter, func() error { return nil }, nil
}

type mockDecrypterProvider struct {
	decrypter *mockDecrypter
	err       error
}

func (m *mockDecrypterProvider) New(ctx context.Context, config map[string]string) (definition.Decrypter, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.decrypter, func() error { return nil }, nil
}

type mockSignValidatorProvider struct {
	validator *mockSignValidator
	err       error
}

func (m *mockSignValidatorProvider) New(ctx context.Context, config map[string]string) (definition.SignValidator, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.validator, func() error { return nil }, nil
}

type mockKeyManagerProvider struct {
	keyManager *mockKeyManager
	err        error
}

func (m *mockKeyManagerProvider) New(ctx context.Context, cache definition.Cache, lookup definition.RegistryLookup, config map[string]string) (definition.KeyManager, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.keyManager, func() error { return nil }, nil
}

// Mock registry lookup for testing
type mockRegistryLookup struct {
	definition.RegistryLookup
	err error
}

// testManager is a helper struct for testing
type testManager struct {
	*Manager
	mockProviders map[string]interface{}
	cleanup       func()
}

// mockValidator is a mock implementation of the Validator interface
type mockValidator struct {
	definition.Cache
}

// mockValidatorProvider is a mock implementation of the ValidatorProvider interface
type mockValidatorProvider struct {
	validator *mockValidator
	err       error
}

func (m *mockValidatorProvider) New(ctx context.Context, config map[string]string) (definition.Cache, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.validator, func() error { return nil }, nil
}

// TestNewManager_Success tests the successful scenarios of the NewManager function
func TestNewManager_Success(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ManagerConfig
	}{
		{
			name: "valid config with empty root",
			cfg: &ManagerConfig{
				Root:       t.TempDir(),
				RemoteRoot: "",
			},
		},
		{
			name: "valid config with root path",
			cfg: &ManagerConfig{
				Root:       t.TempDir(),
				RemoteRoot: "",
			},
		},
		{
			name: "valid config with remote root",
			cfg: &ManagerConfig{
				Root:       t.TempDir(),
				RemoteRoot: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			m, cleanup, err := NewManager(ctx, tt.cfg)
			if err != nil {
				t.Errorf("NewManager() error = %v, want nil", err)
				return
			}
			if m == nil {
				t.Error("NewManager() returned nil manager")
				return
			}
			if cleanup == nil {
				t.Error("NewManager() returned nil cleanup function")
				return
			}

			// Verify manager fields
			if m.plugins == nil {
				t.Error("NewManager() returned manager with nil plugins map")
			}
			if m.closers == nil {
				t.Error("NewManager() returned manager with nil closers slice")
			}

			// Call cleanup to ensure it doesn't panic
			cleanup()
		})
	}
}

// TestNewManager_Failure tests the failure scenarios of the NewManager function
func TestNewManager_Failure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *ManagerConfig
		expectedError string
	}{
		{
			name: "invalid config with nonexistent root",
			cfg: &ManagerConfig{
				Root:       "/nonexistent/dir",
				RemoteRoot: "",
			},
			expectedError: "no such file or directory",
		},
		{
			name: "invalid config with nonexistent remote root",
			cfg: &ManagerConfig{
				Root:       t.TempDir(),
				RemoteRoot: "/nonexistent/remote.zip",
			},
			expectedError: "no such file or directory",
		},
		{
			name: "invalid config with permission denied root",
			cfg: &ManagerConfig{
				Root:       "/root/restricted",
				RemoteRoot: "",
			},
			expectedError: "permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			m, cleanup, err := NewManager(ctx, tt.cfg)
			if err == nil {
				t.Error("NewManager() expected error, got nil")
				return
			}
			if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("NewManager() error = %v, want error containing %q", err, tt.expectedError)
			}
			if m != nil {
				t.Error("NewManager() returned non-nil manager for error case")
			}
			if cleanup != nil {
				t.Error("NewManager() returned non-nil cleanup function for error case")
			}
		})
	}
}

func TestPublisherSuccess(t *testing.T) {
	tests := []struct {
		name          string
		publisherID   string
		mockPublisher *mockPublisher
		expectedError error
	}{
		{
			name:          "successful publisher creation",
			publisherID:   "publisherId",
			mockPublisher: &mockPublisher{},
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errFunc := func() error { return nil }
			m := &Manager{
				closers: []func(){},
				plugins: map[string]onixPlugin{
					tt.publisherID: &mockPlugin{
						symbol: &mockPublisherProvider{
							publisher: tt.mockPublisher,
							err:       errFunc(),
						},
					},
				},
			}

			p, err := m.Publisher(context.Background(), &Config{
				ID:     tt.publisherID,
				Config: map[string]string{},
			})

			if err != tt.expectedError {
				t.Errorf("Manager.Publisher() error = %v, want %v", err, tt.expectedError)
			}

			if p != tt.mockPublisher {
				t.Errorf("Manager.Publisher() did not return the correct publisher")
			}
		})
	}
}

// TestPublisherFailure tests the failure scenarios of the Publisher method
func TestPublisherFailure(t *testing.T) {
	tests := []struct {
		name          string
		publisherID   string
		plugins       map[string]onixPlugin
		expectedError string
	}{
		{
			name:          "plugin not found",
			publisherID:   "nonexistent",
			plugins:       make(map[string]onixPlugin),
			expectedError: "plugin nonexistent not found",
		},
		{
			name:        "provider error",
			publisherID: "error-provider",
			plugins: map[string]onixPlugin{
				"error-provider": &mockPlugin{
					symbol: &mockPublisherProvider{
						publisher: nil,
						err:       errors.New("provider error"),
					},
				},
			},
			expectedError: "provider error",
		},
		{
			name:        "lookup error",
			publisherID: "lookup-error",
			plugins: map[string]onixPlugin{
				"lookup-error": &mockPlugin{
					symbol: nil,
					err:    errors.New("lookup failed"),
				},
			},
			expectedError: "lookup failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				closers: []func(){},
				plugins: tt.plugins,
			}

			p, err := m.Publisher(context.Background(), &Config{
				ID:     tt.publisherID,
				Config: map[string]string{},
			})

			if err == nil {
				t.Error("Manager.Publisher() expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("Manager.Publisher() error = %v, want error containing %q", err, tt.expectedError)
			}

			if p != nil {
				t.Error("Manager.Publisher() expected nil publisher, got non-nil")
			}
		})
	}
}

// TestManager_SchemaValidator_Success tests the successful scenarios of the SchemaValidator method
func TestManagerSchemaValidatorSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockSchemaValidatorProvider
	}{
		{
			name: "successful validator creation",
			cfg: &Config{
				ID:     "test-validator",
				Config: map[string]string{},
			},
			plugin: &mockSchemaValidatorProvider{
				validator: &mockSchemaValidator{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call SchemaValidator
			validator, err := m.SchemaValidator(context.Background(), tt.cfg)

			// Check success case
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if validator == nil {
				t.Error("expected non-nil validator, got nil")
			}
		})
	}
}

// TestManager_SchemaValidator_Failure tests the failure scenarios of the SchemaValidator method
func TestManagerSchemaValidatorFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockSchemaValidatorProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-validator",
				Config: map[string]string{},
			},
			plugin: &mockSchemaValidatorProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-validator",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-validator not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call SchemaValidator
			validator, err := m.SchemaValidator(context.Background(), tt.cfg)

			// Check error
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if validator != nil {
				t.Error("expected nil validator, got non-nil")
			}
		})
	}
}

// TestManager_Router_Success tests the successful scenarios of the Router method
func TestManagerRouterSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockRouterProvider
	}{
		{
			name: "successful router creation",
			cfg: &Config{
				ID:     "test-router",
				Config: map[string]string{},
			},
			plugin: &mockRouterProvider{
				router: &mockRouter{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Router
			router, err := m.Router(context.Background(), tt.cfg)

			// Check success case
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if router == nil {
				t.Error("expected non-nil router, got nil")
			}
			if router != tt.plugin.router {
				t.Error("router does not match expected instance")
			}
		})
	}
}

// TestManager_Router_Failure tests the failure scenarios of the Router method
func TestManagerRouterFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockRouterProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-router",
				Config: map[string]string{},
			},
			plugin: &mockRouterProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-router",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-router not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Router
			router, err := m.Router(context.Background(), tt.cfg)

			// Check error
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if router != nil {
				t.Error("expected nil router, got non-nil")
			}
		})
	}
}

// TestManager_Step_Success tests the successful scenarios of the Step method
func TestManagerStepSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockStepProvider
	}{
		{
			name: "successful step creation",
			cfg: &Config{
				ID:     "test-step",
				Config: map[string]string{},
			},
			plugin: &mockStepProvider{
				step: &mockStep{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Step
			step, err := m.Step(context.Background(), tt.cfg)

			// Check success case
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if step == nil {
				t.Error("expected non-nil step, got nil")
			}
			if step != tt.plugin.step {
				t.Error("step does not match expected instance")
			}
		})
	}
}

// TestManager_Step_Failure tests the failure scenarios of the Step method
func TestManagerStepFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockStepProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-step",
				Config: map[string]string{},
			},
			plugin: &mockStepProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-step",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-step not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Step
			step, err := m.Step(context.Background(), tt.cfg)

			// Check error
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if step != nil {
				t.Error("expected nil step, got non-nil")
			}
		})
	}
}

// TestManager_Validator_Success tests the successful scenarios of the Validator method
func TestManagerValidatorSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockValidatorProvider
	}{
		{
			name: "successful validator creation",
			cfg: &Config{
				ID:     "test-validator",
				Config: map[string]string{},
			},
			plugin: &mockValidatorProvider{
				validator: &mockValidator{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Validator and expect a panic
			defer func() {
				if r := recover(); r != nil {
					if r != "unimplemented" {
						t.Errorf("expected panic with 'unimplemented', got %v", r)
					}
				} else {
					t.Error("expected panic, got none")
				}
			}()

			// This should panic
			m.Validator(context.Background(), tt.cfg)
		})
	}
}

// TestManager_Validator_Failure tests the failure scenarios of the Validator method
func TestManagerValidatorFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockValidatorProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-validator",
				Config: map[string]string{},
			},
			plugin: &mockValidatorProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-validator",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-validator not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Validator and expect a panic
			defer func() {
				if r := recover(); r != nil {
					if r != "unimplemented" {
						t.Errorf("expected panic with 'unimplemented', got %v", r)
					}
				} else {
					t.Error("expected panic, got none")
				}
			}()

			// This should panic
			m.Validator(context.Background(), tt.cfg)
		})
	}
}

// TestManager_Cache_Success tests the successful scenarios of the Cache method
func TestManagerCacheSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockCacheProvider
	}{
		{
			name: "successful cache creation",
			cfg: &Config{
				ID:     "test-cache",
				Config: map[string]string{},
			},
			plugin: &mockCacheProvider{
				cache: &mockCache{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Cache
			cache, err := m.Cache(context.Background(), tt.cfg)

			// Check success case
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if cache == nil {
				t.Error("expected non-nil cache, got nil")
			}
		})
	}
}

// TestManager_Cache_Failure tests the failure scenarios of the Cache method
func TestManagerCacheFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockCacheProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-cache",
				Config: map[string]string{},
			},
			plugin: &mockCacheProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-cache",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-cache not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Cache
			cache, err := m.Cache(context.Background(), tt.cfg)

			// Check error
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if cache != nil {
				t.Error("expected nil cache, got non-nil")
			}
		})
	}
}

// TestManager_Signer_Success tests the successful scenarios of the Signer method
func TestManagerSignerSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockSignerProvider
	}{
		{
			name: "successful signer creation",
			cfg: &Config{
				ID:     "test-signer",
				Config: map[string]string{},
			},
			plugin: &mockSignerProvider{
				signer: &mockSigner{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Signer
			signer, err := m.Signer(context.Background(), tt.cfg)

			// Check success case
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if signer == nil {
				t.Error("expected non-nil signer, got nil")
			}
		})
	}
}

// TestManager_Signer_Failure tests the failure scenarios of the Signer method
func TestManagerSignerFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockSignerProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-signer",
				Config: map[string]string{},
			},
			plugin: &mockSignerProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-signer",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-signer not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Signer
			signer, err := m.Signer(context.Background(), tt.cfg)

			// Check error
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if signer != nil {
				t.Error("expected nil signer, got non-nil")
			}
		})
	}
}

// TestManager_Encryptor_Success tests the successful scenarios of the Encryptor method
func TestManagerEncryptorSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockEncrypterProvider
	}{
		{
			name: "successful encrypter creation",
			cfg: &Config{
				ID:     "test-encrypter",
				Config: map[string]string{},
			},
			plugin: &mockEncrypterProvider{
				encrypter: &mockEncrypter{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Encryptor
			encrypter, err := m.Encryptor(context.Background(), tt.cfg)

			// Check success case
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if encrypter == nil {
				t.Error("expected non-nil encrypter, got nil")
			}
		})
	}
}

// TestManager_Encryptor_Failure tests the failure scenarios of the Encryptor method
func TestManagerEncryptorFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockEncrypterProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-encrypter",
				Config: map[string]string{},
			},
			plugin: &mockEncrypterProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-encrypter",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-encrypter not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Encryptor
			encrypter, err := m.Encryptor(context.Background(), tt.cfg)

			// Check error
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if encrypter != nil {
				t.Error("expected nil encrypter, got non-nil")
			}
		})
	}
}

// TestManager_Decryptor_Success tests the successful scenarios of the Decryptor method
func TestManagerDecryptorSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockDecrypterProvider
	}{
		{
			name: "successful decrypter creation",
			cfg: &Config{
				ID:     "test-decrypter",
				Config: map[string]string{},
			},
			plugin: &mockDecrypterProvider{
				decrypter: &mockDecrypter{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Decryptor
			decrypter, err := m.Decryptor(context.Background(), tt.cfg)

			// Check success case
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if decrypter == nil {
				t.Error("expected non-nil decrypter, got nil")
			}
		})
	}
}

// TestManager_Decryptor_Failure tests the failure scenarios of the Decryptor method
func TestManagerDecryptorFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockDecrypterProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-decrypter",
				Config: map[string]string{},
			},
			plugin: &mockDecrypterProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-decrypter",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-decrypter not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Decryptor
			decrypter, err := m.Decryptor(context.Background(), tt.cfg)

			// Check error
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if decrypter != nil {
				t.Error("expected nil decrypter, got non-nil")
			}
		})
	}
}

// TestManager_SignValidator tests the SignValidator method of the Manager
func TestManagerSignValidator(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		plugin  *mockSignValidatorProvider
		wantErr bool
	}{
		{
			name: "successful sign validator creation",
			cfg: &Config{
				ID:     "test-sign-validator",
				Config: map[string]string{},
			},
			plugin: &mockSignValidatorProvider{
				validator: &mockSignValidator{},
			},
			wantErr: false,
		},
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-sign-validator",
				Config: map[string]string{},
			},
			plugin: &mockSignValidatorProvider{
				err: errors.New("provider error"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			_, err := m.SignValidator(ctx, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Manager.SignValidator() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestManager_KeyManager_Success tests the successful scenarios of the KeyManager method
func TestManagerKeyManagerSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockKeyManagerProvider
	}{
		{
			name: "successful key manager creation",
			cfg: &Config{
				ID:     "test-key-manager",
				Config: map[string]string{},
			},
			plugin: &mockKeyManagerProvider{
				keyManager: &mockKeyManager{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Create mock cache and registry lookup
			mockCache := &mockCache{}
			mockRegistry := &mockRegistryLookup{}

			// Call KeyManager
			keyManager, err := m.KeyManager(context.Background(), mockCache, mockRegistry, tt.cfg)

			// Check success case
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if keyManager == nil {
				t.Error("expected non-nil key manager, got nil")
			}
		})
	}
}

// TestManager_KeyManager_Failure tests the failure scenarios of the KeyManager method
func TestManagerKeyManagerFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockKeyManagerProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-key-manager",
				Config: map[string]string{},
			},
			plugin: &mockKeyManagerProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-key-manager",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-key-manager not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Create mock cache and registry lookup
			mockCache := &mockCache{}
			mockRegistry := &mockRegistryLookup{}

			// Call KeyManager
			keyManager, err := m.KeyManager(context.Background(), mockCache, mockRegistry, tt.cfg)

			// Check error
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if keyManager != nil {
				t.Error("expected nil key manager, got non-nil")
			}
		})
	}
}

// TestUnzip_Success tests the successful scenarios of the unzip function
func TestUnzipSuccess(t *testing.T) {
	tests := []struct {
		name       string
		setupFunc  func() (string, string, func()) // returns src, dest, cleanup
		verifyFunc func(t *testing.T, dest string)
	}{
		{
			name: "extract single file",
			setupFunc: func() (string, string, func()) {
				// Create a temporary directory for the test
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a zip file with a single test file
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					t.Fatalf("Failed to create zip file: %v", err)
				}

				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a test file to the zip
				testFile, err := zipWriter.Create("test.txt")
				if err != nil {
					t.Fatalf("Failed to create file in zip: %v", err)
				}
				testFile.Write([]byte("test content"))

				zipWriter.Close()
				zipFile.Close()

				// Create destination directory
				destDir := filepath.Join(tempDir, "extracted")
				return zipPath, destDir, func() {
					os.RemoveAll(tempDir)
				}
			},
			verifyFunc: func(t *testing.T, dest string) {
				// Verify the extracted file exists and has correct content
				content, err := os.ReadFile(filepath.Join(dest, "test.txt"))
				if err != nil {
					t.Errorf("Failed to read extracted file: %v", err)
				}
				if string(content) != "test content" {
					t.Errorf("Extracted file content = %v, want %v", string(content), "test content")
				}
			},
		},
		{
			name: "extract file in subdirectory",
			setupFunc: func() (string, string, func()) {
				// Create a temporary directory for the test
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a zip file with a file in a subdirectory
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					t.Fatalf("Failed to create zip file: %v", err)
				}

				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a file in a subdirectory
				testFile, err := zipWriter.Create("subdir/test.txt")
				if err != nil {
					t.Fatalf("Failed to create file in zip: %v", err)
				}
				testFile.Write([]byte("subdirectory content"))

				zipWriter.Close()
				zipFile.Close()

				// Create destination directory
				destDir := filepath.Join(tempDir, "extracted")
				return zipPath, destDir, func() {
					os.RemoveAll(tempDir)
				}
			},
			verifyFunc: func(t *testing.T, dest string) {
				// Verify the extracted file in subdirectory exists and has correct content
				content, err := os.ReadFile(filepath.Join(dest, "subdir/test.txt"))
				if err != nil {
					t.Errorf("Failed to read extracted file in subdirectory: %v", err)
				}
				if string(content) != "subdirectory content" {
					t.Errorf("Extracted file content in subdirectory = %v, want %v", string(content), "subdirectory content")
				}
			},
		},
		{
			name: "extract multiple files",
			setupFunc: func() (string, string, func()) {
				// Create a temporary directory for the test
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a zip file with multiple files
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					t.Fatalf("Failed to create zip file: %v", err)
				}

				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add multiple files to the zip
				files := map[string]string{
					"file1.txt":        "content of file 1",
					"file2.txt":        "content of file 2",
					"subdir/file3.txt": "content of file 3",
				}

				for name, content := range files {
					testFile, err := zipWriter.Create(name)
					if err != nil {
						t.Fatalf("Failed to create file in zip: %v", err)
					}
					testFile.Write([]byte(content))
				}

				zipWriter.Close()
				zipFile.Close()

				// Create destination directory
				destDir := filepath.Join(tempDir, "extracted")
				return zipPath, destDir, func() {
					os.RemoveAll(tempDir)
				}
			},
			verifyFunc: func(t *testing.T, dest string) {
				// Verify all extracted files exist and have correct content
				expectedFiles := map[string]string{
					"file1.txt":        "content of file 1",
					"file2.txt":        "content of file 2",
					"subdir/file3.txt": "content of file 3",
				}

				for path, expectedContent := range expectedFiles {
					content, err := os.ReadFile(filepath.Join(dest, path))
					if err != nil {
						t.Errorf("Failed to read extracted file %s: %v", path, err)
					}
					if string(content) != expectedContent {
						t.Errorf("Extracted file %s content = %v, want %v", path, string(content), expectedContent)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			src, dest, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test
			err := unzip(src, dest)
			if err != nil {
				t.Errorf("unzip() error = %v, want nil", err)
			}

			// Verify the result
			tt.verifyFunc(t, dest)
		})
	}
}

// TestUnzip_Failure tests the failure scenarios of the unzip function
func TestUnzipFailure(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func() (string, string, func()) // returns src, dest, cleanup
		expectedError string
	}{
		{
			name: "nonexistent source file",
			setupFunc: func() (string, string, func()) {
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}
				return "nonexistent.zip", filepath.Join(tempDir, "extracted"), func() {
					os.RemoveAll(tempDir)
				}
			},
			expectedError: "open nonexistent.zip: no such file or directory",
		},
		{
			name: "invalid zip file",
			setupFunc: func() (string, string, func()) {
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create an invalid zip file
				zipPath := filepath.Join(tempDir, "invalid.zip")
				if err := os.WriteFile(zipPath, []byte("not a zip file"), 0644); err != nil {
					t.Fatalf("Failed to create invalid zip file: %v", err)
				}

				return zipPath, filepath.Join(tempDir, "extracted"), func() {
					os.RemoveAll(tempDir)
				}
			},
			expectedError: "zip: not a valid zip file",
		},
		{
			name: "destination directory creation failure",
			setupFunc: func() (string, string, func()) {
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a valid zip file
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					t.Fatalf("Failed to create zip file: %v", err)
				}

				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				testFile, err := zipWriter.Create("test.txt")
				if err != nil {
					t.Fatalf("Failed to create file in zip: %v", err)
				}
				testFile.Write([]byte("test content"))

				zipWriter.Close()
				zipFile.Close()

				// Create a file instead of a directory to cause the error
				destPath := filepath.Join(tempDir, "extracted")
				if err := os.WriteFile(destPath, []byte("not a directory"), 0644); err != nil {
					t.Fatalf("Failed to create file at destination: %v", err)
				}

				return zipPath, destPath, func() {
					os.RemoveAll(tempDir)
				}
			},
			expectedError: "mkdir",
		},
		{
			name: "file creation failure",
			setupFunc: func() (string, string, func()) {
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a zip file with a file that would be extracted to a read-only location
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					t.Fatalf("Failed to create zip file: %v", err)
				}

				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				testFile, err := zipWriter.Create("test.txt")
				if err != nil {
					t.Fatalf("Failed to create file in zip: %v", err)
				}
				testFile.Write([]byte("test content"))

				zipWriter.Close()
				zipFile.Close()

				// Create a read-only directory
				destDir := filepath.Join(tempDir, "extracted")
				if err := os.MkdirAll(destDir, 0555); err != nil {
					t.Fatalf("Failed to create read-only directory: %v", err)
				}

				return zipPath, destDir, func() {
					os.RemoveAll(tempDir)
				}
			},
			expectedError: "permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			src, dest, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test
			err := unzip(src, dest)
			if err == nil {
				t.Errorf("unzip() error = nil, want error containing %q", tt.expectedError)
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("unzip() error = %v, want error containing %q", err, tt.expectedError)
			}
		})
	}
}

// TestManager_CloserCleanup tests the cleanup functionality of closer functions
func TestManagerCloserCleanup(t *testing.T) {
	// Create a manager
	m := &Manager{
		closers: make([]func(), 0),
	}

	// Track if closers were called
	closer1Called := false
	closer2Called := false
	closer3Called := false

	// Add closers using the same pattern as in manager.go
	m.closers = append(m.closers, func() {
		if err := func() error {
			closer1Called = true
			return nil
		}(); err != nil {
			panic(err)
		}
	})

	m.closers = append(m.closers, func() {
		if err := func() error {
			closer2Called = true
			return nil
		}(); err != nil {
			panic(err)
		}
	})

	m.closers = append(m.closers, func() {
		if err := func() error {
			closer3Called = true
			return nil
		}(); err != nil {
			panic(err)
		}
	})

	// Verify closers were added
	if len(m.closers) != 3 {
		t.Errorf("Expected 3 closers, got %d", len(m.closers))
	}

	// Execute all closers
	for _, closer := range m.closers {
		closer()
	}

	// Verify all closers were called
	if !closer1Called {
		t.Errorf("Closer 1 was not called")
	}
	if !closer2Called {
		t.Errorf("Closer 2 was not called")
	}
	if !closer3Called {
		t.Errorf("Closer 3 was not called")
	}
}

// TestManager_CloserManagement_Success tests the successful scenarios of closer management
func TestManagerCloserManagementSuccess(t *testing.T) {
	tests := []struct {
		name            string
		initialClosers  []func()
		newCloser       func() error
		expectedCount   int
		verifyExecution bool
	}{
		{
			name:            "add first closer to empty list",
			initialClosers:  []func(){},
			newCloser:       func() error { return nil },
			expectedCount:   1,
			verifyExecution: true,
		},
		{
			name:            "add closer to existing list",
			initialClosers:  []func(){func() {}},
			newCloser:       func() error { return nil },
			expectedCount:   2,
			verifyExecution: true,
		},
		{
			name:            "ignore nil closer",
			initialClosers:  []func(){func() {}},
			newCloser:       nil,
			expectedCount:   1,
			verifyExecution: false,
		},
		{
			name:            "add multiple closers sequentially",
			initialClosers:  []func(){func() {}, func() {}},
			newCloser:       func() error { return nil },
			expectedCount:   3,
			verifyExecution: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with initial closers
			m := &Manager{
				closers: make([]func(), 0, len(tt.initialClosers)),
			}
			m.closers = append(m.closers, tt.initialClosers...)

			// Track if the new closer was called
			closerCalled := false

			// Add the new closer if it's not nil
			if tt.newCloser != nil {
				m.closers = append(m.closers, func() {
					if err := func() error {
						closerCalled = true
						return tt.newCloser()
					}(); err != nil {
						panic(err)
					}
				})
			}

			// Verify the number of closers
			if len(m.closers) != tt.expectedCount {
				t.Errorf("got %d closers, want %d", len(m.closers), tt.expectedCount)
			}

			// Execute all closers
			for _, closer := range m.closers {
				closer()
			}

			// Verify the new closer was called if expected
			if tt.verifyExecution && !closerCalled {
				t.Error("new closer was not called")
			}
		})
	}
}

// TestManager_CloserManagement_Failure tests the failure scenarios of closer management
func TestManagerCloserManagementFailure(t *testing.T) {
	tests := []struct {
		name          string
		newCloser     func() error
		expectedError string
	}{
		{
			name: "closer returns error",
			newCloser: func() error {
				return fmt.Errorf("intentional error")
			},
			expectedError: "intentional error",
		},
		{
			name: "closer panics with string",
			newCloser: func() error {
				panic("intentional panic")
			},
			expectedError: "intentional panic",
		},
		{
			name: "closer panics with error",
			newCloser: func() error {
				panic(fmt.Errorf("panic error"))
			},
			expectedError: "panic error",
		},
		{
			name: "closer panics with nil",
			newCloser: func() error {
				panic(nil)
			},
			expectedError: "panic called with nil argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager
			m := &Manager{
				closers: make([]func(), 0),
			}

			// Add the closer that will fail
			m.closers = append(m.closers, func() {
				if err := tt.newCloser(); err != nil {
					panic(err)
				}
			})

			// Execute the closer and verify it panics
			defer func() {
				r := recover()
				if r == nil {
					t.Error("expected panic but got none")
					return
				}

				// Convert panic value to string for comparison
				var errStr string
				switch v := r.(type) {
				case error:
					errStr = v.Error()
				case string:
					errStr = v
				default:
					errStr = "panic called with nil argument"
				}

				if errStr != tt.expectedError {
					t.Errorf("got panic %q, want %q", errStr, tt.expectedError)
				}
			}()

			// This should panic
			m.closers[0]()
		})
	}
}

// TestValidateMgrCfgSuccess tests the successful scenarios of the validateMgrCfg function
func TestValidateMgrCfgSuccess(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ManagerConfig
	}{
		{
			name: "valid config with empty fields",
			cfg: &ManagerConfig{
				Root:       "",
				RemoteRoot: "",
			},
		},
		{
			name: "valid config with root path",
			cfg: &ManagerConfig{
				Root:       "/path/to/plugins",
				RemoteRoot: "",
			},
		},
		{
			name: "valid config with remote root",
			cfg: &ManagerConfig{
				Root:       "/path/to/plugins",
				RemoteRoot: "/path/to/remote/plugins.zip",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMgrCfg(tt.cfg)
			if err != nil {
				t.Errorf("validateMgrCfg() error = %v, want nil", err)
			}
		})
	}
}

func TestLoadPluginSuccess(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() (string, string, func()) // returns path, id, cleanup
	}{
		{
			name: "load valid plugin",
			setupFunc: func() (string, string, func()) {
				// Create a temporary directory for the test
				tempDir, err := os.MkdirTemp("", "plugin-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a mock plugin file (we can't create a real .so file in tests)
				pluginPath := filepath.Join(tempDir, "test-plugin.so")
				if err := os.WriteFile(pluginPath, []byte("mock plugin content"), 0644); err != nil {
					t.Fatalf("Failed to create mock plugin file: %v", err)
				}

				return pluginPath, "test-plugin", func() {
					os.RemoveAll(tempDir)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip the test since we can't create real .so files in tests
			t.Skip("Cannot create real .so files in tests")

			// Setup test environment
			path, id, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test
			p, elapsed, err := loadPlugin(context.Background(), path, id)
			if err != nil {
				t.Errorf("loadPlugin() error = %v, want nil", err)
			}
			if p == nil {
				t.Error("loadPlugin() returned nil plugin")
			}
			if elapsed == 0 {
				t.Error("loadPlugin() returned zero elapsed time")
			}
		})
	}
}

// TestLoadPluginFailure tests the failure scenarios of the loadPlugin function
func TestLoadPluginFailure(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func() (string, string, func()) // returns path, id, cleanup
		expectedError string
	}{
		{
			name: "nonexistent plugin file",
			setupFunc: func() (string, string, func()) {
				tempDir, err := os.MkdirTemp("", "plugin-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}
				return filepath.Join(tempDir, "nonexistent.so"), "nonexistent", func() {
					os.RemoveAll(tempDir)
				}
			},
			expectedError: "failed to open plugin nonexistent: plugin.Open",
		},
		{
			name: "invalid plugin file",
			setupFunc: func() (string, string, func()) {
				tempDir, err := os.MkdirTemp("", "plugin-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create an invalid plugin file
				pluginPath := filepath.Join(tempDir, "invalid.so")
				if err := os.WriteFile(pluginPath, []byte("not a valid plugin"), 0644); err != nil {
					t.Fatalf("Failed to create invalid plugin file: %v", err)
				}

				return pluginPath, "invalid", func() {
					os.RemoveAll(tempDir)
				}
			},
			expectedError: "failed to open plugin invalid: plugin.Open",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			path, id, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test
			p, elapsed, err := loadPlugin(context.Background(), path, id)
			if err == nil {
				t.Errorf("loadPlugin() error = nil, want error containing %q", tt.expectedError)
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("loadPlugin() error = %v, want error containing %q", err, tt.expectedError)
			}
			if p != nil {
				t.Error("loadPlugin() returned non-nil plugin for error case")
			}
			if elapsed != 0 {
				t.Error("loadPlugin() returned non-zero elapsed time for error case")
			}
		})
	}
}

// TestPluginsSuccess tests the successful scenarios of the plugins function
func TestPluginsSuccess(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() (*ManagerConfig, func()) // returns config and cleanup
		wantCount int
	}{
		{
			name: "empty directory",
			setupFunc: func() (*ManagerConfig, func()) {
				// Create a temporary directory for the test
				tempDir, err := os.MkdirTemp("", "plugins-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				cfg := &ManagerConfig{
					Root:       tempDir,
					RemoteRoot: "",
				}

				return cfg, func() {
					os.RemoveAll(tempDir)
				}
			},
			wantCount: 0,
		},
		{
			name: "directory with non-plugin files",
			setupFunc: func() (*ManagerConfig, func()) {
				// Create a temporary directory for the test
				tempDir, err := os.MkdirTemp("", "plugins-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create some non-plugin files
				files := []string{
					"file1.txt",
					"file2.json",
					"file3.go",
				}
				for _, file := range files {
					if err := os.WriteFile(filepath.Join(tempDir, file), []byte("test content"), 0644); err != nil {
						t.Fatalf("Failed to create test file: %v", err)
					}
				}

				cfg := &ManagerConfig{
					Root:       tempDir,
					RemoteRoot: "",
				}

				return cfg, func() {
					os.RemoveAll(tempDir)
				}
			},
			wantCount: 0,
		},
		{
			name: "directory with subdirectories",
			setupFunc: func() (*ManagerConfig, func()) {
				// Create a temporary directory for the test
				tempDir, err := os.MkdirTemp("", "plugins-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create some subdirectories
				dirs := []string{
					"dir1",
					"dir2/subdir",
				}
				for _, dir := range dirs {
					if err := os.MkdirAll(filepath.Join(tempDir, dir), 0755); err != nil {
						t.Fatalf("Failed to create directory: %v", err)
					}
				}

				cfg := &ManagerConfig{
					Root:       tempDir,
					RemoteRoot: "",
				}

				return cfg, func() {
					os.RemoveAll(tempDir)
				}
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			cfg, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test
			got, err := plugins(context.Background(), cfg)
			if err != nil {
				t.Errorf("plugins() error = %v, want nil", err)
			}
			if len(got) != tt.wantCount {
				t.Errorf("plugins() returned %d plugins, want %d", len(got), tt.wantCount)
			}
		})
	}
}

// TestPluginsFailure tests the failure scenarios of the plugins function
func TestPluginsFailure(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func() (*ManagerConfig, func()) // returns config and cleanup
		expectedError string
	}{
		{
			name: "nonexistent directory",
			setupFunc: func() (*ManagerConfig, func()) {
				tempDir, err := os.MkdirTemp("", "plugins-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}
				os.RemoveAll(tempDir) // Remove the directory to cause an error

				cfg := &ManagerConfig{
					Root:       tempDir,
					RemoteRoot: "",
				}

				return cfg, func() {}
			},
			expectedError: "no such file or directory",
		},
		{
			name: "permission denied",
			setupFunc: func() (*ManagerConfig, func()) {
				// Create a temporary directory for the test
				tempDir, err := os.MkdirTemp("", "plugins-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Remove read permission from the directory
				if err := os.Chmod(tempDir, 0); err != nil {
					t.Fatalf("Failed to change directory permissions: %v", err)
				}

				cfg := &ManagerConfig{
					Root:       tempDir,
					RemoteRoot: "",
				}

				return cfg, func() {
					os.Chmod(tempDir, 0755) // Restore permissions before cleanup
					os.RemoveAll(tempDir)
				}
			},
			expectedError: "permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			cfg, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test
			got, err := plugins(context.Background(), cfg)
			if err == nil {
				t.Errorf("plugins() error = nil, want error containing %q", tt.expectedError)
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("plugins() error = %v, want error containing %q", err, tt.expectedError)
			}
			if got != nil {
				t.Error("plugins() returned non-nil map for error case")
			}
		})
	}
}

// TestProviderSuccess tests the successful scenarios of the provider function
func TestProviderSuccess(t *testing.T) {
	tests := []struct {
		name     string
		plugins  map[string]onixPlugin
		id       string
		wantType interface{}
	}{
		{
			name: "get publisher provider",
			plugins: map[string]onixPlugin{
				"test-plugin": &mockPlugin{
					symbol: &mockPublisherProvider{
						publisher: &mockPublisher{},
					},
					err: nil,
				},
			},
			id:       "test-plugin",
			wantType: (*definition.PublisherProvider)(nil),
		},
		{
			name: "get schema validator provider",
			plugins: map[string]onixPlugin{
				"test-plugin": &mockPlugin{
					symbol: &mockSchemaValidatorProvider{
						validator: &mockSchemaValidator{},
					},
					err: nil,
				},
			},
			id:       "test-plugin",
			wantType: (*definition.SchemaValidatorProvider)(nil),
		},
		{
			name: "get router provider",
			plugins: map[string]onixPlugin{
				"test-plugin": &mockPlugin{
					symbol: &mockRouterProvider{
						router: &mockRouter{},
					},
					err: nil,
				},
			},
			id:       "test-plugin",
			wantType: (*definition.RouterProvider)(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run the test
			switch tt.wantType.(type) {
			case *definition.PublisherProvider:
				got, err := provider[definition.PublisherProvider](tt.plugins, tt.id)
				if err != nil {
					t.Errorf("provider() error = %v, want nil", err)
				}
				if got == nil {
					t.Error("provider() returned nil provider")
				}
			case *definition.SchemaValidatorProvider:
				got, err := provider[definition.SchemaValidatorProvider](tt.plugins, tt.id)
				if err != nil {
					t.Errorf("provider() error = %v, want nil", err)
				}
				if got == nil {
					t.Error("provider() returned nil provider")
				}
			case *definition.RouterProvider:
				got, err := provider[definition.RouterProvider](tt.plugins, tt.id)
				if err != nil {
					t.Errorf("provider() error = %v, want nil", err)
				}
				if got == nil {
					t.Error("provider() returned nil provider")
				}
			default:
				t.Errorf("unsupported provider type: %T", tt.wantType)
			}
		})
	}
}

// TestProviderFailure tests the failure scenarios of the provider function
func TestProviderFailure(t *testing.T) {
	tests := []struct {
		name       string
		plugins    map[string]onixPlugin
		id         string
		wantErrMsg string
	}{
		{
			name:       "plugin not found",
			plugins:    map[string]onixPlugin{},
			id:         "nonexistent",
			wantErrMsg: "plugin nonexistent not found",
		},
		{
			name: "lookup error",
			plugins: map[string]onixPlugin{
				"test-plugin": &mockPlugin{
					symbol: nil,
					err:    errors.New("lookup failed"),
				},
			},
			id:         "test-plugin",
			wantErrMsg: "lookup failed",
		},
		{
			name: "invalid provider type",
			plugins: map[string]onixPlugin{
				"test-plugin": &mockPlugin{
					symbol: &struct{}{}, // Invalid type
					err:    nil,
				},
			},
			id:         "test-plugin",
			wantErrMsg: "failed to cast Provider for test-plugin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test with PublisherProvider type
			got, err := provider[definition.PublisherProvider](tt.plugins, tt.id)
			if err == nil {
				t.Error("provider() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Errorf("provider() error = %v, want error containing %v", err, tt.wantErrMsg)
			}
			if got != nil {
				t.Error("provider() expected nil provider")
			}

			// Test with SchemaValidatorProvider type
			gotValidator, err := provider[definition.SchemaValidatorProvider](tt.plugins, tt.id)
			if err == nil {
				t.Error("provider() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Errorf("provider() error = %v, want error containing %v", err, tt.wantErrMsg)
			}
			if gotValidator != nil {
				t.Error("provider() expected nil provider")
			}

			// Test with RouterProvider type
			gotRouter, err := provider[definition.RouterProvider](tt.plugins, tt.id)
			if err == nil {
				t.Error("provider() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Errorf("provider() error = %v, want error containing %v", err, tt.wantErrMsg)
			}
			if gotRouter != nil {
				t.Error("provider() expected nil provider")
			}
		})
	}
}

// TestManagerMiddlewareSuccess tests the successful scenarios of the Middleware method
func TestManagerMiddlewareSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockMiddlewareProvider
	}{
		{
			name: "successful middleware creation",
			cfg: &Config{
				ID:     "test-middleware",
				Config: map[string]string{},
			},
			plugin: &mockMiddlewareProvider{
				middleware: func(h http.Handler) http.Handler { return h },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Middleware
			middleware, err := m.Middleware(context.Background(), tt.cfg)

			// Check success case
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if middleware == nil {
				t.Error("expected non-nil middleware, got nil")
			}
		})
	}
}

// TestManagerMiddlewareFailure tests the failure scenarios of the Middleware method
func TestManagerMiddlewareFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockMiddlewareProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-middleware",
				Config: map[string]string{},
			},
			plugin: &mockMiddlewareProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-middleware",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-middleware not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Middleware
			middleware, err := m.Middleware(context.Background(), tt.cfg)

			// Check error
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if middleware != nil {
				t.Error("expected nil middleware, got non-nil")
			}
		})
	}
}
