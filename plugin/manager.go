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

type PluginConfig struct {
	ID     string            `yaml:"id"`
	Config map[string]string `yaml:"config"`
}

type PluginManagerConfig struct {
	Plugins map[string]PluginConfig `yaml:"plugins"`
}

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

	return &PluginManager{
		pluginDir: pluginDir,
		config:    config,
		plugins:   make(map[string]interface{}),
	}, nil
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

	// pluginPath := filepath.Join(pm.pluginDir, pluginName+".so")
	pluginPath := filepath.Join(finalPluginDir, pluginName+".so")
	p, err := plugin.Open(pluginPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open plugin %s: %w", pluginName, err)
	}

	symbol, err := p.Lookup("GetPlugin")
	if err != nil {
		return nil, fmt.Errorf("failed to find GetPlugin function in plugin %s: %w", pluginName, err)
	}

	var instance interface{}
	if getSignerProvider, ok := symbol.(func() SignerProvider); ok {
		provider := getSignerProvider()
		instance, err = provider.New(context.Background(), pluginConfig)
	} else if getVerifierProvider, ok := symbol.(func() ValidatorProvider); ok {
		provider := getVerifierProvider()
		instance, err = provider.New(context.Background(), pluginConfig)
	} else {
		return nil, fmt.Errorf("plugin %s does not match expected types", pluginName)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize plugin %s: %w", pluginName, err)
	}

	pm.plugins[pluginID] = instance
	return instance, nil
}

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

func (pm *PluginManager) Close() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.plugins = make(map[string]interface{})
}
