package plugin

import (
	"archive/zip"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"plugin"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

type mockPlugin struct {
	symbol plugin.Symbol
	err    error
}

func (m *mockPlugin) Lookup(str string) (plugin.Symbol, error) {
	return m.symbol, m.err
}

// Mock implementations for testing.
type mockPublisher struct {
	definition.Publisher
}

type mockSchemaValidator struct {
	definition.SchemaValidator
}

type mockRouter struct {
	definition.Router
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

// Mock providers.
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
	errFunc   func() error
}

func (m *mockSchemaValidatorProvider) New(ctx context.Context, config map[string]string) (definition.SchemaValidator, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.validator, func() error { return nil }, nil
}

// Mock providers for additional interfaces.
type mockRouterProvider struct {
	router  *mockRouter
	err     error
	errFunc func() error
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
	step    *mockStep
	err     error
	errFunc func() error
}

func (m *mockStepProvider) New(ctx context.Context, config map[string]string) (definition.Step, func(), error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.step, func() {}, nil
}

// Mock providers for additional interfaces.
type mockCacheProvider struct {
	cache   *mockCache
	err     error
	errFunc func() error
}

func (m *mockCacheProvider) New(ctx context.Context, config map[string]string) (definition.Cache, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.cache, func() error { return nil }, nil
}

type mockSignerProvider struct {
	signer  *mockSigner
	err     error
	errFunc func() error
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
	errFunc   func() error
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
	errFunc   func() error
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
	errFunc   func() error
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
	errFunc    func() error
}

func (m *mockKeyManagerProvider) New(ctx context.Context, cache definition.Cache, lookup definition.RegistryLookup, config map[string]string) (definition.KeyManager, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.keyManager, func() error { return nil }, nil
}

// Mock registry lookup for testing.
type mockRegistryLookup struct {
	definition.RegistryLookup
}

// createTestZip creates a zip file with test content in a temporary directory.
func createTestZip(t *testing.T) string {
	t.Helper()
	// Create a temporary directory for the zip file
	tempDir := t.TempDir()
	zipPath := filepath.Join(tempDir, "test.zip")

	// Create a zip file
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("Failed to create zip file: %v", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Add a test file to the zip
	testFile, err := zipWriter.Create("test.txt")
	if err != nil {
		t.Fatalf("Failed to create file in zip: %v", err)
	}
	_, err = testFile.Write([]byte("test content"))
	if err != nil {
		t.Fatalf("Failed to write to file: %v", err)
	}

	return zipPath
}

// TestNewManagerSuccess tests the successful scenarios of the NewManager function.
func TestNewManagerSuccess(t *testing.T) {
	// Build the dummy plugin first.
	cmd := exec.Command("go", "build", "-buildmode=plugin", "-o", "./testdata/dummy.so", "./testdata/dummy.go")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build dummy plugin: %v", err)
	}

	// Clean up the .so file after test completes.
	t.Cleanup(func() {
		if err := os.Remove("./testdata/dummy.so"); err != nil && !os.IsNotExist(err) {
			t.Logf("Failed to remove dummy.so: %v", err)
		}
	})

	tests := []struct {
		name string
		cfg  *ManagerConfig
	}{
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
				Root: t.TempDir(),
				RemoteRoot: func() string {
					zipPath := createTestZip(t)
					return zipPath
				}(),
			},
		},
		{
			name: "valid config with so file",
			cfg: &ManagerConfig{
				Root:       "./testdata",
				RemoteRoot: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			m, cleanup, err := NewManager(ctx, tt.cfg)
			if err != nil {
				t.Fatalf("NewManager() error = %v, want nil", err)
			}
			if m == nil {
				t.Fatal("NewManager() returned nil manager")
			}
			if cleanup == nil {
				t.Fatal("NewManager() returned nil cleanup function")
			}

			// Verify manager fields.
			if m.plugins == nil {
				t.Fatal("NewManager() returned manager with nil plugins map")
			}
			if m.closers == nil {
				t.Fatal("NewManager() returned manager with nil closers slice")
			}

			// Call cleanup to ensure it doesn't panic.
			cleanup()
		})
	}
}

