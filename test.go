package main

import (
	"context"
	"fmt"
	"plugin"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

func main() {
	ctx := context.Background()

	// Path to the compiled plugin .so file
	// Adjust the path accordingly
	pluginPath := "pkg/plugin/implementation/cache.so"

	// Open the plugin
	p, err := plugin.Open(pluginPath)
	if err != nil {
		fmt.Printf("Failed to open plugin: %v\n", err)
		return
	}

	// Lookup the 'Provider' symbol
	symProvider, err := p.Lookup("Provider")
	if err != nil {
		fmt.Printf("Failed to lookup 'Provider': %v\n", err)
		return
	}

	// Assert that the symbol implements the CacheProvider interface
	provider, ok := symProvider.(definition.CacheProvider)
	if !ok {
		fmt.Println("Plugin 'Provider' does not implement CacheProvider interface.")
		return
	}
	fmt.Println("Successfully loaded CacheProvider plugin.")

	// Setup config
	config := map[string]string{
		"addr": "localhost:6379", // Adjust to your Redis instance
	}

	// Create a new cache instance using the plugin provider
	cacheInstance, cleanup, err := provider.New(ctx, config)
	if err != nil {
		fmt.Printf("Error creating cache instance: %v\n", err)
		return
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	fmt.Println("Cache instance created successfully.")

	// Test Set
	key := "plugin_test_key"
	value := "plugin_test_value"
	ttl := 10 * time.Second

	err = cacheInstance.Set(ctx, key, value, ttl)
	if err != nil {
		fmt.Printf("Set failed: %v\n", err)
		return
	}
	fmt.Println("Set operation successful.")

	// Test Get
	got, err := cacheInstance.Get(ctx, key)
	if err != nil {
		fmt.Printf("Get failed: %v\n", err)
		return
	}
	fmt.Printf("Got value: %s\n", got)

	// Test Delete
	err = cacheInstance.Delete(ctx, key)
	if err != nil {
		fmt.Printf("Delete failed: %v\n", err)
		return
	}
	fmt.Println("Delete operation successful.")

	// Test Clear
	// Add a key to test Clear
	err = cacheInstance.Set(ctx, "another_plugin_key", "another_plugin_value", ttl)
	if err != nil {
		fmt.Printf("Set for clear test failed: %v\n", err)
		return
	}
	fmt.Println("Added key for clear test.")

	err = cacheInstance.Clear(ctx)
	if err != nil {
		fmt.Printf("Clear failed: %v\n", err)
		return
	}
	fmt.Println("Clear operation successful.")
}
