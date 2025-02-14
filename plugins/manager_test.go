package main

import (
	"testing"
)

func TestLoadPluginsConfig(t *testing.T) {
	// Test loading a valid configuration
	config, err := loadPluginsConfig("config.yaml")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if config.Plugins.ValidationPlugin.ID == "" {
		t.Fatal("Expected validation_plugin ID to be set")
	}
}

// func TestNewPluginManager(t *testing.T) {
// 	// Load the configuration
// 	config, err := loadPluginsConfig("config.yaml")
// 	if err != nil {
// 		t.Fatalf("Failed to load plugins configuration: %v", err)
// 	}

// 	// Create a new PluginManager
// 	pm, err := New(config)
// 	if err != nil {
// 		t.Fatalf("Failed to create PluginManager: %v", err)
// 	}

// 	if pm == nil {
// 		t.Fatal("Expected PluginManager to be created")
// 	}
// }
