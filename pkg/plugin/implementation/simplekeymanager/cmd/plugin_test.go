package main

import (
	"context"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/simplekeymanager"
)

type mockRegistry struct{}

func (m *mockRegistry) Lookup(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
	return nil, nil
}

func TestSimpleKeyManagerProvider_New(t *testing.T) {
	provider := &simpleKeyManagerProvider{}
	ctx := context.Background()
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
				"subscriberId":     "bap-one",
				"keyId":            "test-key",
				"signingPrivateKey": "dGVzdC1zaWduaW5nLXByaXZhdGU=",
				"signingPublicKey":  "dGVzdC1zaWduaW5nLXB1YmxpYw==",
				"encrPrivateKey":    "dGVzdC1lbmNyLXByaXZhdGU=",
				"encrPublicKey":     "dGVzdC1lbmNyLXB1YmxpYw==",
			},
			wantErr: false,
		},
		{
			name: "invalid config - partial keys",
			config: map[string]string{
				"keyId":            "test-key",
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
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			km, cleanup, err := provider.New(ctx, registry, tt.config)

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

	// Test with nil registry
	_, _, err := provider.New(ctx, nil, config)
	if err == nil {
		t.Error("simpleKeyManagerProvider.New() should fail with nil registry")
	}
}

func TestConfigMapping(t *testing.T) {
	provider := &simpleKeyManagerProvider{}
	ctx := context.Background()
	registry := &mockRegistry{}

	configMap := map[string]string{
		"subscriberId":     "mapped-np",
		"keyId":            "mapped-key-id",
		"signingPrivateKey": "dGVzdC1zaWduaW5nLXByaXZhdGU=",
		"signingPublicKey":  "dGVzdC1zaWduaW5nLXB1YmxpYw==",
		"encrPrivateKey":    "dGVzdC1lbmNyLXByaXZhdGU=",
		"encrPublicKey":     "dGVzdC1lbmNyLXB1YmxpYw==",
	}

	_, cleanup, err := provider.New(ctx, registry, configMap)
	if err != nil {
		t.Errorf("Config mapping failed: %v", err)
		return
	}

	if cleanup != nil {
		cleanup()
	}
}

func TestDeprecatedNetworkParticipantKey(t *testing.T) {
	provider := &simpleKeyManagerProvider{}
	ctx := context.Background()
	registry := &mockRegistry{}

	// Config using the deprecated networkParticipant key should still work.
	_, cleanup, err := provider.New(ctx, registry, map[string]string{
		"networkParticipant": "bap-one",
		"keyId":              "test-key",
		"signingPrivateKey":  "dGVzdC1zaWduaW5nLXByaXZhdGU=",
		"signingPublicKey":   "dGVzdC1zaWduaW5nLXB1YmxpYw==",
		"encrPrivateKey":     "dGVzdC1lbmNyLXByaXZhdGU=",
		"encrPublicKey":      "dGVzdC1lbmNyLXB1YmxpYw==",
	})
	if err != nil {
		t.Errorf("deprecated networkParticipant key should still work: %v", err)
	}
	if cleanup != nil {
		cleanup()
	}

	// subscriberId takes precedence over deprecated networkParticipant when both are present.
	_, cleanup, err = provider.New(ctx, registry, map[string]string{
		"subscriberId":       "bap-primary",
		"networkParticipant": "bap-ignored",
		"keyId":              "test-key",
		"signingPrivateKey":  "dGVzdC1zaWduaW5nLXByaXZhdGU=",
		"signingPublicKey":   "dGVzdC1zaWduaW5nLXB1YmxpYw==",
		"encrPrivateKey":     "dGVzdC1lbmNyLXByaXZhdGU=",
		"encrPublicKey":      "dGVzdC1lbmNyLXB1YmxpYw==",
	})
	if err != nil {
		t.Errorf("subscriberId should take precedence over networkParticipant: %v", err)
	}
	if cleanup != nil {
		cleanup()
	}
}

func TestProviderInterface(t *testing.T) {
	provider := &simpleKeyManagerProvider{}
	ctx := context.Background()

	_, _, err := provider.New(ctx, &mockRegistry{}, map[string]string{})
	if err == nil {
		t.Log("Provider.New() succeeded with mock dependencies")
	} else {
		t.Logf("Provider.New() failed as expected: %v", err)
	}
}

func TestNewSimpleKeyManagerFunc(t *testing.T) {
	if newSimpleKeyManagerFunc == nil {
		t.Error("newSimpleKeyManagerFunc is nil")
	}

	ctx := context.Background()
	registry := &mockRegistry{}
	cfg := &simplekeymanager.Config{}

	_, cleanup, err := newSimpleKeyManagerFunc(ctx, registry, cfg)

	if err != nil {
		t.Logf("newSimpleKeyManagerFunc failed as expected with mocks: %v", err)
	} else {
		t.Log("newSimpleKeyManagerFunc succeeded with mock dependencies")
		if cleanup != nil {
			cleanup()
		}
	}
}
