package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"plugin"
	"strings"
	"testing"
	"time"

	"archive/zip"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations of plugin interfaces
type MockSigner struct{}

func (m *MockSigner) Sign(ctx context.Context, body []byte, privateKeyBase64 string, createdAt, expiresAt int64) (string, error) {
	return "mock_signature", nil
}

func (m *MockSigner) Close() error {
	return nil
}

type MockSignValidator struct{}

func (m *MockSignValidator) Validate(ctx context.Context, body []byte, header string, publicKeyBase64 string) error {
	return nil
}

func (m *MockSignValidator) Close() error {
	return nil
}

type MockDecrypter struct{}

func (m *MockDecrypter) Decrypt(ctx context.Context, data, key, iv string) (string, error) {
	return "decrypted_data", nil
}

func (m *MockDecrypter) Close() error {
	return nil
}

type MockEncrypter struct{}

func (m *MockEncrypter) Encrypt(ctx context.Context, data, key, iv string) (string, error) {
	return "encrypted_data", nil
}

func (m *MockEncrypter) Close() error {
	return nil
}

type MockPublisher struct{}

func (m *MockPublisher) Publish(ctx context.Context, topic string, message []byte) error {
	return nil
}

func (m *MockPublisher) Close() error {
	return nil
}

// Mock providers for each plugin type
type MockSignerProvider struct {
	newFunc func(ctx context.Context, config map[string]string) (definition.Signer, func() error, error)
}

func (m *MockSignerProvider) New(ctx context.Context, config map[string]string) (definition.Signer, func() error, error) {
	if config == nil {
		return nil, nil, errors.New("failed to load provider")
	}
	return m.newFunc(ctx, config)
}

type MockSignValidatorProvider struct {
	newFunc func(ctx context.Context, config map[string]string) (definition.SignValidator, func() error, error)
}

func (m *MockSignValidatorProvider) New(ctx context.Context, config map[string]string) (definition.SignValidator, func() error, error) {
	return m.newFunc(ctx, config)
}

type MockDecrypterProvider struct {
	newFunc func(ctx context.Context, config map[string]string) (definition.Decrypter, func() error, error)
}

func (m *MockDecrypterProvider) New(ctx context.Context, config map[string]string) (definition.Decrypter, func() error, error) {
	return m.newFunc(ctx, config)
}

type MockEncrypterProvider struct {
	newFunc func(ctx context.Context, config map[string]string) (definition.Encrypter, func() error, error)
}

func (m *MockEncrypterProvider) New(ctx context.Context, config map[string]string) (definition.Encrypter, func() error, error) {
	return m.newFunc(ctx, config)
}

type MockPublisherProvider struct {
	newFunc func(ctx context.Context, config map[string]string) (definition.Publisher, error)
}

func (m *MockPublisherProvider) New(ctx context.Context, config map[string]string) (definition.Publisher, error) {
	return m.newFunc(ctx, config)
}

// MockStepProvider is a mock implementation of StepProvider
type MockStepProvider struct {
	newFunc func(ctx context.Context, config map[string]string) (definition.Step, func(), error)
}

func (m *MockStepProvider) New(ctx context.Context, config map[string]string) (definition.Step, func(), error) {
	if m.newFunc != nil {
		return m.newFunc(ctx, config)
	}
	return nil, nil, errors.New("new not implemented")
}

// TestNewManager tests the NewManager function
func TestNewManager_Success(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "plugin-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a valid plugin directory
	validPluginDir := filepath.Join(tmpDir, "plugins")
	err = os.MkdirAll(validPluginDir, 0755)
	require.NoError(t, err)

	tests := []struct {
		name        string
		cfg         *ManagerConfig
		wantErr     bool
		errContains string
	}{
		{
			name: "valid configuration",
			cfg: &ManagerConfig{
				Root:       validPluginDir,
				RemoteRoot: "",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, closer, err := NewManager(context.Background(), tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, manager)
				assert.Nil(t, closer)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, manager)
				assert.NotNil(t, closer)
				if closer != nil {
					closer()
				}
			}
		})
	}
}

