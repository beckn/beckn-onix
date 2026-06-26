package main

import (
	"context"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// stubLoader is a no-op ManifestLoader for smoke-testing provider construction.
type stubLoader struct{}

func (stubLoader) GetByNetworkID(context.Context, string) (*model.ManifestDocument, error) {
	return nil, nil
}
func (stubLoader) GetBySubscriberID(context.Context, string) (*model.ManifestDocument, error) {
	return nil, nil
}
func (stubLoader) GetByMetadata(context.Context, model.ManifestMetadata) (*model.ManifestDocument, error) {
	return nil, nil
}

func TestProvider_SymbolType(t *testing.T) {
	// Verify the exported Provider symbol satisfies SchemaVersionMediatorProvider.
	// This mirrors the type assertion the plugin manager performs at runtime.
	var _ definition.SchemaVersionMediatorProvider = Provider
}

func TestProvider_New_ReturnsMediator(t *testing.T) {
	mediator, closer, err := Provider.New(context.Background(), stubLoader{}, map[string]string{
		"nodeId": "nfh.global/subscribers.beckn.one/bap.example.com",
	})
	if err != nil {
		t.Fatalf("Provider.New() error = %v", err)
	}
	if mediator == nil {
		t.Fatal("expected non-nil SchemaVersionMediator")
	}
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	if err := closer(); err != nil {
		t.Fatalf("closer() error = %v", err)
	}
}

func TestProvider_New_InvalidAction_ReturnsError(t *testing.T) {
	_, _, err := Provider.New(context.Background(), stubLoader{}, map[string]string{
		"nodeId": "nfh.global/subscribers.beckn.one/bap.example.com",
		"action": "passThrough",
	})
	if err == nil {
		t.Fatal("expected error for invalid action=passThrough, got nil")
	}
}
