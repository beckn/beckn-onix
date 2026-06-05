package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/keymanager"
)

type mockRegistry struct {
	LookupFunc func(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error)
}

func (m *mockRegistry) Lookup(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
	if m.LookupFunc != nil {
		return m.LookupFunc(ctx, sub)
	}
	return []model.Subscription{
		{
			Subscriber: model.Subscriber{
				SubscriberID: sub.SubscriberID,
				URL:          "https://mock.registry/subscriber",
				Type:         "BPP",
				Domain:       "retail",
			},
			KeyID:            sub.KeyID,
			SigningPublicKey: "mock-signing-public-key",
			EncrPublicKey:    "mock-encryption-public-key",
			ValidFrom:        time.Now().Add(-time.Hour),
			ValidUntil:       time.Now().Add(time.Hour),
			Status:           "SUBSCRIBED",
			Created:          time.Now().Add(-2 * time.Hour),
			Updated:          time.Now(),
			Nonce:            "mock-nonce",
		},
	}, nil
}

type mockCache struct{}

func (m *mockCache) Get(ctx context.Context, key string) (string, error) {
	return "", nil
}
func (m *mockCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return nil
}
func (m *mockCache) Clear(ctx context.Context) error {
	return nil
}

func (m *mockCache) Delete(ctx context.Context, key string) error {
	return nil
}

func TestNewSuccess(t *testing.T) {
	// Setup dummy implementations and variables
	ctx := context.Background()
	cache := &mockCache{}
	registry := &mockRegistry{}
	cfg := map[string]string{
		"vaultAddr": "http://dummy-vault",
		"kvVersion": "2",
	}

	cleanupCalled := false
	fakeCleanup := func() error {
		cleanupCalled = true
		return nil
	}

	newKeyManagerFunc = func(ctx context.Context, cache definition.Cache, registry definition.RegistryLookup, cfg *keymanager.Config) (*keymanager.KeyMgr, func() error, error) {
		// return a mock struct pointer of *keymanager.KeyMgr or a stub instance
		return &keymanager.KeyMgr{}, fakeCleanup, nil
	}

	// Create provider and call New
	provider := &keyManagerProvider{}
	km, cleanup, err := provider.New(ctx, cache, registry, cfg)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if km == nil {
		t.Fatal("Expected non-nil KeyManager instance")
	}
	if cleanup == nil {
		t.Fatal("Expected non-nil cleanup function")
	}

	// Call cleanup and check if it behaves correctly
	if err := cleanup(); err != nil {
		t.Fatalf("Expected no error from cleanup, got %v", err)
	}
	if !cleanupCalled {
		t.Error("Expected cleanup function to be called")
	}
}

func TestNewFailure(t *testing.T) {
	// Setup dummy variables
	ctx := context.Background()
	cache := &mockCache{}
	registry := &mockRegistry{}
	cfg := map[string]string{
		"vaultAddr": "http://dummy-vault",
		"kvVersion": "2",
	}

	newKeyManagerFunc = func(ctx context.Context, cache definition.Cache, registry definition.RegistryLookup, cfg *keymanager.Config) (*keymanager.KeyMgr, func() error, error) {
		return nil, nil, fmt.Errorf("some error")
	}

	provider := &keyManagerProvider{}
	km, cleanup, err := provider.New(ctx, cache, registry, cfg)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if km != nil {
		t.Error("Expected nil KeyManager on error")
	}
	if cleanup != nil {
		t.Error("Expected nil cleanup function on error")
	}
}