// TestNewManager_Failure tests various failure scenarios for NewManager
func TestNewManager_Failure(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "plugin-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name        string
		cfg         *ManagerConfig
		wantErr     bool
		errContains string
	}{
		{
			name: "non-existent root path",
			cfg: &ManagerConfig{
				Root: filepath.Join(tmpDir, "nonexistent"),
			},
			wantErr:     true,
			errContains: "no such file or directory",
		},
		{
			name: "non-existent remote root",
			cfg: &ManagerConfig{
				Root:       tmpDir,
				RemoteRoot: filepath.Join(tmpDir, "nonexistent.zip"),
			},
			wantErr:     true,
			errContains: "no such file or directory",
		},
		{
			name: "invalid remote root zip",
			cfg: &ManagerConfig{
				Root:       tmpDir,
				RemoteRoot: filepath.Join(tmpDir, "invalid.zip"),
			},
			wantErr:     true,
			errContains: "zip: not a valid zip file",
		},
	}

	// Create an invalid zip file for testing
	invalidZipPath := filepath.Join(tmpDir, "invalid.zip")
	if err := os.WriteFile(invalidZipPath, []byte("not a zip file"), 0644); err != nil {
		t.Fatalf("Failed to create invalid zip file: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, cleanup, err := NewManager(context.Background(), tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				} else if !contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing %q, got %q", tt.errContains, err.Error())
				}
				if manager != nil {
					t.Error("Expected nil manager, got non-nil")
				}
				if cleanup != nil {
					t.Error("Expected nil cleanup function, got non-nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if manager == nil {
					t.Error("Expected non-nil manager, got nil")
				}
				if cleanup == nil {
					t.Error("Expected non-nil cleanup function, got nil")
				}
				if cleanup != nil {
					cleanup()
				}
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// TestProvider tests the provider function
func TestProvider(t *testing.T) {
	type testProvider struct {
		value string
	}

	// Create a real plugin that exports a Provider symbol
	pluginPath := filepath.Join(os.TempDir(), "test_provider.so")
	defer os.Remove(pluginPath)

	// Create a simple Go file that exports a Provider symbol
	goFile := filepath.Join(os.TempDir(), "test_provider.go")
	err := os.WriteFile(goFile, []byte(`
package main

type TestProvider struct {
	value string
}

var Provider = &TestProvider{value: "test"}

func main() {}
`), 0644)
	if err != nil {
		t.Fatalf("Failed to create test plugin source: %v", err)
	}
	defer os.Remove(goFile)

	// Build the plugin
	cmd := exec.Command("go", "build", "-buildmode=plugin", "-o", pluginPath, goFile)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build test plugin: %v", err)
	}

	// Load the plugin
	p, err := plugin.Open(pluginPath)
	if err != nil {
		t.Fatalf("Failed to load test plugin: %v", err)
	}

	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		id          string
		wantErr     bool
		errContains string
		verify      func(t *testing.T, got interface{})
	}{
		{
			name: "success - valid provider",
			plugins: map[string]*plugin.Plugin{
				"test-provider": p,
			},
			id:      "test-provider",
			wantErr: false,
			verify: func(t *testing.T, got interface{}) {
				provider, ok := got.(*testProvider)
				assert.True(t, ok)
				assert.Equal(t, "test", provider.value)
			},
		},
		{
			name:        "error - plugin not found",
			plugins:     make(map[string]*plugin.Plugin),
			id:          "nonexistent",
			wantErr:     true,
			errContains: "plugin nonexistent not found",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			id:          "test-provider",
			wantErr:     true,
			errContains: "failed to lookup Provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			id:          "test-provider",
			wantErr:     true,
			errContains: "failed to lookup Provider for test-provider",
		},
		{
			name:        "error - empty plugin map",
			plugins:     map[string]*plugin.Plugin{},
			id:          "test-provider",
			wantErr:     true,
			errContains: "plugin test-provider not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := provider[testProvider](tt.plugins, tt.id)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
			if tt.verify != nil {
				tt.verify(t, got)
			}
		})
	}
}

