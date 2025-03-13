package plugin

import (
	"beckn-onix/shared/plugin/definition"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"strings"

	"gopkg.in/yaml.v2"
)

// ValidationPluginConfig represents configuration details for a plugin.
type ValidationPluginConfig struct {
	ID         string        `yaml:"id"`
	Schema     SchemaDetails `yaml:"config"`
	PluginPath string        `yaml:"plugin_path"`
}

// SchemaDetails contains information about the plugin schema directory.
type SchemaDetails struct {
	SchemaDir string `yaml:"schema_dir"`
}

// Config represents the configuration for the application, including plugin configurations.
type Config struct {
	Plugins struct {
		ValidationPlugin ValidationPluginConfig `yaml:"validation_plugin"`
	} `yaml:"plugins"`
}

// Manager handles dynamic plugin loading and management.
type Manager struct {
	vp         definition.ValidatorProvider
	validators map[string]definition.Validator
	cfg        *Config
}

// NewManager initializes a new Manager with the given configuration file.
func NewManager(ctx context.Context, cfg *Config) (*Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration cannot be nil")
	}

	// Load validator plugin
	vp, err := provider[definition.ValidatorProvider](cfg.Plugins.ValidationPlugin.PluginPath, cfg.Plugins.ValidationPlugin.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load validator plugin: %w", err)
	}
	if vp == nil {
		return nil, fmt.Errorf("validator provider is nil")
	}

	// Initialize validator
	validatorMap, err := vp.New(ctx, map[string]string{
		"schema_dir": cfg.Plugins.ValidationPlugin.Schema.SchemaDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize validator: %w", err)
	}

	// Initialize the validators map
	validators := make(map[string]definition.Validator)
	for key, validator := range validatorMap {
		validators[key] = validator
	}
	fmt.Println("validators : ", validators)

	return &Manager{vp: vp, validators: validators, cfg: cfg}, nil
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

// pluginPath constructs the path to the plugin shared object file.
func pluginPath(path, id string) string {
	return filepath.Join(path, id+".so")
}

// Validators retrieves the validation plugin instances.
func (m *Manager) Validators(ctx context.Context) (map[string]definition.Validator, error) {
	if m.vp == nil {
		return nil, fmt.Errorf("validator plugin provider not loaded")
	}

	configMap := map[string]string{
		"schema_dir": m.cfg.Plugins.ValidationPlugin.Schema.SchemaDir,
	}
	_, err := m.vp.New(ctx, configMap)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize validator: %w", err)
	}

	return m.validators, nil
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
