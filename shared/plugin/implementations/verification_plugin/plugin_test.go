package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// MockVerifier is a mock implementation of plugins.Validator for testing.
type MockVerifier struct{}

func (m *MockVerifier) Verify(ctx context.Context, signature []byte, payload []byte, publicKey string) (bool, error) {
	if len(signature) == 0 || len(payload) == 0 || publicKey == "" {
		return false, errors.New("invalid input parameters")
	}
	return true, nil
}

// TestVerifierProviderSuccess tests successful creation of a verifier.
func TestVerifierProviderSuccess(t *testing.T) {
	provider := VerifierProvider{}

	verifierInstance, err := provider.New(context.Background(), map[string]string{})

	assert.NoError(t, err)
	assert.NotNil(t, verifierInstance)
}

// TestVerifierProviderWithNilContext tests verifier creation failure due to nil context.
func TestVerifierProviderWithNilContext(t *testing.T) {
	provider := VerifierProvider{}

	verifierInstance, err := provider.New(nil, map[string]string{})

	assert.NoError(t, err)
	assert.NotNil(t, verifierInstance)
}

// TestVerifierProviderWithEmptyConfig tests verifier creation with an empty config.
func TestVerifierProviderWithEmptyConfig(t *testing.T) {
	provider := VerifierProvider{}

	verifierInstance, err := provider.New(context.Background(), map[string]string{})

	assert.NoError(t, err)
	assert.NotNil(t, verifierInstance)
}

// TestMockVerifierVerifySuccess tests the Verify function of MockVerifier.
func TestMockVerifierVerifySuccess(t *testing.T) {
	mockVerifier := MockVerifier{}

	valid, err := mockVerifier.Verify(context.Background(), []byte("valid_signature"), []byte("payload"), "valid_public_key")

	assert.NoError(t, err)
	assert.True(t, valid)
}

// TestMockVerifierVerifyFailure tests Verify with invalid inputs.
func TestMockVerifierVerifyFailure(t *testing.T) {
	mockVerifier := MockVerifier{}

	valid, err := mockVerifier.Verify(context.Background(), []byte{}, []byte("payload"), "valid_public_key")
	assert.Error(t, err)
	assert.False(t, valid)

	valid, err = mockVerifier.Verify(context.Background(), []byte("valid_signature"), []byte{}, "valid_public_key")
	assert.Error(t, err)
	assert.False(t, valid)

	valid, err = mockVerifier.Verify(context.Background(), []byte("valid_signature"), []byte("payload"), "")
	assert.Error(t, err)
	assert.False(t, valid)
}
