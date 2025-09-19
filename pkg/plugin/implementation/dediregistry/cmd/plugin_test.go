package main

import (
	"context"
	"testing"
)

func TestDediRegistryProvider_New(t *testing.T) {
	ctx := context.Background()
	provider := dediRegistryProvider{}

	config := map[string]string{
		"baseURL":      "https://test.com",
		"apiKey":       "test-key",
		"namespaceID":  "test-namespace",
		"registryName": "test-registry",
		"recordName":   "test-record",
		"timeout":      "30",
	}

	dediRegistry, closer, err := provider.New(ctx, config)
	if err != nil {
		t.Errorf("New() error = %v", err)
		return
	}

	if dediRegistry == nil {
		t.Error("New() returned nil dediRegistry")
	}

	if closer == nil {
		t.Error("New() returned nil closer")
	}

	// Test cleanup
	if err := closer(); err != nil {
		t.Errorf("closer() error = %v", err)
	}
}

func TestDediRegistryProvider_New_NilContext(t *testing.T) {
	provider := dediRegistryProvider{}
	config := map[string]string{}

	_, _, err := provider.New(nil, config)
	if err == nil {
		t.Error("New() with nil context should return error")
	}
}