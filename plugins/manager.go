package plugins

import (
	//"beckn-onix/plugins/plugin_definition"
	"fmt"
	"io/ioutil"
	"log"
	"plugin"
	"runtime"
	"time"

	"gopkg.in/yaml.v2"
)

// PluginConfig represents the configuration for plugins, including the plugins themselves.
type PluginConfig struct {
	Plugins Plugins `yaml:"plugins"`
}

// Plugins holds the various plugin types used in the configuration.
type Plugins struct {
	ValidationPlugin ValidationPlugin `yaml:"validation_plugin"`
}

// ValidationPlugin represents a plugin with an ID, configuration, and the path to the plugin.
type ValidationPlugin struct {
	ID         string        `yaml:"id"`
	Config     PluginDetails `yaml:"config"`
	PluginPath string        `yaml:"plugin_path"`
}

// PluginDetails contains information about the plugin schema directory.
type PluginDetails struct {
	Schema string `yaml:"schema_dir"`
}

// PluginManager manages the loading and execution of plugins.
type PluginManager struct {
	validatorProvider ValidatorProvider
}

// NewValidatorProvider initializes the PluginManager with the given configuration.
func NewValidatorProvider(pluginsConfig PluginConfig) (*PluginManager, map[string]Validator, error) {
	start := time.Now()

	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	validationPlugin := pluginsConfig.Plugins.ValidationPlugin
	if validationPlugin.ID == "" {
		return nil, nil, fmt.Errorf("validation_plugin ID is empty")
	}

	pluginPath := validationPlugin.PluginPath + validationPlugin.ID + ".so"

	// Check if the plugin path is empty
	if pluginPath == "" {
		return nil, nil, fmt.Errorf("plugin path is empty")
	}

	// Load the plugin
	p, err := plugin.Open(pluginPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open plugin: %v", err)
	}

	vpSymbol, err := p.Lookup("GetProvider")
	if err != nil {
		return nil, nil, err
	}
	getProviderFunc, ok := vpSymbol.(func() ValidatorProvider)
	if !ok {
		return nil, nil, fmt.Errorf("failed to cast to *plugins.ValidatorProvider")
	}
	validatorProvider := getProviderFunc()

	schemaDir := pluginsConfig.Plugins.ValidationPlugin.Config.Schema
	validator, err := validatorProvider.Initialize(schemaDir)
	if err != nil {
		log.Fatalf("Failed to initialize validator provider: %v", err)
	}

	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)
	fmt.Printf("Memory allocated during plugin boot-up: %v MiB", (memStatsAfter.Alloc-memStatsBefore.Alloc)/1024/1024)
	fmt.Println(" ")

	fmt.Printf("plugin boot-up executed in %s\n", time.Since(start))
	return &PluginManager{validatorProvider: validatorProvider}, validator, nil
}

// LoadPluginsConfig loads the plugins configuration from a YAML file.
func LoadPluginsConfig(filePath string) (PluginConfig, error) {
	// start := time.Now()

	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return PluginConfig{}, err
	}

	var config PluginConfig
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return PluginConfig{}, err
	}
	// fmt.Printf("loadconfig executed in %s\n", time.Since(start))

	return config, nil
}
