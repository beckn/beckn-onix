package main

import (
	"context"
	"errors"
	"testing"
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

	if err != nil {
		t.Fatalf("Expected no error, but got: %v", err)
	}
	if verifierInstance == nil {
		t.Fatal("Expected verifier instance to be non-nil")
	}
}

// TestVerifierProviderWithNilContext tests verifier creation failure due to nil context.
func TestVerifierProviderWithNilContext(t *testing.T) {
	provider := VerifierProvider{}

	verifierInstance, err := provider.New(context.TODO(), map[string]string{})

	if err != nil {
		t.Fatalf("Expected no error, but got: %v", err)
	}
	if verifierInstance == nil {
		t.Fatal("Expected verifier instance to be non-nil")
	}
}

// TestVerifierProviderWithEmptyConfig tests verifier creation with an empty config.
func TestVerifierProviderWithEmptyConfig(t *testing.T) {
	provider := VerifierProvider{}

	verifierInstance, err := provider.New(context.Background(), map[string]string{})

	if err != nil {
		t.Fatalf("Expected no error, but got: %v", err)
	}
	if verifierInstance == nil {
		t.Fatal("Expected verifier instance to be non-nil")
	}
}

// TestMockVerifierVerifySuccess tests the Verify function of MockVerifier.
func TestMockVerifierVerifySuccess(t *testing.T) {
	mockVerifier := MockVerifier{}

	valid, err := mockVerifier.Verify(context.Background(), []byte("valid_signature"), []byte("payload"), "valid_public_key")

	if err != nil {
		t.Fatalf("Expected no error, but got: %v", err)
	}
	if !valid {
		t.Fatal("Expected verification to succeed")
	}
}

// TestMockVerifierVerifyFailure tests Verify with invalid inputs.
func TestMockVerifierVerifyFailure(t *testing.T) {
	mockVerifier := MockVerifier{}

	valid, err := mockVerifier.Verify(context.Background(), []byte{}, []byte("payload"), "valid_public_key")
	if err == nil {
		t.Fatal("Expected error due to invalid input, but got none")
	}
	if valid {
		t.Fatal("Expected verification to fail due to invalid input")
	}

	valid, err = mockVerifier.Verify(context.Background(), []byte("valid_signature"), []byte{}, "valid_public_key")
	if err == nil {
		t.Fatal("Expected error due to invalid input, but got none")
	}
	if valid {
		t.Fatal("Expected verification to fail due to invalid input")
	}

	valid, err = mockVerifier.Verify(context.Background(), []byte("valid_signature"), []byte("payload"), "")
	if err == nil {
		t.Fatal("Expected error due to invalid input, but got none")
	}
	if valid {
		t.Fatal("Expected verification to fail due to invalid input")
	}
}
