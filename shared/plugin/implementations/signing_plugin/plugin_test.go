package main

import (
	"context"
	"errors"
	"testing"

	plugins "plugins/shared/plugin"
	signer "plugins/shared/plugin/implementations/signing_plugin/Signer"

	"github.com/stretchr/testify/assert"
)

// TestParseConfigSuccess tests parsing a valid configuration.
func TestParseConfigSuccess(t *testing.T) {
	config := map[string]string{"ttl": "3600"}
	expectedConfig := signer.SigningConfig{TTL: 3600}

	parsedConfig, err := parseConfig(config)

	assert.NoError(t, err)
	assert.Equal(t, expectedConfig, parsedConfig)
}

// TestParseConfigMissingTTL tests parsing a configuration where TTL is missing.
func TestParseConfigMissingTTL(t *testing.T) {
	config := map[string]string{}

	_, err := parseConfig(config)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ttl not found in config")
}

// TestParseConfigInvalidTTL tests parsing a configuration with an invalid TTL value.
func TestParseConfigInvalidTTL(t *testing.T) {
	config := map[string]string{"ttl": "invalid"}

	_, err := parseConfig(config)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ttl value")
}

// MockSigner is a mock implementation of plugins.Signer for testing.
type MockSigner struct{}

func (m *MockSigner) Sign(ctx context.Context, body []byte, privateKeyBase64 string) (string, error) {
	return "mocked_signature", nil
}

func (m *MockSigner) Close() error {
	return nil
}

// MockSignerFactory is a helper function to simulate signer creation.
func MockSignerFactory(ctx context.Context, cfg signer.SigningConfig) (plugins.Signer, error) {
	if cfg.TTL <= 0 {
		return nil, errors.New("invalid signer config")
	}
	return &MockSigner{}, nil
}

// TestSignerProviderSuccess tests successful creation of a signer.
func TestSignerProviderSuccess(t *testing.T) {
	config := map[string]string{"ttl": "3600"}
	provider := SignerProvider{}

	signerInstance, err := provider.New(context.Background(), config)

	assert.NoError(t, err)
	assert.NotNil(t, signerInstance)
}

// TestSignerProviderInvalidConfig tests signer creation failure due to invalid config.
func TestSignerProviderInvalidConfig(t *testing.T) {
	config := map[string]string{"ttl": "invalid"}
	provider := SignerProvider{}

	signerInstance, err := provider.New(context.Background(), config)

	assert.Error(t, err)
	assert.Nil(t, signerInstance)
	assert.Contains(t, err.Error(), "invalid config")
}

// TestSignerProviderMissingTTL tests signer creation failure due to missing TTL.
func TestSignerProviderMissingTTL(t *testing.T) {
	config := map[string]string{} // No TTL key
	provider := SignerProvider{}

	signerInstance, err := provider.New(context.Background(), config)

	assert.Error(t, err)
	assert.Nil(t, signerInstance)
	assert.Contains(t, err.Error(), "invalid config")
}
