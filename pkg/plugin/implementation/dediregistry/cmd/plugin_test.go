package main

import (
	"context"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/dediregistry"
)

func TestDediRegistryProvider_ParseConfig(t *testing.T) {
	provider := dediRegistryProvider{}
	ctx := context.Background()

	cfg := provider.parseConfig(ctx, map[string]string{
		"url":            "https://test.com/dedi",
		"registryName":   "subscribers.beckn.one",
		"timeout":        "30",
		"retry_max":      "5",
		"retry_wait_min": "100ms",
		"retry_wait_max": "2s",
	})

	if cfg.URL != "https://test.com/dedi" {
		t.Fatalf("expected URL to be parsed, got %q", cfg.URL)
	}
	if cfg.RegistryName != "subscribers.beckn.one" {
		t.Fatalf("expected RegistryName to be parsed, got %q", cfg.RegistryName)
	}
	if cfg.Timeout != 30 {
		t.Fatalf("expected Timeout 30, got %d", cfg.Timeout)
	}
	if cfg.RetryMax != 5 {
		t.Fatalf("expected RetryMax 5, got %d", cfg.RetryMax)
	}
	if cfg.RetryWaitMin != 100*time.Millisecond {
		t.Fatalf("expected RetryWaitMin 100ms, got %v", cfg.RetryWaitMin)
	}
	if cfg.RetryWaitMax != 2*time.Second {
		t.Fatalf("expected RetryWaitMax 2s, got %v", cfg.RetryWaitMax)
	}
}

func TestDediRegistryProvider_New_ForwardsRetryConfig(t *testing.T) {
	provider := dediRegistryProvider{}
	originalNewDediRegistryFunc := newDediRegistryFunc
	t.Cleanup(func() {
		newDediRegistryFunc = originalNewDediRegistryFunc
	})

	var captured *dediregistry.Config
	newDediRegistryFunc = func(ctx context.Context, cfg *dediregistry.Config) (*dediregistry.DeDiRegistryClient, func() error, error) {
		captured = cfg
		return new(dediregistry.DeDiRegistryClient), func() error { return nil }, nil
	}

	config := map[string]string{
		"url":            "https://test.com/dedi",
		"registryName":   "subscribers.beckn.one",
		"timeout":        "30",
		"retry_max":      "5",
		"retry_wait_min": "100ms",
		"retry_wait_max": "2s",
	}

	_, closer, err := provider.New(context.Background(), config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if closer == nil {
		t.Fatal("expected closer to be non-nil")
	}
	if err := closer(); err != nil {
		t.Fatalf("closer() error = %v", err)
	}
	if captured == nil {
		t.Fatal("expected config to be forwarded to client constructor")
	}
	if captured.RetryMax != 5 {
		t.Fatalf("expected RetryMax 5, got %d", captured.RetryMax)
	}
	if captured.RetryWaitMin != 100*time.Millisecond {
		t.Fatalf("expected RetryWaitMin 100ms, got %v", captured.RetryWaitMin)
	}
	if captured.RetryWaitMax != 2*time.Second {
		t.Fatalf("expected RetryWaitMax 2s, got %v", captured.RetryWaitMax)
	}
}

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
	got := parseAllowedNetworkIDs("commerce-network.org/prod, local-commerce.org/production, ,")
	want := []string{
		"commerce-network.org/prod",
		"local-commerce.org/production",
	}

	if len(got) != len(want) {
		t.Fatalf("expected %d allowed network IDs, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("expected allowedNetworkIDs[%d] to preserve input order as %q, got %q", i, want[i], got[i])
		}
	}
}

func TestResolveAllowedNetworkIDs_DeprecatedAllowedParentNamespacesErrorsWithoutAllowedNetworkIDs(t *testing.T) {
	config := map[string]string{
		"allowedParentNamespaces": "commerce-network.org/prod, local-commerce.org/production",
	}

	got, err := resolveAllowedNetworkIDs(config)
	if err == nil {
		t.Fatal("expected error when only allowedParentNamespaces is configured")
	}
	if got != nil {
		t.Fatalf("expected nil allowed network IDs on error, got %#v", got)
	}
}

func TestResolveAllowedNetworkIDs_AllowedNetworkIDsTakesPrecedence(t *testing.T) {
	config := map[string]string{
		"url":                     "https://test.com/dedi",
		"registryName":            "subscribers.beckn.one",
		"allowedParentNamespaces": "deprecated-network.org/legacy",
		"allowedNetworkIDs":       "commerce-network.org/prod, local-commerce.org/production",
	}

	got, err := resolveAllowedNetworkIDs(config)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := []string{
		"commerce-network.org/prod",
		"local-commerce.org/production",
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

func TestDediRegistryProvider_New_DeprecatedAllowedParentNamespacesErrorsWithoutAllowedNetworkIDs(t *testing.T) {
	ctx := context.Background()
	provider := dediRegistryProvider{}

	config := map[string]string{
		"url":                     "https://test.com/dedi",
		"registryName":            "subscribers.beckn.one",
		"allowedParentNamespaces": "commerce-network.org",
	}

	_, _, err := provider.New(ctx, config)
	if err == nil {
		t.Fatal("expected New() to error when only allowedParentNamespaces is configured")
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
