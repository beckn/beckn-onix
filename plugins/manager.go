package main

import (
	//"context"
	"fmt"
	"io/ioutil"

	//"log"
	"plugin"

	"gopkg.in/yaml.v2"
)

// PluginManager manages the loading and execution of plugins.
type PluginManager struct {
	validatorProvider ValidatorProvider
}

// NewPluginManager initializes the PluginManager with the given configuration. //new
func New(pluginsConfig PluginConfig) (*PluginManager, error) {
	validationPlugin := pluginsConfig.Plugins.ValidationPlugin

	if validationPlugin.ID == "" {
		return nil, fmt.Errorf("validation_plugin ID is empty")
	}

	pluginPath := validationPlugin.PluginPath + validationPlugin.ID + ".so"

	// Check if the plugin path is empty
	if pluginPath == "" {
		return nil, fmt.Errorf("plugin path is empty")
	}

	// Load the plugin
	p, err := plugin.Open(pluginPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open plugin: %v", err)
	}

	vpSymbol, err := p.Lookup("Provider")
	if err != nil {
		return nil, err
	}

	validatorProvider, ok := vpSymbol.(ValidatorProvider)
	if !ok {
		return nil, fmt.Errorf("failed to cast to ValidatorProvider")
	}

	return &PluginManager{validatorProvider: validatorProvider}, nil
}

// loadPluginsConfig loads the plugins configuration from a YAML file.
func loadPluginsConfig(filePath string) (PluginConfig, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return PluginConfig{}, err
	}

	var config PluginConfig
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return PluginConfig{}, err
	}

	return config, nil
}

// func main() {
// 	pluginsConfig, err := loadPluginsConfig("schema.yaml")
// 	if err != nil {
// 		log.Fatalf("Failed to load plugins configuration: %v", err)
// 	}

// 	pm, err := New(pluginsConfig)
// 	if err != nil {
// 		log.Fatalf("Failed to create PluginManager: %v", err)
// 	}
// 	schemaPath := pluginsConfig.ValidationPlugin.Config.Schema

// 	payloadData, err := ioutil.ReadFile("schemas/payload.json")
// 	if err != nil {
// 		log.Fatalf("Failed to read payload data: %v", err)
// 	}

// 	validator, err := pm.validatorProvider.New(schemaPath)
// 	if err != nil {
// 		log.Fatalf("Failed to get validator: %v", err)
// 	}

// 	err = validator.Validate(context.Background(), payloadData)
// 	if err != nil {
// 		log.Printf("Validation failed: %v", err)
// 	} else {
// 		log.Println("Validation succeeded!")
// 	}
// }
