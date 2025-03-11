package plugin

import (
	logger "beckn-onix/shared/log"
	"beckn-onix/shared/plugin/definition"
	"fmt"
	"path/filepath"
	"plugin"
	"sync"
)

// Plugin defines the interface that all plugins must implement
type Plugin interface {
	Handle(message string) error
	Configure(config map[string]interface{}) error
}

// PluginManager manages all registered plugins
type PluginManager struct {
	plugins    map[string]Plugin // Changed from definition.Plugin to Plugin
	pluginPath string
	config     *Config
	mu         sync.RWMutex
}

// NewPluginManager creates a new plugin manager
func NewPluginManager(pluginPath string, configFile string) *PluginManager {
	config, err := LoadConfig(configFile)
	if err != nil {
		logger.Log.Info("Warning: Failed to load config: ", err)
		config = &Config{
			Plugins: make(map[string]PluginConfig),
		}
	}

	return &PluginManager{
		plugins:    make(map[string]Plugin),
		pluginPath: pluginPath,
		config:     config,
	}
}

// LoadPlugin loads a plugin from a .so file
func (pm *PluginManager) LoadPlugin(name string) error {
	pluginFile := filepath.Join(pm.pluginPath, name+"/"+name+".so")
	plug, err := plugin.Open(pluginFile)
	if err != nil {
		return err
	}

	// Look up exported symbol
	symbol, err := plug.Lookup("Plugin")
	if err != nil {
		return err
	}

	// Register plugin
	pluginInstance := symbol.(Plugin)
	pm.Register(name, pluginInstance)

	// Configure the plugin if configuration exists
	if pluginConfig, exists := pm.config.GetPluginConfig(name); exists {
		if err := pluginInstance.Configure(pluginConfig.Config); err != nil {
			logger.Log.Info("Warning: Failed to configure plugin", name, ":", err)
		}
	}

	return nil
}

// Register adds a plugin to the manager
func (pm *PluginManager) Register(name string, plugin Plugin) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.plugins[name] = plugin
}

// Get returns a plugin by name
func (pm *PluginManager) Get(name string) (Plugin, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if plugin, exists := pm.plugins[name]; exists {
		return plugin, nil
	}
	return nil, fmt.Errorf("plugin '%s' not found", name)
}

// GetPublisher returns the publisher plugin if registered
func (pm *PluginManager) GetPublisher(name string) (definition.Publisher, error) {
	plugin, err := pm.Get(name)
	if err != nil {
		return nil, err
	}

	publisher, ok := plugin.(definition.Publisher)
	if !ok {
		return nil, fmt.Errorf("plugin '%s' is not a publisher", name)
	}
	return publisher, nil
}

// PublishMessage publishes a message to a specific topic
func (pm *PluginManager) PublishMessage(topic string, message string) {
	if plugin, exists := pm.plugins[topic]; exists {
		plugin.Handle(message)
	} else {
		logger.Log.Info("No plugin registered for topic: ", topic)
	}
}
