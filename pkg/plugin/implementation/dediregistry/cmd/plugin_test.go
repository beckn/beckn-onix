package main

import (
	"context"
	"testing"
)

func TestDediRegistryProvider_New(t *testing.T) {
	ctx := context.Background()
	provider := dediRegistryProvider{}

	config := map[string]string{
		"url":          "https://test.com/dedi",
		"registryName": "subscribers.beckn.one",
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

func TestDediRegistryProvider_New_InvalidConfig(t *testing.T) {
	ctx := context.Background()
	provider := dediRegistryProvider{}

	tests := []struct {
		name   string
		config map[string]string
	}{
		{
			name:   "missing url",
			config: map[string]string{"registryName": "subscribers.beckn.one", "timeout": "30"},
		},
		{
			name:   "missing registryName",
			config: map[string]string{"url": "https://test.com/dedi", "timeout": "30"},
		},
		{
			name:   "empty config",
			config: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := provider.New(ctx, tt.config)
			if err == nil {
				t.Errorf("New() with %s should return error", tt.name)
			}
		})
	}
}

func TestDediRegistryProvider_New_InvalidTimeout(t *testing.T) {
	ctx := context.Background()
	provider := dediRegistryProvider{}

	config := map[string]string{
		"url":          "https://test.com/dedi",
		"registryName": "subscribers.beckn.one",
		"timeout":      "invalid",
	}

	// Invalid timeout should be ignored, not cause error
	dediRegistry, closer, err := provider.New(ctx, config)
	if err != nil {
		t.Errorf("New() with invalid timeout should not return error, got: %v", err)
	}
	if dediRegistry == nil {
		t.Error("New() should return valid registry even with invalid timeout")
	}
	if closer != nil {
		closer()
	}
}

func TestParseAllowedNetworkIDs(t *testing.T) {
	got := parseAllowedNetworkIDs("commerce-network/subscriber-references, retail-network/subscriber-references, ,")
	want := []string{
		"commerce-network/subscriber-references",
		"retail-network/subscriber-references",
	}

	if len(got) != len(want) {
		t.Fatalf("expected %d allowed network IDs, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("expected allowedNetworkIDs[%d] = %q, got %q", i, want[i], got[i])
		}
	}
}

func TestDediRegistryProvider_New_NilContext(t *testing.T) {
	provider := dediRegistryProvider{}

	config := map[string]string{
		"url":          "https://test.com/dedi",
		"registryName": "subscribers.beckn.one",
	}

	_, _, err := provider.New(nil, config)
	if err == nil {
		t.Error("New() with nil context should return error")
	}
	if err.Error() != "context cannot be nil" {
		t.Errorf("Expected specific error message, got %v", err)
	}
}