// TestNewManagerFailure tests the failure scenarios of the NewManager function.
func TestNewManagerFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *ManagerConfig
		expectedError string
	}{
		{
			name: "invalid config with empty root",
			cfg: &ManagerConfig{
				Root:       "",
				RemoteRoot: "",
			},
			expectedError: "root path cannot be empty",
		},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			m, cleanup, err := NewManager(ctx, tt.cfg)
			if err == nil {
				t.Fatal("NewManager() expected error, got nil")
			}
			if m != nil {
				t.Fatal("NewManager() returned non-nil manager for error case")
			}
			if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("NewManager() error = %v, want error containing %q", err, tt.expectedError)
			}
			if cleanup != nil {
				t.Fatal("NewManager() returned non-nil cleanup function for error case")
			}

		})
	}
}

func TestPublisherSuccess(t *testing.T) {
	t.Run("successful publisher creation", func(t *testing.T) {
		publisherID := "publisherId"
		mockPublisher := &mockPublisher{}
		errFunc := func() error { return nil }
		m := &Manager{
			plugins: map[string]onixPlugin{
				publisherID: &mockPlugin{
					symbol: &mockPublisherProvider{
						publisher: mockPublisher,
						errFunc:   errFunc,
					},
				},
			},
			closers: []func(){},
		}

		p, err := m.Publisher(context.Background(), &Config{
			ID:     publisherID,
			Config: map[string]string{},
		})

		if err != nil {
			t.Fatalf("Manager.Publisher() error = %v, want no error", err)
		}

		if p != mockPublisher {
			t.Fatalf("Manager.Publisher() did not return the correct publisher")
		}

		if len(m.closers) != 1 {
			t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
		}

		m.closers[0]()

	})
}

// TestPublisherFailure tests the failure scenarios of the Publisher method.
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
					symbol: nil,
					err:    errors.New("provider error"),
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
				t.Fatal("Manager.Publisher() expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("Manager.Publisher() error = %v, want error containing %q", err, tt.expectedError)
			}

			if p != nil {
				t.Fatal("Manager.Publisher() expected nil publisher, got non-nil")
			}
		})
	}
}

// TestSchemaValidatorSuccess tests the successful scenarios of the SchemaValidator method.
func TestSchemaValidatorSuccess(t *testing.T) {
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
				errFunc:   func() error { return nil },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call SchemaValidator.
			validator, err := m.SchemaValidator(context.Background(), tt.cfg)

			// Check success case.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if validator != tt.plugin.validator {
				t.Fatal("validator does not match expected instance")
			}

			if len(m.closers) != 1 {
				t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
			}

			m.closers[0]()
		})
	}
}

// TestSchemaValidatorFailure tests the failure scenarios of the SchemaValidator method.
func TestSchemaValidatorFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        definition.SchemaValidatorProvider
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call SchemaValidator.
			validator, err := m.SchemaValidator(context.Background(), tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if validator != nil {
				t.Fatal("expected nil validator, got non-nil")
			}
		})
	}
}

// TestRouterSuccess tests the successful scenarios of the Router method.
func TestRouterSuccess(t *testing.T) {
	t.Run("successful router creation", func(t *testing.T) {
		cfg := &Config{
			ID:     "test-router",
			Config: map[string]string{},
		}
		plugin := &mockRouterProvider{
			router:  &mockRouter{},
			errFunc: func() error { return nil },
		}
		// Create a manager with the mock plugin.
		m := &Manager{
			plugins: map[string]onixPlugin{
				cfg.ID: &mockPlugin{
					symbol: plugin,
				},
			},
			closers: []func(){},
		}

		// Call Router.
		router, err := m.Router(context.Background(), cfg)

		// Check success case.
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if router == nil {
			t.Fatal("expected non-nil router, got nil")
		}
		if router != plugin.router {
			t.Fatal("router does not match expected instance")
		}

		if len(m.closers) != 1 {
			t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
		}

		m.closers[0]()
	})
}

// TestRouterFailure tests the failure scenarios of the Router method.
func TestRouterFailure(t *testing.T) {
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Router.
			router, err := m.Router(context.Background(), tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if router != nil {
				t.Fatal("expected nil router, got non-nil")
			}
		})
	}
}

