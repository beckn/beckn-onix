package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/core/module"
	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
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

// mockRun is a mock implementation of the `run` function, simulating a successful run.
func mockRun(ctx context.Context, configPath string) error {
	return nil // Simulate a successful run
}

// TestMainFunction tests the main function execution, including command-line argument parsing.
func TestMainFunction(t *testing.T) {
	// Backup original run function and restore it after test
	origRun := runFunc
	defer func() { runFunc = origRun }()
	runFunc = mockRun

	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// Set mock command-line arguments
	os.Args = []string{"cmd", "-config=../../config/test-config.yaml"}

	fs := flag.NewFlagSet("test", flag.ExitOnError)
	fs.StringVar(&configPath, "config", "../../config/clientSideHandler-config.yaml", "Path to the configuration file")

	if err := fs.Parse(os.Args[1:]); err != nil {
		t.Fatalf("Failed to parse flags: %v", err)
	}
	main()
}

func TestRunSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	configPath := "../test/validConfig.yaml"

	// Mock dependencies
	originalNewManager := newManagerFunc
	newManagerFunc = func(ctx context.Context, cfg *plugin.ManagerConfig) (*plugin.Manager, func(), error) {
		return &plugin.Manager{}, func() {}, nil
	}
	defer func() { newManagerFunc = originalNewManager }()

	originalNewServer := newServerFunc
	newServerFunc = func(ctx context.Context, mgr handler.PluginManager, cfg *Config) (http.Handler, error) {
		return http.NewServeMux(), nil
	}
	defer func() { newServerFunc = originalNewServer }()

	if err := run(ctx, filepath.Clean(configPath)); err != nil {
		t.Errorf("Expected no error, but got: %v", err)
	}
}

// TestRunFailure validates failure scenarios for the run function.
func TestRunFailure(t *testing.T) {
	tests := []struct {
		name        string
		configData  string
		mockMgr     func() (*MockPluginManager, func(), error)
		mockLogger  func(cfg *Config) error
		mockServer  func(ctx context.Context, mgr handler.PluginManager, cfg *Config) (http.Handler, error)
		expectedErr string
	}{
		{
			name:       "Invalid Config File",
			configData: "invalid_config.yaml",
			mockMgr: func() (*MockPluginManager, func(), error) {
				return &MockPluginManager{}, func() {}, nil
			},
			mockLogger: func(cfg *Config) error {
				return nil
			},
			mockServer: func(ctx context.Context, mgr handler.PluginManager, cfg *Config) (http.Handler, error) {
				return nil, errors.New("failed to start server")
			},
			expectedErr: "failed to initialize config: invalid config: missing app name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			testFilePath := tt.configData
			mockConfig := `invalid: "config"`
			err := os.WriteFile(testFilePath, []byte(mockConfig), 0644)
			if err != nil {
				t.Errorf("Failed to create test config file: %v", err)
			}
			defer os.Remove(testFilePath)

			// Mock dependencies
			originalNewManager := newManagerFunc
			// newManagerFunc = func(ctx context.Context, cfg *plugin.ManagerConfig) (*plugin.Manager, func(), error) {
			// 	return tt.mockMgr()
			// }
			newManagerFunc = nil
			defer func() { newManagerFunc = originalNewManager }()

			originalNewServer := newServerFunc
			newServerFunc = func(ctx context.Context, mgr handler.PluginManager, cfg *Config) (http.Handler, error) {
				return tt.mockServer(ctx, mgr, cfg)
			}
			defer func() { newServerFunc = originalNewServer }()

			// Run function
			err = run(ctx, testFilePath)
			if err == nil {
				t.Errorf("Expected error, but got nil")
			} else if err.Error() != tt.expectedErr {
				t.Errorf("Expected error '%s', but got '%s'", tt.expectedErr, err.Error())
			}
		})
	}
}

