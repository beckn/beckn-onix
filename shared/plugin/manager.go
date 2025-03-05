package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"sync"

	"gopkg.in/yaml.v3"
)

// PluginConfig represents the configuration for a specific plugin.
type PluginConfig struct {
	ID     string            `yaml:"id"`
	Config map[string]string `yaml:"config"`
}

// PluginManagerConfig holds configurations for multiple plugins.
type PluginManagerConfig struct {
	Plugins map[string]PluginConfig `yaml:"plugins"`
}

// PluginManager handles dynamic loading and management of plugins.
type PluginManager struct {
	pluginDir string
	config    *PluginManagerConfig
	plugins   map[string]interface{}
	mu        sync.Mutex
}

// New initializes a new PluginManager with the given plugin directory and config file.
func New(pluginDir, configPath string) (*PluginManager, error) {
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return &PluginManager{
		pluginDir: pluginDir,
		config:    config,
		plugins:   make(map[string]interface{}),
	}, nil
}

// loadConfig reads and parses the plugin configuration from a YAML file.
func loadConfig(path string) (*PluginManagerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg PluginManagerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &cfg, nil
}

// LoadPluginByID loads a plugin by its ID and returns its instance.
func (pm *PluginManager) LoadPluginByID(pluginID string) (interface{}, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if instance, exists := pm.plugins[pluginID]; exists {
		return instance, nil
	}

	var pluginName string
	var pluginConfig map[string]string

	for name, p := range pm.config.Plugins {
		if p.ID == pluginID {
			pluginName = name
			pluginConfig = p.Config
			break
		}
	}

	if pluginName == "" {
		return nil, fmt.Errorf("plugin with ID %s not found in config", pluginID)
	}

	finalPluginDir := pm.pluginDir + "/" + pluginName
	pluginPath := filepath.Join(finalPluginDir, pluginName+".so")

	p, err := plugin.Open(pluginPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open plugin %s: %w", pluginName, err)
	}

	// Lookup the Provider symbol
	symbol, err := p.Lookup("Provider")
	if err != nil {
		return nil, fmt.Errorf("failed to find Provider symbol in plugin %s: %w", pluginName, err)
	}

	// Initialize the plugin instance
	var instance interface{}

	switch pluginID {
	case "signing_plugin":
		provider, ok := symbol.(SignerProvider)
		if !ok {
			return nil, fmt.Errorf("plugin %s does not match expected SignerProvider type", pluginName)
		}
		instance, err = provider.New(context.Background(), pluginConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize plugin %s: %w", pluginName, err)
		}
	case "verification_plugin":
		provider, ok := symbol.(ValidatorProvider)
		if !ok {
			return nil, fmt.Errorf("plugin %s does not match expected ValidatorProvider type", pluginName)
		}
		instance, err = provider.New(context.Background(), pluginConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize plugin %s: %w", pluginName, err)
		}
	default:
		return nil, fmt.Errorf("unknown plugin ID: %s", pluginID)
	}

	pm.plugins[pluginID] = instance
	return instance, nil
}

// GetSigner loads and returns the signing plugin instance
func (pm *PluginManager) GetSigner() (Signer, error) {
	instance, err := pm.LoadPluginByID("signing_plugin")
	if err != nil {
		return nil, err
	}

	signer, ok := instance.(Signer)
	if !ok {
		return nil, fmt.Errorf("plugin does not implement Signer interface")
	}
	return signer, nil
}

// GetVerifier loads and returns the verification plugin instance
func (pm *PluginManager) GetVerifier() (Validator, error) {
	instance, err := pm.LoadPluginByID("verification_plugin")
	if err != nil {
		return nil, err
	}

	verifier, ok := instance.(Validator)
	if !ok {
		return nil, fmt.Errorf("plugin does not implement Validator interface")
	}
	return verifier, nil
}

// Close clears the loaded plugins and releases any associated resources.
func (pm *PluginManager) Close() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.plugins = make(map[string]interface{})
}
