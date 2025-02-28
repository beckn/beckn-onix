
package pubsub

import (
	"os"
	"gopkg.in/yaml.v2"
)

// PluginConfig represents the configuration for a plugin
type PluginConfig struct {
	ID     string                 `yaml:"id"`
	Config map[string]interface{} `yaml:"config"`
}

// Config represents the top-level configuration
type Config struct {
	Plugins map[string]PluginConfig `yaml:"plugins"`
}

// LoadConfig loads the configuration from a YAML file
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// GetPluginConfig retrieves the configuration for a specific plugin type
func (c *Config) GetPluginConfig(pluginType string) (PluginConfig, bool) {
	if config, ok := c.Plugins[pluginType]; ok {
		return config, true
	}
	return PluginConfig{}, false
}
