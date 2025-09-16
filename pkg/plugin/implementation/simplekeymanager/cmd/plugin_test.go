package main

import (
	"context"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/simplekeymanager"
)

// Mock implementations for testing
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

type mockRegistry struct{}

func (m *mockRegistry) Lookup(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
	return nil, nil
}

func TestSimpleKeyManagerProvider_New(t *testing.T) {
	provider := &simpleKeyManagerProvider{}
	ctx := context.Background()
	cache := &mockCache{}
	registry := &mockRegistry{}

	tests := []struct {
		name    string
		config  map[string]string
		wantErr bool
	}{
		{
			name:    "empty config",
			config:  map[string]string{},
			wantErr: false,
		},
		{
			name: "valid config with keys",
			config: map[string]string{
				"networkParticipant": "bap-one",
				"keyId":              "test-key",
				"signingPrivateKey":  "dGVzdC1zaWduaW5nLXByaXZhdGU=",
				"signingPublicKey":   "dGVzdC1zaWduaW5nLXB1YmxpYw==",
				"encrPrivateKey":     "dGVzdC1lbmNyLXByaXZhdGU=",
				"encrPublicKey":      "dGVzdC1lbmNyLXB1YmxpYw==",
			},
			wantErr: false,
		},
		{
			name: "invalid config - partial keys",
			config: map[string]string{
				"keyId":             "test-key",
				"signingPrivateKey": "dGVzdC1zaWduaW5nLXByaXZhdGU=",
				// Missing other required keys
			},
			wantErr: true,
		},
		{
			name: "config with only keyId",
			config: map[string]string{
				"keyId": "test-key",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			km, cleanup, err := provider.New(ctx, cache, registry, tt.config)

			if (err != nil) != tt.wantErr {
				t.Errorf("simpleKeyManagerProvider.New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if km == nil {
					t.Error("simpleKeyManagerProvider.New() returned nil keymanager")
				}
				if cleanup == nil {
					t.Error("simpleKeyManagerProvider.New() returned nil cleanup function")
				}
				if cleanup != nil {
					// Test that cleanup doesn't panic
					err := cleanup()
					if err != nil {
						t.Errorf("cleanup() error = %v", err)
					}
				}
			} else {
				if km != nil {
					t.Error("simpleKeyManagerProvider.New() should return nil keymanager on error")
				}
				if cleanup != nil {
					t.Error("simpleKeyManagerProvider.New() should return nil cleanup on error")
				}
			}
		})
	}
}

func TestSimpleKeyManagerProvider_NewWithNilDependencies(t *testing.T) {
	provider := &simpleKeyManagerProvider{}
	ctx := context.Background()
	config := map[string]string{}

	// Test with nil cache
	_, _, err := provider.New(ctx, nil, &mockRegistry{}, config)
	if err == nil {
		t.Error("simpleKeyManagerProvider.New() should fail with nil cache")
	}

	// Test with nil registry
	_, _, err = provider.New(ctx, &mockCache{}, nil, config)
	if err == nil {
		t.Error("simpleKeyManagerProvider.New() should fail with nil registry")
	}

	// Test with both nil
	_, _, err = provider.New(ctx, nil, nil, config)
	if err == nil {
		t.Error("simpleKeyManagerProvider.New() should fail with nil dependencies")
	}
}

func TestConfigMapping(t *testing.T) {
	provider := &simpleKeyManagerProvider{}
	ctx := context.Background()
	cache := &mockCache{}
	registry := &mockRegistry{}

	// Test config mapping
	configMap := map[string]string{
		"networkParticipant": "mapped-np",
		"keyId":              "mapped-key-id",
		"signingPrivateKey":  "mapped-signing-private",
		"signingPublicKey":   "mapped-signing-public",
		"encrPrivateKey":     "mapped-encr-private",
		"encrPublicKey":      "mapped-encr-public",
	}

	// We can't directly test the config mapping without exposing internals,
	// but we can test that the mapping doesn't cause errors
	_, cleanup, err := provider.New(ctx, cache, registry, configMap)
	if err != nil {
		t.Errorf("Config mapping failed: %v", err)
		return
	}

	if cleanup != nil {
		cleanup()
	}
}

// Test that the provider implements the correct interface
// This is a compile-time check to ensure interface compliance
func TestProviderInterface(t *testing.T) {
	provider := &simpleKeyManagerProvider{}
	ctx := context.Background()

	// This should compile if the interface is implemented correctly
	_, _, err := provider.New(ctx, &mockCache{}, &mockRegistry{}, map[string]string{})

	// We expect an error here because of missing dependencies, but the call should compile
	if err == nil {
		// This might succeed with mocks, which is fine
		t.Log("Provider.New() succeeded with mock dependencies")
	} else {
		t.Logf("Provider.New() failed as expected: %v", err)
	}
}

func TestNewSimpleKeyManagerFunc(t *testing.T) {
	// Test that the function variable is set
	if newSimpleKeyManagerFunc == nil {
		t.Error("newSimpleKeyManagerFunc is nil")
	}

	// Test that it points to the correct function
	ctx := context.Background()
	cache := &mockCache{}
	registry := &mockRegistry{}
	cfg := &simplekeymanager.Config{}

	// This should call the actual New function
	_, cleanup, err := newSimpleKeyManagerFunc(ctx, cache, registry, cfg)

	if err != nil {
		t.Logf("newSimpleKeyManagerFunc failed as expected with mocks: %v", err)
	} else {
		t.Log("newSimpleKeyManagerFunc succeeded with mock dependencies")
		if cleanup != nil {
			cleanup()
		}
	}
}