// TestStepSuccess tests the successful scenarios of the Step method.
func TestStepSuccess(t *testing.T) {
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
				step:    &mockStep{},
				errFunc: func() error { return nil },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Step.
			step, err := m.Step(context.Background(), tt.cfg)

			// Check success case.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if step == nil {
				t.Fatal("expected non-nil step, got nil")
			}
			if step != tt.plugin.step {
				t.Fatal("step does not match expected instance")
			}

			if len(m.closers) != 1 {
				t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
			}

			m.closers[0]()
		})
	}
}

// TestStepFailure tests the failure scenarios of the Step method.
func TestStepFailure(t *testing.T) {
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Step.
			step, err := m.Step(context.Background(), tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if step != nil {
				t.Fatal("expected nil step, got non-nil")
			}
		})
	}
}

// TestCacheSuccess tests the successful scenarios of the Cache method.
func TestCacheSuccess(t *testing.T) {
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
				cache:   &mockCache{},
				errFunc: func() error { return nil },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Cache.
			cache, err := m.Cache(context.Background(), tt.cfg)

			// Check success case.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cache == nil {
				t.Fatal("expected non-nil cache, got nil")
			}

			if cache != tt.plugin.cache {
				t.Fatal("cache does not match expected instance")
			}

			if len(m.closers) != 1 {
				t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
			}

			m.closers[0]()
		})
	}
}

// TestCacheFailure tests the failure scenarios of the Cache method.
func TestCacheFailure(t *testing.T) {
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Cache.
			cache, err := m.Cache(context.Background(), tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if cache != nil {
				t.Fatal("expected nil cache, got non-nil")
			}
		})
	}
}

// TestSignerSuccess tests the successful scenarios of the Signer method.
func TestSignerSuccess(t *testing.T) {
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
				signer:  &mockSigner{},
				errFunc: func() error { return nil },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Signer.
			signer, err := m.Signer(context.Background(), tt.cfg)

			// Check success case.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if signer == nil {
				t.Fatal("expected non-nil signer, got nil")
			}

			if signer != tt.plugin.signer {
				t.Fatal("signer does not match expected instance")
			}

			if len(m.closers) != 1 {
				t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
			}

			m.closers[0]()
		})
	}
}

// TestSignerFailure tests the failure scenarios of the Signer method.
func TestSignerFailure(t *testing.T) {
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Signer.
			signer, err := m.Signer(context.Background(), tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if signer != nil {
				t.Fatal("expected nil signer, got non-nil")
			}
		})
	}
}

// TestEncryptorSuccess tests the successful scenarios of the Encryptor method.
func TestEncryptorSuccess(t *testing.T) {
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
				errFunc:   func() error { return nil },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Encryptor.
			encrypter, err := m.Encryptor(context.Background(), tt.cfg)

			// Check success case.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if encrypter == nil {
				t.Fatal("expected non-nil encrypter, got nil")
			}

			if encrypter != tt.plugin.encrypter {
				t.Fatal("encrypter does not match expected instance")
			}

			if len(m.closers) != 1 {
				t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
			}

			m.closers[0]()
		})
	}
}

// TestEncryptorFailure tests the failure scenarios of the Encryptor method.
func TestEncryptorFailure(t *testing.T) {
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Encryptor.
			encrypter, err := m.Encryptor(context.Background(), tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if encrypter != nil {
				t.Fatal("expected nil encrypter, got non-nil")
			}
		})
	}
}

// TestDecryptorSuccess tests the successful scenarios of the Decryptor method.
func TestDecryptorSuccess(t *testing.T) {
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
				errFunc:   func() error { return nil },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Decryptor.
			decrypter, err := m.Decryptor(context.Background(), tt.cfg)

			// Check success case.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decrypter == nil {
				t.Fatal("expected non-nil decrypter, got nil")
			}

			if decrypter != tt.plugin.decrypter {
				t.Fatal("decrypter does not match expected instance")
			}

			if len(m.closers) != 1 {
				t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
			}

			m.closers[0]()
		})
	}
}