func TestUnzip(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "unzip-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test cases
	tests := []struct {
		name          string
		setup         func() (string, string, error)
		wantErr       bool
		validateFiles func(string) error
	}{
		{
			name: "successful unzip with single file",
			setup: func() (string, string, error) {
				// Create a temporary zip file
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					return "", "", err
				}
				defer zipFile.Close()

				// Create a zip writer
				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a file to the zip
				fileInZip, err := zipWriter.Create("test.txt")
				if err != nil {
					return "", "", err
				}
				fileInZip.Write([]byte("test content"))

				return zipPath, filepath.Join(tempDir, "output"), nil
			},
			wantErr: false,
			validateFiles: func(dest string) error {
				// Check if the file was extracted
				extractedFile := filepath.Join(dest, "test.txt")
				if _, err := os.Stat(extractedFile); err != nil {
					return err
				}
				return nil
			},
		},
		{
			name: "successful unzip with nested directories",
			setup: func() (string, string, error) {
				// Create a temporary zip file
				zipPath := filepath.Join(tempDir, "nested.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					return "", "", err
				}
				defer zipFile.Close()

				// Create a zip writer
				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a file in a nested directory
				fileInZip, err := zipWriter.Create("nested/test.txt")
				if err != nil {
					return "", "", err
				}
				fileInZip.Write([]byte("nested content"))

				return zipPath, filepath.Join(tempDir, "nested-output"), nil
			},
			wantErr: false,
			validateFiles: func(dest string) error {
				// Check if the nested file was extracted
				extractedFile := filepath.Join(dest, "nested", "test.txt")
				if _, err := os.Stat(extractedFile); err != nil {
					return err
				}
				return nil
			},
		},
		{
			name: "non-existent zip file",
			setup: func() (string, string, error) {
				return "non-existent.zip", filepath.Join(tempDir, "output"), nil
			},
			wantErr: true,
			validateFiles: func(dest string) error {
				return nil
			},
		},
		{
			name: "invalid zip file",
			setup: func() (string, string, error) {
				// Create an invalid zip file
				zipPath := filepath.Join(tempDir, "invalid.zip")
				if err := os.WriteFile(zipPath, []byte("not a zip file"), 0644); err != nil {
					return "", "", err
				}
				return zipPath, filepath.Join(tempDir, "output"), nil
			},
			wantErr: true,
			validateFiles: func(dest string) error {
				return nil
			},
		},
		{
			name: "destination directory creation failure",
			setup: func() (string, string, error) {
				// Create a temporary zip file
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					return "", "", err
				}
				defer zipFile.Close()

				// Create a zip writer
				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a file to the zip
				fileInZip, err := zipWriter.Create("test.txt")
				if err != nil {
					return "", "", err
				}
				fileInZip.Write([]byte("test content"))

				// Create a read-only directory
				readOnlyDir := filepath.Join(tempDir, "readonly")
				if err := os.MkdirAll(readOnlyDir, 0444); err != nil {
					return "", "", err
				}

				return zipPath, readOnlyDir, nil
			},
			wantErr: true,
			validateFiles: func(dest string) error {
				return nil
			},
		},
		{
			name: "file copy failure",
			setup: func() (string, string, error) {
				// Create a temporary zip file
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					return "", "", err
				}
				defer zipFile.Close()

				// Create a zip writer
				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a file to the zip
				fileInZip, err := zipWriter.Create("test.txt")
				if err != nil {
					return "", "", err
				}
				fileInZip.Write([]byte("test content"))

				// Create a destination directory with a file that can't be overwritten
				destDir := filepath.Join(tempDir, "dest")
				if err := os.MkdirAll(destDir, 0755); err != nil {
					return "", "", err
				}
				// Create a read-only file in the destination
				readOnlyFile := filepath.Join(destDir, "test.txt")
				if err := os.WriteFile(readOnlyFile, []byte("readonly"), 0444); err != nil {
					return "", "", err
				}

				return zipPath, destDir, nil
			},
			wantErr: true,
			validateFiles: func(dest string) error {
				return nil
			},
		},
		{
			name: "file copy error due to read-only destination",
			setup: func() (string, string, error) {
				// Create a temporary zip file
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					return "", "", err
				}
				defer zipFile.Close()

				// Create a zip writer
				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a file to the zip
				fileInZip, err := zipWriter.Create("test.txt")
				if err != nil {
					return "", "", err
				}
				fileInZip.Write([]byte("test content"))

				// Create a read-only destination directory
				destDir := filepath.Join(tempDir, "readonly-dest")
				if err := os.MkdirAll(destDir, 0444); err != nil {
					return "", "", err
				}

				return zipPath, destDir, nil
			},
			wantErr: true,
			validateFiles: func(dest string) error {
				return nil
			},
		},
		{
			name: "file copy error due to invalid source file",
			setup: func() (string, string, error) {
				// Create a temporary zip file
				zipPath := filepath.Join(tempDir, "test.zip")
				zipFile, err := os.Create(zipPath)
				if err != nil {
					return "", "", err
				}
				defer zipFile.Close()

				// Create a zip writer
				zipWriter := zip.NewWriter(zipFile)
				defer zipWriter.Close()

				// Add a file to the zip with invalid content
				fileInZip, err := zipWriter.Create("test.txt")
				if err != nil {
					return "", "", err
				}
				// Write some content that will cause an error during extraction
				fileInZip.Write([]byte("test content"))

				// Create a destination directory
				destDir := filepath.Join(tempDir, "dest")
				if err := os.MkdirAll(destDir, 0755); err != nil {
					return "", "", err
				}

				// Close the zip file to ensure it's written
				if err := zipWriter.Close(); err != nil {
					return "", "", err
				}
				if err := zipFile.Close(); err != nil {
					return "", "", err
				}

				// Corrupt the zip file
				if err := os.WriteFile(zipPath, []byte("corrupted content"), 0644); err != nil {
					return "", "", err
				}

				return zipPath, destDir, nil
			},
			wantErr: true,
			validateFiles: func(dest string) error {
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, dest, err := tt.setup()
			if err != nil {
				t.Fatalf("Setup failed: %v", err)
			}

			err = unzip(src, dest)
			if (err != nil) != tt.wantErr {
				t.Errorf("unzip() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if err := tt.validateFiles(dest); err != nil {
					t.Errorf("File validation failed: %v", err)
				}
			}
		})
	}
}

