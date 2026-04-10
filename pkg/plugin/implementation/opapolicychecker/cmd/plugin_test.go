package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProviderNewSuccess(t *testing.T) {
	provider := provider{}
	dir := t.TempDir()
	policyPath := filepath.Join("..", "testdata", "example.rego")
	configPath := filepath.Join(dir, "network-policies.yaml")
	if err := os.WriteFile(configPath, []byte("networkPolicies:\n  default:\n    type: file\n    location: "+policyPath+"\n    query: data.policy.result\n"), 0644); err != nil {
		t.Fatalf("failed to write network policy config: %v", err)
	}
	config := map[string]string{
		"networkPolicyConfig": configPath,
	}

	checker, closer, err := provider.New(context.Background(), config)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	if checker == nil {
		t.Fatal("New() returned nil checker")
	}
	if closer == nil {
		t.Fatal("New() returned nil closer")
	}

	closer()
}

func TestProviderNewFailure(t *testing.T) {
	provider := provider{}

	_, _, err := provider.New(context.Background(), map[string]string{
		"networkPolicyConfig": "",
	})
	if err == nil {
		t.Fatal("expected error when required config is missing")
	}
}