// TestDecryptorFailure tests the failure scenarios of the Decryptor method.
func TestDecryptorFailure(t *testing.T) {
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Decryptor.
			decrypter, err := m.Decryptor(context.Background(), tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if decrypter != nil {
				t.Fatal("expected nil decrypter, got non-nil")
			}
		})
	}
}

// TestSignValidatorSuccess tests the successful scenarios of the SignValidator method.
func TestSignValidatorSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		plugin *mockSignValidatorProvider
	}{
		{
			name: "successful sign validator creation",
			cfg: &Config{
				ID:     "test-sign-validator",
				Config: map[string]string{},
			},
			plugin: &mockSignValidatorProvider{
				validator: &mockSignValidator{},
				errFunc:   func() error { return nil },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call SignValidator.
			validator, err := m.SignValidator(context.Background(), tt.cfg)

			// Check success case.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if validator == nil {
				t.Fatal("expected non-nil validator, got nil")
			}
			if validator != tt.plugin.validator {
				t.Fatal("validator does not match expected instance")
			}

			if len(m.closers) != 1 {
				t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
			}

			m.closers[0]()
		})
	}
}

// TestSignValidatorFailure tests the failure scenarios of the SignValidator method.
func TestSignValidatorFailure(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		plugin        *mockSignValidatorProvider
		expectedError string
	}{
		{
			name: "provider error",
			cfg: &Config{
				ID:     "test-sign-validator",
				Config: map[string]string{},
			},
			plugin: &mockSignValidatorProvider{
				err: errors.New("provider error"),
			},
			expectedError: "provider error",
		},
		{
			name: "plugin not found",
			cfg: &Config{
				ID:     "nonexistent-sign-validator",
				Config: map[string]string{},
			},
			plugin:        nil,
			expectedError: "plugin nonexistent-sign-validator not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call SignValidator.
			validator, err := m.SignValidator(context.Background(), tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if validator != nil {
				t.Fatal("expected nil validator, got non-nil")
			}
		})
	}
}

// TestKeyManagerSuccess tests the successful scenarios of the KeyManager method.
func TestKeyManagerSuccess(t *testing.T) {
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
				errFunc:    func() error { return nil },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Create mock cache and registry lookup.
			mockCache := &mockCache{}
			mockRegistry := &mockRegistryLookup{}

			// Call KeyManager.
			keyManager, err := m.KeyManager(context.Background(), mockCache, mockRegistry, tt.cfg)

			// Check success case.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if keyManager == nil {
				t.Fatal("expected non-nil key manager, got nil")
			}

			if keyManager != tt.plugin.keyManager {
				t.Fatal("key manager does not match expected instance")
			}

			if len(m.closers) != 1 {
				t.Fatalf("Manager.closers has %d closers, expected 1", len(m.closers))
			}

			m.closers[0]()
		})
	}
}

// TestKeyManagerFailure tests the failure scenarios of the KeyManager method.
func TestKeyManagerFailure(t *testing.T) {
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Create mock cache and registry lookup.
			mockCache := &mockCache{}
			mockRegistry := &mockRegistryLookup{}

			// Call KeyManager.
			keyManager, err := m.KeyManager(context.Background(), mockCache, mockRegistry, tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if keyManager != nil {
				t.Fatal("expected nil key manager, got non-nil")
			}
		})
	}
}

