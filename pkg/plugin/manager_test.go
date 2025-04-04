package plugin

import (
	"archive/zip"
	"context"
	// "errors"
	// "fmt"
	"os"
	"path/filepath"
	"plugin"
	"testing"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

type mockPlugin struct {
	symbol plugin.Symbol
	err error
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
	provider *mockMiddleware
	err      error
}

func (m *mockMiddlewareProvider) New(ctx context.Context, config map[string]string) (definition.MiddlewareProvider, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.provider, func() error { return nil }, nil
}

type mockStepProvider struct {
	step *mockStep
	err  error
}

func (m *mockStepProvider) New(ctx context.Context, config map[string]string) (definition.Step, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.step, func() error { return nil }, nil
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
	manager *mockKeyManager
	err     error
}

func (m *mockKeyManagerProvider) New(ctx context.Context, config map[string]string) (definition.KeyManager, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.manager, func() error { return nil }, nil
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

// func newTestManager() *testManager {
// 	// Create a temporary directory for testing
// 	tmpDir, err := os.MkdirTemp("", "plugin-test")
// 	if err != nil {
// 		panic(fmt.Sprintf("failed to create temp dir: %v", err))
// 	}

// 	// Create a test manager with the temporary directory
// 	ctx := context.Background()
// 	cfg := &ManagerConfig{
// 		Root:       tmpDir,
// 		RemoteRoot: "",
// 	}
// 	m, cleanup, err := NewManager(ctx, cfg)
// 	if err != nil {
// 		panic(fmt.Sprintf("failed to create manager: %v", err))
// 	}

// 	tm := &testManager{
// 		Manager:       m,
// 		mockProviders: make(map[string]interface{}),
// 		cleanup:       cleanup,
// 	}

// 	// Initialize the plugins map
// 	m.plugins = make(map[string]*plugin.Plugin)

// 	return tm
// }

// Test cases
func TestNewManager(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ManagerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: &ManagerConfig{
				Root:       t.TempDir(),
				RemoteRoot: "",
			},
			wantErr: false,
		},
		{
			name: "invalid config",
			cfg: &ManagerConfig{
				Root:       "/nonexistent/dir",
				RemoteRoot: "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, cleanup, err := NewManager(ctx, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewManager() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if cleanup != nil {
				cleanup()
			}
		})
	}
}

func TestPublisherSuccess(t *testing.T) {
	publisherID := "publisherId"
	errFunc := func() error { return nil}
	mockPublisher := &mockPublisher{} 
	m := &Manager{
		closers: []func(){},
		plugins: map[string]onixPlugin{
			publisherID: &mockPlugin{
				symbol : &mockPublisherProvider{
					publisher: mockPublisher,
					err: errFunc(),
				},
			},
	}}
		p, err := m.Publisher(context.Background(), &Config{
			ID:     publisherID,
			Config: map[string]string{},
		}); 
		if(err != nil) {
			t.Errorf("Manager.Publisher() error = %v", err)
		}
		// if(len(m.closers) == 0) {
		// 	t.Errorf("Manager.Publisher() did not register closer")
		// }
		if p != mockPublisher {
			t.Errorf("Manager.Publisher() did not return the correct publisher")
		}
}

// func TestManager_SchemaValidator(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockSchemaValidatorProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful validator creation",
// 			cfg: &Config{
// 				ID:     "test-validator",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockSchemaValidatorProvider{
// 				validator: &mockSchemaValidator{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-validator",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockSchemaValidatorProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			// Create a mock plugin that returns our provider
// 			mockPlugin := &plugin.Plugin{}
// 			tm.plugins[tt.cfg.ID] = mockPlugin

