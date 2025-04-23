package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"strings"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"

	"gopkg.in/yaml.v2"
)

// Config represents the plugin manager configuration.
type Config struct {
	Root            string       `yaml:"root"`
	SchemaValidator PluginConfig `yaml:"schema_validator"`
}

// PluginConfig represents configuration details for a plugin.
type PluginConfig struct {
	ID     string            `yaml:"id"`
	Config map[string]string `yaml:"config"`
}

// // ValidationPluginConfig represents configuration details for a plugin.
// type ValidationPluginConfig struct {
// 	ID         string        `yaml:"id"`
// 	Schema     SchemaDetails `yaml:"config"`
// 	PluginPath string        `yaml:"plugin_path"`
// }

// SchemaDetails contains information about the plugin schema directory.
type SchemaDetails struct {
	SchemaDir string `yaml:"schema_dir"`
}

// // Config represents the configuration for the application, including plugin configurations.
// type Config struct {
// 	Plugins struct {
// 		ValidationPlugin ValidationPluginConfig `yaml:"validation_plugin"`
// 	} `yaml:"plugins"`
// }

// Manager handles dynamic plugin loading and management.
type Manager struct {
	vp  definition.SchemaValidatorProvider
	cfg *Config
}

// NewManager initializes a new Manager with the given configuration file.
func NewManager(ctx context.Context, cfg *Config) (*Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration cannot be nil")
	}

	// Load schema validator plugin
	vp, err := provider[definition.SchemaValidatorProvider](cfg.Root, cfg.SchemaValidator.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load validator plugin: %w", err)
	}
	if vp == nil {
		return nil, fmt.Errorf("validator provider is nil")
	}

	// // Initialize validator
	// validatorMap, defErr := vp.New(ctx, map[string]string{
	// 	"schema_dir": cfg.Plugins.ValidationPlugin.Schema.SchemaDir,
	// })
	// if defErr != nil {
	// 	return nil, fmt.Errorf("failed to initialize validator: %v", defErr)
	// }

	// // Initialize the validators map
	// validators := make(map[string]definition.Validator)
	// for key, validator := range validatorMap {
	// 	validators[key] = validator
	// }

	return &Manager{vp: vp, cfg: cfg}, nil
}

// provider loads a plugin dynamically and retrieves its provider instance.
func provider[T any](path string, id string) (T, error) {
	var zero T
	if len(strings.TrimSpace(id)) == 0 {
		return zero, nil
	}

	p, err := plugin.Open(pluginPath(path, id))
	if err != nil {
		return zero, fmt.Errorf("failed to open plugin %s: %w", id, err)
	}

	symbol, err := p.Lookup("Provider")
	if err != nil {
		return zero, fmt.Errorf("failed to find Provider symbol in plugin %s: %w", id, err)
	}

	// Ensure the symbol is of the correct type
	prov, ok := symbol.(*T)
	if !ok {
		return zero, fmt.Errorf("failed to cast Provider for %s", id)
	}

	return *prov, nil
}

// pluginPath constructs the path to the plugin pkg object file.
func pluginPath(path, id string) string {
	return filepath.Join(path, id+".so")
}

// Validators retrieves the validation plugin instances.
func (m *Manager) SchemaValidator(ctx context.Context) (definition.SchemaValidator, func() error, error) {
	if m.vp == nil {
		return nil, nil, fmt.Errorf("schema validator plugin provider not loaded")

	}
	schemaValidator, close, err := m.vp.New(ctx, m.cfg.SchemaValidator.Config)
	if err != nil {

		return nil, nil, fmt.Errorf("failed to initialize schema validator: %v", err)
	}
	return schemaValidator, close, nil
}

// LoadConfig loads the configuration from a YAML file.
func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var cfg Config
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to decode config file: %w", err)
	}

	return &cfg, nil
}