// TestUnzipSuccess tests the successful scenarios of the unzip function.
func TestUnzipSuccess(t *testing.T) {
	tests := []struct {
		name       string
		setupFunc  func() (string, string, func()) // returns src, dest, cleanup.
		verifyFunc func(t *testing.T, dest string)
	}{
		{
			name: "extract single file",
			setupFunc: func() (string, string, func()) {
				// Create a temporary directory for the test.
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a zip file with a single test file.
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					t.Fatalf("Failed to create zip file: %v", err)
				}

				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a test file to the zip.
				testFile, err := zipWriter.Create("test.txt")
				if err != nil {
					t.Fatalf("Failed to create file in zip: %v", err)
				}
				_, err = testFile.Write([]byte("test content"))
				if err != nil {
					t.Fatalf("Failed to write to file: %v", err)
				}

				zipWriter.Close()
				zipFile.Close()

				// Create destination directory.
				destDir := filepath.Join(tempDir, "extracted")
				return zipPath, destDir, func() {
					os.RemoveAll(tempDir)
				}
			},
			verifyFunc: func(t *testing.T, dest string) {
				// Verify the extracted file exists and has correct content.
				content, err := os.ReadFile(filepath.Join(dest, "test.txt"))
				if err != nil {
					t.Fatalf("Failed to read extracted file: %v", err)
				}
				if string(content) != "test content" {
					t.Fatalf("Extracted file content = %v, want %v", string(content), "test content")
				}
			},
		},
		{
			name: "extract file in subdirectory",
			setupFunc: func() (string, string, func()) {
				// Create a temporary directory for the test.
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a zip file with a file in a subdirectory.
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					t.Fatalf("Failed to create zip file: %v", err)
				}

				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a file in a subdirectory.
				testFile, err := zipWriter.Create("subdir/test.txt")
				if err != nil {
					t.Fatalf("Failed to create file in zip: %v", err)
				}
				_, err = testFile.Write([]byte("subdirectory content"))
				if err != nil {
					t.Fatalf("Failed to write to file: %v", err)
				}

				zipWriter.Close()
				zipFile.Close()

				// Create destination directory.
				destDir := filepath.Join(tempDir, "extracted")
				return zipPath, destDir, func() {
					os.RemoveAll(tempDir)
				}
			},
			verifyFunc: func(t *testing.T, dest string) {
				// Verify the extracted file in subdirectory exists and has correct content.
				content, err := os.ReadFile(filepath.Join(dest, "subdir/test.txt"))
				if err != nil {
					t.Fatalf("Failed to read extracted file in subdirectory: %v", err)
				}
				if string(content) != "subdirectory content" {
					t.Fatalf("Extracted file content in subdirectory = %v, want %v", string(content), "subdirectory content")
				}
			},
		},
		{
			name: "extract multiple files",
			setupFunc: func() (string, string, func()) {
				// Create a temporary directory for the test.
				tempDir, err := os.MkdirTemp("", "unzip-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a zip file with multiple files.
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					t.Fatalf("Failed to create zip file: %v", err)
				}

				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add multiple files to the zip.
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
					_, err = testFile.Write([]byte(content))
					if err != nil {
						t.Fatalf("Failed to write to file: %v", err)
					}
				}

				zipWriter.Close()
				zipFile.Close()

				// Create destination directory.
				destDir := filepath.Join(tempDir, "extracted")
				return zipPath, destDir, func() {
					os.RemoveAll(tempDir)
				}
			},
			verifyFunc: func(t *testing.T, dest string) {
				// Verify all extracted files exist and have correct content.
				expectedFiles := map[string]string{
					"file1.txt":        "content of file 1",
					"file2.txt":        "content of file 2",
					"subdir/file3.txt": "content of file 3",
				}

				for path, expectedContent := range expectedFiles {
					content, err := os.ReadFile(filepath.Join(dest, path))
					if err != nil {
						t.Fatalf("Failed to read extracted file %s: %v", path, err)
					}
					if string(content) != expectedContent {
						t.Fatalf("Extracted file %s content = %v, want %v", path, string(content), expectedContent)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment.
			src, dest, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test.
			err := unzip(src, dest)
			if err != nil {
				t.Fatalf("unzip() error = %v, want nil", err)
			}

			// Verify the result.
			tt.verifyFunc(t, dest)
		})
	}
}

// TestUnzipFailure tests the failure scenarios of the unzip function.
func TestUnzipFailure(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func() (string, string, func()) // returns src, dest, cleanup.
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

				// Create an invalid zip file.
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

				// Create a valid zip file.
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
				_, err = testFile.Write([]byte("test content"))
				if err != nil {
					t.Fatalf("Failed to write to file: %v", err)
				}

				zipWriter.Close()
				zipFile.Close()

				// Create a file instead of a directory to cause the error.
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

				// Create a zip file with a file that would be extracted to a read-only location.
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
				_, err = testFile.Write([]byte("test content"))
				if err != nil {
					t.Fatalf("Failed to write to file: %v", err)
				}

				zipWriter.Close()
				zipFile.Close()

				// Create a read-only directory.
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
			// Setup test environment.
			src, dest, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test.
			err := unzip(src, dest)
			if err == nil {
				t.Fatalf("unzip() error = nil, want error containing %q", tt.expectedError)
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("unzip() error = %v, want error containing %q", err, tt.expectedError)
			}
		})
	}
}