// TestInitConfigSuccess tests the successful initialization of the config.
func TestInitConfigSuccess(t *testing.T) {
	tests := []struct {
		name       string
		configData string
	}{
		{
			name: "Valid Config",
			configData: `
appName: "TestApp"
http:
  port: "8080"
  timeout:
    read: 5
    write: 5
    idle: 10
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := "test_config_success.yaml"
			defer os.Remove(configPath)

			err := os.WriteFile(configPath, []byte(tt.configData), 0644)
			if err != nil {
				t.Errorf("Failed to create test config file: %v", err)
			}

			_, err = initConfig(context.Background(), configPath)
			if err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
		})
	}
}

// TestInitConfigFailure tests failure scenarios for config initialization.
func TestInitConfigFailure(t *testing.T) {
	tests := []struct {
		name        string
		configData  string
		expectedErr string
	}{
		{
			name:        "Invalid YAML Format",
			configData:  `appName: "TestApp"\nhttp: { invalid_yaml }`,
			expectedErr: "could not decode config",
		},
		{
			name:        "Missing Required Fields",
			configData:  `appName: ""\nhttp:\n  timeout:\n    read: 5\n`,
			expectedErr: "could not decode config: yaml: did not find expected key",
		},
		{
			name:        "Non-Existent File",
			configData:  "",
			expectedErr: "could not open config file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := "test_config_failure.yaml"

			if tt.configData != "" {
				err := os.WriteFile(configPath, []byte(tt.configData), 0644)
				if err != nil {
					t.Errorf("Failed to create test config file: %v", err)
				}
				defer os.Remove(configPath)
			} else {
				// Ensure file does not exist for non-existent file test
				os.Remove(configPath)
			}

			_, err := initConfig(context.Background(), configPath)
			if err == nil {
				t.Errorf("Expected error but got nil")
			} else if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("Expected error containing '%s', but got '%s'", tt.expectedErr, err.Error())
			}
		})
	}
}

// TestNewServerSuccess tests successful server creation.
func TestNewServerSuccess(t *testing.T) {
	tests := []struct {
		name    string
		modules []module.Config
	}{
		{
			name:    "Successful server creation with no modules",
			modules: []module.Config{}, // No modules to simplify the test
		},
	}

	mockMgr := new(MockPluginManager) // Mocking PluginManager

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Modules: tt.modules,
				HTTP: httpConfig{
					Port: "8080",
					Timeouts: timeoutConfig{
						Read:  5,
						Write: 5,
						Idle:  10,
					},
				},
			}

			handler, err := newServer(context.Background(), mockMgr, cfg)

			if err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
			if handler == nil {
				t.Errorf("Expected handler to be non-nil, but got nil")
			}
		})
	}
}

// TestNewServerFailure tests failure scenarios when creating a server.
func TestNewServerFailure(t *testing.T) {
	tests := []struct {
		name    string
		modules []module.Config
	}{
		{
			name: "Module registration failure",
			modules: []module.Config{
				{
					Name: "InvalidModule",
					Path: "/invalid",
				},
			},
		},
	}

	mockMgr := new(MockPluginManager) // Mocking PluginManager

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Modules: tt.modules,
				HTTP: httpConfig{
					Port: "8080",
					Timeouts: timeoutConfig{
						Read:  5,
						Write: 5,
						Idle:  10,
					},
				},
			}

			handler, err := newServer(context.Background(), mockMgr, cfg)

			if err == nil {
				t.Errorf("Expected an error, but got nil")
			}
			if handler != nil {
				t.Errorf("Expected handler to be nil, but got a non-nil value")
			}
		})
	}
}

// TestValidateConfigSuccess tests validation of a correct config.
func TestValidateConfigSuccess(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "Valid Config",
			cfg: Config{
				AppName: "TestApp",
				HTTP: httpConfig{
					Port: "8080",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(&tt.cfg)
			if err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
		})
	}
}

// TestValidateConfigFailure tests validation failures for incorrect config.
func TestValidateConfigFailure(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		expectedErr string
	}{
		{
			name: "Missing AppName",
			cfg: Config{
				AppName: "",
				HTTP: httpConfig{
					Port: "8080",
				},
			},
			expectedErr: "missing app name",
		},
		{
			name: "Missing Port",
			cfg: Config{
				AppName: "TestApp",
				HTTP: httpConfig{
					Port: "",
				},
			},
			expectedErr: "missing port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(&tt.cfg)
			if err == nil {
				t.Errorf("Expected error '%s', but got nil", tt.expectedErr)
			} else if err.Error() != tt.expectedErr {
				t.Errorf("Expected error '%s', but got '%s'", tt.expectedErr, err.Error())
			}
		})
	}
}
