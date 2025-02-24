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

// Plugin config structs
type PluginConfig struct {
	ID     string            `yaml:"id"`
	Config map[string]string `yaml:"config"`
}

type PluginManagerConfig struct {
	Plugins    map[string]PluginConfig `yaml:"plugins"`
	Middleware []PluginConfig          `yaml:"middleware"`
}

// PluginManager struct
type PluginManager struct {
	pluginDir string
	config    *PluginManagerConfig
	plugins   map[string]interface{}
	mu        sync.Mutex
}

func New(pluginDir, configPath string) (*PluginManager, error) {
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	pm := &PluginManager{
		pluginDir: pluginDir,
		config:    config,
		plugins:   make(map[string]interface{}),
	}

	return pm, nil
}

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

// Load a plugin dynamically
func (pm *PluginManager) LoadPluginByID(pluginID string) (interface{}, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Return if already loaded
	if instance, exists := pm.plugins[pluginID]; exists {
		return instance, nil
	}

	// Find the plugin in the config
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

	pluginPath := filepath.Join(pm.pluginDir, pluginName+".so")
	p, err := plugin.Open(pluginPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open plugin %s: %w", pluginName, err)
	}

	symbol, err := p.Lookup("GetPlugin")
	if err != nil {
		return nil, fmt.Errorf("failed to find GetPlugin function in plugin %s: %w", pluginName, err)
	}

	getPluginFunc, ok := symbol.(func() SignerProvider)
	if !ok {
		return nil, fmt.Errorf("GetPlugin function has incorrect type in plugin %s", pluginName)
	}

	provider := getPluginFunc()
	instance, err := provider.New(context.Background(), pluginConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize plugin %s: %w", pluginName, err)
	}

	pm.plugins[pluginID] = instance
	return instance, nil
}

func (pm *PluginManager) GetSigner() (Signer, error) {
	instance, err := pm.LoadPluginByID("beckn_signing")
	if err != nil {
		return nil, err
	}

	signer, ok := instance.(Signer)
	if !ok {
		return nil, fmt.Errorf("plugin does not implement Signer interface")
	}
	return signer, nil
}

func (pm *PluginManager) Close() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.plugins = make(map[string]interface{})
}