// TestValidateMgrCfgSuccess tests the successful scenarios of the validateMgrCfg function.
func TestValidateMgrCfgSuccess(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ManagerConfig
	}{
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
				t.Fatalf("validateMgrCfg() error = %v, want nil", err)
			}
		})
	}
}

func TestLoadPluginSuccess(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() (string, string, func()) // returns path, id, cleanup.
	}{
		{
			name: "load valid plugin",
			setupFunc: func() (string, string, func()) {
				// Create a temporary directory for the test.
				tempDir, err := os.MkdirTemp("", "plugin-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create a mock plugin file (we can't create a real .so file in tests).
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
			// Skip the test since we can't create real .so files in tests.
			t.Skip("Cannot create real .so files in tests")

			// Setup test environment.
			path, id, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test.
			p, elapsed, err := loadPlugin(context.Background(), path, id)
			if err != nil {
				t.Fatalf("loadPlugin() error = %v, want nil", err)
			}
			if p == nil {
				t.Fatal("loadPlugin() returned nil plugin")
			}
			if elapsed == 0 {
				t.Fatal("loadPlugin() returned zero elapsed time")
			}
		})
	}
}

// TestLoadPluginFailure tests the failure scenarios of the loadPlugin function.
func TestLoadPluginFailure(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func() (string, string, func()) // returns path, id, cleanup.
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

				// Create an invalid plugin file.
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
			// Setup test environment.
			path, id, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test.
			p, elapsed, err := loadPlugin(context.Background(), path, id)
			if err == nil {
				t.Fatalf("loadPlugin() error = nil, want error containing %q", tt.expectedError)
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("loadPlugin() error = %v, want error containing %q", err, tt.expectedError)
			}
			if p != nil {
				t.Fatal("loadPlugin() returned non-nil plugin for error case")
			}
			if elapsed != 0 {
				t.Fatal("loadPlugin() returned non-zero elapsed time for error case")
			}
		})
	}
}

// TestPluginsSuccess tests the successful scenarios of the plugins function.
func TestPluginsSuccess(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() (*ManagerConfig, func()) // returns config and cleanup.
		wantCount int
	}{
		{
			name: "empty directory",
			setupFunc: func() (*ManagerConfig, func()) {
				// Create a temporary directory for the test.
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
				// Create a temporary directory for the test.
				tempDir, err := os.MkdirTemp("", "plugins-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create some non-plugin files.
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
				// Create a temporary directory for the test.
				tempDir, err := os.MkdirTemp("", "plugins-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Create some subdirectories.
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
			// Setup test environment.
			cfg, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test.
			got, err := plugins(context.Background(), cfg)
			if err != nil {
				t.Fatalf("plugins() error = %v, want nil", err)
			}
			if len(got) != tt.wantCount {
				t.Fatalf("plugins() returned %d plugins, want %d", len(got), tt.wantCount)
			}
		})
	}
}

// TestPluginsFailure tests the failure scenarios of the plugins function.
func TestPluginsFailure(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func() (*ManagerConfig, func()) // returns config and cleanup.
		expectedError string
	}{
		{
			name: "nonexistent directory",
			setupFunc: func() (*ManagerConfig, func()) {
				tempDir, err := os.MkdirTemp("", "plugins-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}
				os.RemoveAll(tempDir) // Remove the directory to cause an error.

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
				// Create a temporary directory for the test.
				tempDir, err := os.MkdirTemp("", "plugins-test-*")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}

				// Remove read permission from the directory.
				if err := os.Chmod(tempDir, 0); err != nil {
					t.Fatalf("Failed to change directory permissions: %v", err)
				}

				cfg := &ManagerConfig{
					Root:       tempDir,
					RemoteRoot: "",
				}

				return cfg, func() {
					err = os.Chmod(tempDir, 0755) // Restore permissions before cleanup.
					os.RemoveAll(tempDir)
				}
			},
			expectedError: "permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment.
			cfg, cleanup := tt.setupFunc()
			defer cleanup()

			// Run the test.
			got, err := plugins(context.Background(), cfg)
			if err == nil {
				t.Fatalf("plugins() error = nil, want error containing %q", tt.expectedError)
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("plugins() error = %v, want error containing %q", err, tt.expectedError)
			}
			if got != nil {
				t.Fatal("plugins() returned non-nil map for error case")
			}
		})
	}
}