func TestValidator(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		plugins   map[string]*plugin.Plugin
		wantPanic bool
		panicMsg  string
	}{
		{
			name: "unimplemented panic",
			cfg: &Config{
				ID:     "test-validator",
				Config: map[string]string{},
			},
			plugins:   make(map[string]*plugin.Plugin),
			wantPanic: true,
			panicMsg:  "unimplemented",
		},
		{
			name: "nil config",
			cfg:  nil,
			plugins: map[string]*plugin.Plugin{
				"test-validator": &plugin.Plugin{},
			},
			wantPanic: true,
			panicMsg:  "unimplemented",
		},
		{
			name: "empty config panic",
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			plugins: map[string]*plugin.Plugin{
				"test-validator": &plugin.Plugin{},
			},
			wantPanic: true,
			panicMsg:  "unimplemented",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			if tt.wantPanic {
				defer func() {
					if r := recover(); r == nil {
						t.Error("Expected panic, got none")
					} else if !contains(fmt.Sprint(r), tt.panicMsg) {
						t.Errorf("Expected panic containing %q, got %q", tt.panicMsg, r)
					}
				}()
			}

			got, err := m.Validator(context.Background(), tt.cfg)
			if !tt.wantPanic {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if got == nil {
					t.Error("Expected non-nil validator, got nil")
				}
			}
		})
	}
}

// MockKeyManager is a mock implementation of KeyManager
type MockKeyManager struct{}

func (m *MockKeyManager) GetPublicKey(ctx context.Context, subscriberId string) (string, error) {
	return "mock_public_key", nil
}

func (m *MockKeyManager) DeletePrivateKeys(ctx context.Context, subscriberId string) error {
	return nil
}

func (m *MockKeyManager) Close() error {
	return nil
}

func (m *MockKeyManager) GenerateKeyPairs() (*model.Keyset, error) {
	return &model.Keyset{
		UniqueKeyID:    "mock_key_id",
		SigningPrivate: "mock_signing_private",
		SigningPublic:  "mock_signing_public",
		EncrPrivate:    "mock_encr_private",
		EncrPublic:     "mock_encr_public",
	}, nil
}

