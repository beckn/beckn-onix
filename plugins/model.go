package main

type PluginConfig struct {
	Plugins Plugins `yaml:"plugins"`
}

// PluginConfig represents the configuration for plugins.
type Plugins struct {
	ValidationPlugin ValidationPlugin `yaml:"validation_plugin"`
}

// ValidationPlugin represents the configuration for a validation plugin.
type ValidationPlugin struct {
	ID         string        `yaml:"id"`
	Config     PluginDetails `yaml:"config"`
	PluginPath string        `yaml:"plugin_path"`
}

// PluginDetails represents the details of the plugin configuration.
type PluginDetails struct {
	Schema string `yaml:"schema"`
}