// 			_, err := tm.SchemaValidator(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.SchemaValidator() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_Router(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockRouterProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful router creation",
// 			cfg: &Config{
// 				ID:     "test-router",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockRouterProvider{
// 				router: &mockRouter{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-router",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockRouterProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			_, err := tm.Router(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.Router() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_Middleware(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockMiddlewareProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful middleware creation",
// 			cfg: &Config{
// 				ID:     "test-middleware",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockMiddlewareProvider{
// 				provider: &mockMiddleware{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-middleware",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockMiddlewareProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			_, err := tm.Middleware(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.Middleware() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_Step(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockStepProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful step creation",
// 			cfg: &Config{
// 				ID:     "test-step",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockStepProvider{
// 				step: &mockStep{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-step",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockStepProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			_, err := tm.Step(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.Step() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_Validator(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		wantErr bool
// 	}{
// 		{
// 			name: "unimplemented validator",
// 			cfg: &Config{
// 				ID:     "test-validator",
// 				Config: nil,
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			m := &Manager{
// 				plugins: make(map[string]*plugin.Plugin),
// 				closers: []func(){},
// 			}

// 			// We expect a panic from the unimplemented method
// 			defer func() {
// 				if r := recover(); r == nil {
// 					t.Error("Expected panic from unimplemented Validator method")
// 				}
// 			}()

// 			_, err := m.Validator(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.Validator() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_Cache(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockCacheProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful cache creation",
// 			cfg: &Config{
// 				ID:     "test-cache",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockCacheProvider{
// 				cache: &mockCache{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-cache",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockCacheProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			_, err := tm.Cache(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.Cache() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_Signer(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockSignerProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful signer creation",
// 			cfg: &Config{
// 				ID:     "test-signer",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockSignerProvider{
// 				signer: &mockSigner{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-signer",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockSignerProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			_, err := tm.Signer(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.Signer() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_Encryptor(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockEncrypterProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful encrypter creation",
// 			cfg: &Config{
// 				ID:     "test-encrypter",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockEncrypterProvider{
// 				encrypter: &mockEncrypter{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-encrypter",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockEncrypterProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			_, err := tm.Encryptor(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.Encryptor() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_Decryptor(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockDecrypterProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful decrypter creation",
// 			cfg: &Config{
// 				ID:     "test-decrypter",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockDecrypterProvider{
// 				decrypter: &mockDecrypter{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-decrypter",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockDecrypterProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			_, err := tm.Decryptor(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.Decryptor() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_SignValidator(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockSignValidatorProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful sign validator creation",
// 			cfg: &Config{
// 				ID:     "test-sign-validator",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockSignValidatorProvider{
// 				validator: &mockSignValidator{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-sign-validator",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockSignValidatorProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			_, err := tm.SignValidator(ctx, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.SignValidator() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

// func TestManager_KeyManager(t *testing.T) {
// 	tests := []struct {
// 		name    string
// 		cfg     *Config
// 		plugin  *mockKeyManagerProvider
// 		wantErr bool
// 	}{
// 		{
// 			name: "successful key manager creation",
// 			cfg: &Config{
// 				ID:     "test-key-manager",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockKeyManagerProvider{
// 				manager: &mockKeyManager{},
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "provider error",
// 			cfg: &Config{
// 				ID:     "test-key-manager",
// 				Config: map[string]string{},
// 			},
// 			plugin: &mockKeyManagerProvider{
// 				err: errors.New("provider error"),
// 			},
// 			wantErr: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctx := context.Background()
// 			tm := newTestManager()
// 			tm.mockProviders[tt.cfg.ID] = tt.plugin

// 			// Create mock cache and registry lookup
// 			mockCache := &mockCache{}
// 			mockRegistry := &mockRegistryLookup{}

// 			_, err := tm.KeyManager(ctx, mockCache, mockRegistry, tt.cfg)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Manager.KeyManager() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }

func TestUnzip(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		dest    string
		wantErr bool
		setup   func() (string, string, func())
	}{
		{
			name: "successful unzip",
			setup: func() (string, string, func()) {
				// Create a temporary directory for the test
				tmpDir, err := os.MkdirTemp("", "unzip-test")
				if err != nil {
					t.Fatalf("failed to create temp dir: %v", err)
				}

				// Create a temporary zip file
				zipFile := filepath.Join(tmpDir, "test.zip")
				f, err := os.Create(zipFile)
				if err != nil {
					t.Fatalf("failed to create zip file: %v", err)
				}
				defer f.Close()

				// Create a zip writer
				zipWriter := zip.NewWriter(f)
				defer zipWriter.Close()

				// Add a file to the zip
				fileWriter, err := zipWriter.Create("test.txt")
				if err != nil {
					t.Fatalf("failed to create file in zip: %v", err)
				}
				_, err = fileWriter.Write([]byte("test content"))
				if err != nil {
					t.Fatalf("failed to write to zip file: %v", err)
				}

				// Create a destination directory
				destDir := filepath.Join(tmpDir, "dest")
				if err := os.MkdirAll(destDir, 0755); err != nil {
					t.Fatalf("failed to create dest dir: %v", err)
				}

				return zipFile, destDir, func() {
					os.RemoveAll(tmpDir)
				}
			},
			wantErr: false,
		},
		{
			name: "nonexistent source file",
			setup: func() (string, string, func()) {
				tmpDir, err := os.MkdirTemp("", "unzip-test")
				if err != nil {
					t.Fatalf("failed to create temp dir: %v", err)
				}
				destDir := filepath.Join(tmpDir, "dest")
				if err := os.MkdirAll(destDir, 0755); err != nil {
					t.Fatalf("failed to create dest dir: %v", err)
				}
				return "nonexistent.zip", destDir, func() {
					os.RemoveAll(tmpDir)
				}
			},
			wantErr: true,
		},
		{
			name: "invalid zip file",
			setup: func() (string, string, func()) {
				tmpDir, err := os.MkdirTemp("", "unzip-test")
				if err != nil {
					t.Fatalf("failed to create temp dir: %v", err)
				}

				// Create an invalid zip file
				zipFile := filepath.Join(tmpDir, "test.zip")
				f, err := os.Create(zipFile)
				if err != nil {
					t.Fatalf("failed to create zip file: %v", err)
				}
				defer f.Close()

				// Write invalid content
				_, err = f.Write([]byte("invalid zip content"))
				if err != nil {
					t.Fatalf("failed to write to zip file: %v", err)
				}

				// Create a destination directory
				destDir := filepath.Join(tmpDir, "dest")
				if err := os.MkdirAll(destDir, 0755); err != nil {
					t.Fatalf("failed to create dest dir: %v", err)
				}

				return zipFile, destDir, func() {
					os.RemoveAll(tmpDir)
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, dest, cleanup := tt.setup()
			defer cleanup()

			err := unzip(src, dest)
			if (err != nil) != tt.wantErr {
				t.Errorf("unzip() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				// Verify that the file was extracted
				extractedFile := filepath.Join(dest, "test.txt")
				if _, err := os.Stat(extractedFile); os.IsNotExist(err) {
					t.Errorf("unzip() did not extract file to %s", extractedFile)
				}
			}
		})
	}
}
