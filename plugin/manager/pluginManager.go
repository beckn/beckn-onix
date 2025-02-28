package pubsub

import (
	logger "beckn-onix/shared/utils"
	"path/filepath"
	"plugin"
)

// Plugin defines the interface that all plugins must implement
type Plugin interface {
	Handle(message string)
	Configure(config map[string]interface{}) error
}

// PluginManager manages all registered plugins
type PluginManager struct {
	plugins    map[string]Plugin
	pluginPath string
	config     *Config
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
	pm.RegisterPlugin(name, pluginInstance)

	// Configure the plugin if configuration exists
	if pluginConfig, exists := pm.config.GetPluginConfig(name); exists {
		if err := pluginInstance.Configure(pluginConfig.Config); err != nil {
			logger.Log.Info("Warning: Failed to configure plugin", name, ":",err)
		}
	}

	return nil
}

// RegisterPlugin registers a new plugin with the manager
func (pm *PluginManager) RegisterPlugin(topic string, plugin Plugin) {
	pm.plugins[topic] = plugin
}

// PublishMessage publishes a message to a specific topic
func (pm *PluginManager) PublishMessage(topic string, message string) {
	if plugin, exists := pm.plugins[topic]; exists {
		plugin.Handle(message)
	} else {
		logger.Log.Info("No plugin registered for topic: ", topic)
	}
}