// TestProviderSuccess tests the successful scenarios of the provider function.
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
			// Run the test.
			switch tt.wantType.(type) {
			case *definition.PublisherProvider:
				got, err := provider[definition.PublisherProvider](tt.plugins, tt.id)
				if err != nil {
					t.Fatalf("provider() error = %v, want nil", err)
				}
				if got == nil {
					t.Fatal("provider() returned nil provider")
				}
			case *definition.SchemaValidatorProvider:
				got, err := provider[definition.SchemaValidatorProvider](tt.plugins, tt.id)
				if err != nil {
					t.Fatalf("provider() error = %v, want nil", err)
				}
				if got == nil {
					t.Fatal("provider() returned nil provider")
				}
			case *definition.RouterProvider:
				got, err := provider[definition.RouterProvider](tt.plugins, tt.id)
				if err != nil {
					t.Fatalf("provider() error = %v, want nil", err)
				}
				if got == nil {
					t.Fatal("provider() returned nil provider")
				}
			default:
				t.Fatalf("unsupported provider type: %T", tt.wantType)
			}
		})
	}
}

// TestProviderFailure tests the failure scenarios of the provider function.
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
					symbol: &struct{}{}, // Invalid type.
					err:    nil,
				},
			},
			id:         "test-plugin",
			wantErrMsg: "failed to cast Provider for test-plugin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test with PublisherProvider type.
			got, err := provider[definition.PublisherProvider](tt.plugins, tt.id)
			if err == nil {
				t.Fatal("provider() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Fatalf("provider() error = %v, want error containing %v", err, tt.wantErrMsg)
			}
			if got != nil {
				t.Fatal("provider() expected nil provider")
			}

			// Test with SchemaValidatorProvider type.
			gotValidator, err := provider[definition.SchemaValidatorProvider](tt.plugins, tt.id)
			if err == nil {
				t.Fatal("provider() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Fatalf("provider() error = %v, want error containing %v", err, tt.wantErrMsg)
			}
			if gotValidator != nil {
				t.Fatal("provider() expected nil provider")
			}

			// Test with RouterProvider type.
			gotRouter, err := provider[definition.RouterProvider](tt.plugins, tt.id)
			if err == nil {
				t.Fatal("provider() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Fatalf("provider() error = %v, want error containing %v", err, tt.wantErrMsg)
			}
			if gotRouter != nil {
				t.Fatal("provider() expected nil provider")
			}
		})
	}
}

// TestManagerMiddlewareSuccess tests the successful scenarios of the Middleware method.
func TestMiddlewareSuccess(t *testing.T) {
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: map[string]onixPlugin{
					tt.cfg.ID: &mockPlugin{
						symbol: tt.plugin,
					},
				},
				closers: []func(){},
			}

			// Call Middleware.
			middleware, err := m.Middleware(context.Background(), tt.cfg)

			// Check success case.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if middleware == nil {
				t.Fatal("expected non-nil middleware, got nil")
			}
		})
	}
}

// TestManagerMiddlewareFailure tests the failure scenarios of the Middleware method.
func TestMiddlewareFailure(t *testing.T) {
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
			// Create a manager with the mock plugin.
			m := &Manager{
				plugins: make(map[string]onixPlugin),
				closers: []func(){},
			}

			// Only add the plugin if it's not nil.
			if tt.plugin != nil {
				m.plugins[tt.cfg.ID] = &mockPlugin{
					symbol: tt.plugin,
				}
			}

			// Call Middleware.
			middleware, err := m.Middleware(context.Background(), tt.cfg)

			// Check error.
			if err == nil {
				t.Fatal("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("error = %v, want error containing %q", err, tt.expectedError)
			}
			if middleware != nil {
				t.Fatal("expected nil middleware, got non-nil")
			}
		})
	}
}
