package main

import (
	"context"
	"testing"
)

func TestProviderNew_ReturnsStep(t *testing.T) {
	step, closer, err := (provider{}).New(context.Background(), map[string]string{
		"role":         "bap",
		"mappingsFile": "../testdata/mappings.yaml",
	})
	if err != nil {
		t.Fatalf("provider.New returned error: %v", err)
	}
	if closer != nil {
		t.Fatalf("expected nil closer, got non-nil")
	}
	if step == nil {
		t.Fatalf("expected step, got nil")
	}
}
