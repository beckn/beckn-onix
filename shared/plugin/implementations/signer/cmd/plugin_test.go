package main

import (
	"context"
	"testing"
)

// TestSignerProviderSuccess ensures the provider successfully creates a new signer instance.
func TestSignerProviderSuccess(t *testing.T) {
	provider := SignerProvider{}
	config := map[string]string{} // Since SigningConfig has no fields, passing an empty config

	signerInstance, err := provider.New(context.Background(), config)

	if err != nil {
		t.Fatalf("Expected no error when creating a signer instance, but got: %v", err)
	}
	if signerInstance == nil {
		t.Fatal("Signer instance should not be nil")
	}
	if signerInstance == nil {
		t.Fatal("Expected signer instance to be non-nil")
	}

}

// TestSignerProviderInvalidConfig ensures that an invalid config does not break the signer initialization.
func TestSignerProviderInvalidConfig(t *testing.T) {
	provider := SignerProvider{}
	invalidConfig := map[string]string{"unexpected_key": "some_value"} // Unexpected config parameters

	signerInstance, err := provider.New(context.Background(), invalidConfig)

	if err != nil {
		t.Fatalf("Unexpected config should not cause an error, but got: %v", err)
	}
	if signerInstance == nil {
		t.Fatal("Signer instance should still be created")
	}
}

// TestSignerProviderNilContext ensures the provider can handle a nil context gracefully.
func TestSignerProviderNilContext(t *testing.T) {
	provider := SignerProvider{}
	config := map[string]string{}

	signerInstance, err := provider.New(context.TODO(), config)

	if err != nil {
		t.Fatalf("Nil context should not cause an error, but got: %v", err)
	}
	if signerInstance == nil {
		t.Fatal("Signer instance should still be created")
	}
}

// TestSignerProviderEmptyConfig ensures that an empty config does not cause issues.
func TestSignerProviderEmptyConfig(t *testing.T) {
	provider := SignerProvider{}
	emptyConfig := map[string]string{}

	signerInstance, err := provider.New(context.Background(), emptyConfig)

	if err != nil {
		t.Fatalf("Empty config should not cause an error, but got: %v", err)
	}
	if signerInstance == nil {
		t.Fatal("Signer instance should still be created")
	}
}

// TestSignerProviderHandlesEdgeCases ensures no panic occurs even with unexpected input.
func TestSignerProviderHandlesEdgeCases(t *testing.T) {
	provider := SignerProvider{}

	edgeCases := []map[string]string{
		nil,
		{},
		{"ttl": ""},
		{"ttl": "-100"},
		{"ttl": "not_a_number"},
	}

	for _, config := range edgeCases {
		t.Run("Testing edge case", func(t *testing.T) {
			signerInstance, err := provider.New(context.Background(), config)
			if err != nil {
				t.Fatalf("Edge case should not cause an error, but got: %v", err)
			}
			if signerInstance == nil {
				t.Fatal("Signer instance should still be created")
			}
		})
	}
}