func (m *MockKeyManager) StorePrivateKeys(ctx context.Context, keyID string, keys *model.Keyset) error {
	return nil
}

func (m *MockKeyManager) SigningPrivateKey(ctx context.Context, keyID string) (string, string, error) {
	return "mock_signing_private", "mock_iv", nil
}

func (m *MockKeyManager) EncrPrivateKey(ctx context.Context, keyID string) (string, string, error) {
	return "mock_encr_private", "mock_iv", nil
}

func (m *MockKeyManager) SigningPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error) {
	return "mock_signing_public", nil
}

func (m *MockKeyManager) EncrPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error) {
	return "mock_encr_public", nil
}

// MockKeyManagerProvider is a mock implementation of KeyManagerProvider
type MockKeyManagerProvider struct {
	newFunc func(ctx context.Context, cache definition.Cache, rClient definition.RegistryLookup, config map[string]string) (definition.KeyManager, func() error, error)
}

func (m *MockKeyManagerProvider) New(ctx context.Context, cache definition.Cache, rClient definition.RegistryLookup, config map[string]string) (definition.KeyManager, func() error, error) {
	if config == nil {
		return nil, nil, errors.New("config cannot be nil")
	}
	return m.newFunc(ctx, cache, rClient, config)
}

// MockCache is a mock implementation of Cache
type MockCache struct{}

func (m *MockCache) Get(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *MockCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return nil
}

func (m *MockCache) Delete(ctx context.Context, key string) error {
	return nil
}

func (m *MockCache) Clear(ctx context.Context) error {
	return nil
}

func (m *MockCache) Close() error {
	return nil
}

// MockRegistryLookup is a mock implementation of RegistryLookup
type MockRegistryLookup struct{}

func (m *MockRegistryLookup) LookupRegistry(ctx context.Context, subscriberId string) (string, error) {
	return "mock_registry", nil
}

func (m *MockRegistryLookup) Lookup(ctx context.Context, subscription *model.Subscription) ([]model.Subscription, error) {
	return []model.Subscription{}, nil
}

// MockPlugin is a mock implementation of plugin.Plugin
type MockPlugin struct {
	lookupFunc func(symName string) (plugin.Symbol, error)
}

func (m *MockPlugin) Lookup(symName string) (plugin.Symbol, error) {
	return m.lookupFunc(symName)
}

func TestKeyManager(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		cache       definition.Cache
		rClient     definition.RegistryLookup
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			cache:       &MockCache{},
			rClient:     &MockRegistryLookup{},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			cache:       &MockCache{},
			rClient:     &MockRegistryLookup{},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			cache:       &MockCache{},
			rClient:     &MockRegistryLookup{},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			cache:       &MockCache{},
			rClient:     &MockRegistryLookup{},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			cache:       &MockCache{},
			rClient:     &MockRegistryLookup{},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.KeyManager(context.Background(), tt.cache, tt.rClient, tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestPublisher(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.Publisher(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestSchemaValidator(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.SchemaValidator(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestRouter(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.Router(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestMiddleware(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.Middleware(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestStep(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.Step(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestCache(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.Cache(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestSigner(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.Signer(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestEncryptor(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.Encryptor(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestDecryptor(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.Decryptor(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestSignValidator(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string]*plugin.Plugin
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "error - nil config",
			plugins:     make(map[string]*plugin.Plugin),
			cfg:         nil,
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - empty config",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "",
				Config: nil,
			},
			wantErr:     true,
			errContains: "failed to load provider for",
		},
		{
			name:    "error - plugin not found",
			plugins: make(map[string]*plugin.Plugin),
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - provider lookup failed",
			plugins: map[string]*plugin.Plugin{
				"test-provider": &plugin.Plugin{},
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
		{
			name: "error - nil plugin",
			plugins: map[string]*plugin.Plugin{
				"test-provider": nil,
			},
			cfg: &Config{
				ID:     "test-provider",
				Config: map[string]string{},
			},
			wantErr:     true,
			errContains: "failed to load provider for test-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				plugins: tt.plugins,
				closers: make([]func(), 0),
			}

			got, err := m.SignValidator(context.Background(), tt.cfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}
