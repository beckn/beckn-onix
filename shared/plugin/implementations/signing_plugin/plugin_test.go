package main

import (
	"context"
	"testing"

	plugins "beckn_onix/shared/plugin"

	"github.com/stretchr/testify/assert"
)

// TestSignerProvider_New_Success ensures the provider successfully creates a new signer instance.
func TestSignerProviderSuccess(t *testing.T) {
	provider := SignerProvider{}
	config := map[string]string{} // Since SigningConfig has no fields, passing an empty config

	signerInstance, err := provider.New(context.Background(), config)

	assert.NoError(t, err, "Expected no error when creating a signer instance")
	assert.NotNil(t, signerInstance, "Signer instance should not be nil")
	assert.Implements(t, (*plugins.Signer)(nil), signerInstance, "Signer instance should implement the Signer interface")
}

// TestSignerProvider_New_InvalidConfig ensures that an invalid config does not break the signer initialization.
func TestSignerProviderInvalidConfig(t *testing.T) {
	provider := SignerProvider{}
	invalidConfig := map[string]string{"unexpected_key": "some_value"} // Unexpected config parameters

	signerInstance, err := provider.New(context.Background(), invalidConfig)

	assert.NoError(t, err, "Unexpected config should not cause an error if ignored")
	assert.NotNil(t, signerInstance, "Signer instance should still be created")
}

// TestSignerProvider_New_NilContext ensures the provider can handle a nil context gracefully.
func TestSignerProviderNilContext(t *testing.T) {
	provider := SignerProvider{}
	config := map[string]string{}

	signerInstance, err := provider.New(nil, config) // Passing nil context

	assert.NoError(t, err, "Nil context should not cause an error")
	assert.NotNil(t, signerInstance, "Signer instance should still be created")
}

// TestSignerProvider_New_EmptyConfig ensures that an empty config does not cause issues.
func TestSignerProviderEmptyConfig(t *testing.T) {
	provider := SignerProvider{}
	emptyConfig := map[string]string{}

	signerInstance, err := provider.New(context.Background(), emptyConfig)

	assert.NoError(t, err, "Empty config should not cause an error")
	assert.NotNil(t, signerInstance, "Signer instance should still be created")
}

// TestSignerProvider_New_HandlesEdgeCases ensures no panic occurs even with unexpected input.
func TestSignerProviderHandlesEdgeCases(t *testing.T) {
	provider := SignerProvider{}

	edgeCases := []map[string]string{
		nil,                     // Nil config map
		{},                      // Empty map
		{"ttl": ""},             // Empty string value
		{"ttl": "-100"},         // Negative value
		{"ttl": "not_a_number"}, // Non-numeric string
	}

	for _, config := range edgeCases {
		t.Run("Testing edge case", func(t *testing.T) {
			signerInstance, err := provider.New(context.Background(), config)
			assert.NoError(t, err, "Edge case should not cause an error")
			assert.NotNil(t, signerInstance, "Signer instance should still be created")
		})
	}
}
