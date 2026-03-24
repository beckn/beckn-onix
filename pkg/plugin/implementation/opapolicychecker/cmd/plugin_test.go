package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestProviderNewSuccess(t *testing.T) {
	provider := provider{}
	config := map[string]string{
		"type":     "file",
		"location": filepath.Join("..", "testdata", "example.rego"),
		"query":    "data.policy.result",
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
		"type":  "file",
		"query": "data.policy.result",
	})
	if err == nil {
		t.Fatal("expected error when required config is missing")
	}
}
