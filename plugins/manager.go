package main

import (
	"beckn-onix/plugins/plugin_definition"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"plugin"
	"strings"

	"gopkg.in/yaml.v2"
)

type PluginConfig struct {
	Plugins Plugins `yaml:"plugins"`
}

type Plugins struct {
	ValidationPlugin ValidationPlugin `yaml:"validation_plugin"`
}

type ValidationPlugin struct {
	ID         string        `yaml:"id"`
	Config     PluginDetails `yaml:"config"`
	PluginPath string        `yaml:"plugin_path"`
}

type PluginDetails struct {
	Schema string `yaml:"schema_dir"`
}

type Payload struct {
	Context struct {
		Domain  string `json:"domain"`
		Version string `json:"version"`
	} `json:"context"`
}

// PluginManager manages the loading and execution of plugins.
type PluginManager struct {
	validatorProvider plugin_definition.ValidatorProvider
}

// NewValidatorProvider initializes the PluginManager with the given configuration.
func NewValidatorProvider(pluginsConfig PluginConfig) (*PluginManager, error) {
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

	vpSymbol, err := p.Lookup("GetProvider")
	if err != nil {
		return nil, err
	}

	getProviderFunc, ok := vpSymbol.(func() plugin_definition.ValidatorProvider)
	if !ok {
		return nil, fmt.Errorf("failed to cast to *plugin_definition.ValidatorProvider")
	}

	validatorProvider := getProviderFunc()

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

func main() {
	pluginsConfig, err := loadPluginsConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load plugins configuration: %v", err)
	}

	pm, err := NewValidatorProvider(pluginsConfig)
	if err != nil {
		log.Fatalf("Failed to create PluginManager: %v", err)
	}

	schemaDir := pluginsConfig.Plugins.ValidationPlugin.Config.Schema

	err = pm.validatorProvider.Initialize(schemaDir)
	if err != nil {
		log.Fatalf("Failed to initialize validator provider: %v", err)
	}

	payloadData, err := ioutil.ReadFile("test/payload.json")
	if err != nil {
		log.Fatalf("Failed to read payload data: %v", err)
	}

	var payload Payload
	if err := json.Unmarshal(payloadData, &payload); err != nil {
		log.Fatalf("Failed to unmarshal payload: %v", err)
	}

	// Construct the schema file name based on domain and version
	schemaFileName := fmt.Sprintf("%s_%s.json", strings.ToLower(payload.Context.Domain), strings.ToLower(payload.Context.Version))

	// Get the validator for the specific schema
	validator, err := pm.validatorProvider.Get(schemaFileName)
	if err != nil {
		log.Fatalf("Failed to get validator: %v", err)
	}
	fmt.Println("printing validator :", validator)

	// Validate the payload against the schema
	if err := validator.Validate(context.Background(), payloadData); err != nil {
		log.Printf("Validation failed: %v", err)
	} else {
		log.Println("Validation succeeded!")
	}
}
