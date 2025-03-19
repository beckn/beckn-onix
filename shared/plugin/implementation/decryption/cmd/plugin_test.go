package main

import (
	"context"
	"errors"
	"testing"

	"github.com/beckn/beckn-onix/shared/plugin/definition"
	"github.com/beckn/beckn-onix/shared/plugin/implementation/decryption"
)

// MockDecrypter is a mock implementation of the definition.Decrypter interface.
type MockDecrypter struct{}

func (m MockDecrypter) Close() error {
	return nil
}

func (m MockDecrypter) Decrypt(ctx context.Context, encryptedData, privateKeyBase64, publicKeyBase64 string) (string, error) {
	return encryptedData, nil
}

// mockDecrypterProvider is a test implementation of DecrypterProvider
type mockDecrypterProvider struct {
	mockNewFunc func(context.Context, *decryption.Config) (definition.Decrypter, func() error, error)
}

func (p mockDecrypterProvider) New(ctx context.Context, config map[string]string) (definition.Decrypter, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}
	return p.mockNewFunc(ctx, &decryption.Config{})
}

// mockNewSuccess mocks successful decryption calls
func mockNewSuccess(ctx context.Context, config *decryption.Config) (definition.Decrypter, func() error, error) {
	return &MockDecrypter{}, func() error { return nil }, nil
}

// mockNewError mocks failed decryption calls
func mockNewError(ctx context.Context, config *decryption.Config) (definition.Decrypter, func() error, error) {
	return nil, nil, errors.New("mock decryption error")
}

func TestDecrypterProviderSuccess(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		config map[string]string
	}{
		{
			name:   "success with empty config",
			ctx:    context.Background(),
			config: map[string]string{},
		},
		{
			name:   "success with non-empty config",
			ctx:    context.Background(),
			config: map[string]string{"key": "value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mockDecrypterProvider{mockNewFunc: mockNewSuccess}
			decrypter, cleanup, err := p.New(tt.ctx, tt.config)

			// Check success cases
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if decrypter == nil {
				t.Error("expected non-nil decrypter but got nil")
			}
			if cleanup == nil {
				t.Error("expected non-nil cleanup function but got nil")
			}

			// Test cleanup function
			if cleanup != nil {
				if err := cleanup(); err != nil {
					t.Errorf("cleanup function returned unexpected error: %v", err)
				}
			}
		})
	}
}

func TestDecrypterProviderFailure(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		config      map[string]string
		mockNewFunc func(context.Context, *decryption.Config) (definition.Decrypter, func() error, error)
		errMsg      string
	}{
		{
			name:        "nil context",
			ctx:         nil,
			config:      map[string]string{},
			mockNewFunc: mockNewSuccess,
			errMsg:      "context cannot be nil",
		},
		{
			name:        "decryption initialization error",
			ctx:         context.Background(),
			config:      map[string]string{},
			mockNewFunc: mockNewError,
			errMsg:      "mock decryption error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mockDecrypterProvider{mockNewFunc: tt.mockNewFunc}
			decrypter, cleanup, err := p.New(tt.ctx, tt.config)

			// Error case validations
			if err == nil {
				t.Error("expected error but got none")
			}
			if err != nil && err.Error() != tt.errMsg {
				t.Errorf("expected error message %q, got %q", tt.errMsg, err.Error())
			}
			if decrypter != nil {
				t.Errorf("expected nil decrypter but got %v", decrypter)
			}
			if cleanup != nil {
				t.Error("expected nil cleanup function but got non-nil")
			}
		})
	}
}

// TestProvider ensures the Provider variable is properly initialized
func TestProvider(t *testing.T) {
	if Provider == nil {
		t.Error("Provider should not be nil")
	}

	if _, ok := Provider.(DecrypterProvider); !ok {
		t.Error("Provider should be of type DecrypterProvider")
	}
}
