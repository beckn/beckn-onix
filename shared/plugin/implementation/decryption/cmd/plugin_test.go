package main

import (
	"context"
	"strings"
	"testing"

	"github.com/beckn/beckn-onix/shared/plugin/definition"
)

// MockDecrypter implements the definition.Decrypter interface for testing
type MockDecrypter struct {
	decryptFunc func(ctx context.Context, encryptedData, privateKey, publicKey string) (string, error)
	closeFunc   func() error
}

func (m *MockDecrypter) Decrypt(ctx context.Context, encryptedData, privateKey, publicKey string) (string, error) {
	if m.decryptFunc != nil {
		return m.decryptFunc(ctx, encryptedData, privateKey, publicKey)
	}
	return "", nil
}

func (m *MockDecrypter) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestDecrypterProviderSuccess(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		config      map[string]string
		wantDecrypt bool
		wantCleanup bool
	}{
		{
			name:        "Valid context with empty config",
			ctx:         context.Background(),
			config:      map[string]string{},
			wantDecrypt: true,
			wantCleanup: true,
		},
		{
			name:        "Valid context with non-empty config",
			ctx:         context.Background(),
			config:      map[string]string{"key": "value"},
			wantDecrypt: true,
			wantCleanup: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := DecrypterProvider{}
			decrypter, cleanup, err := provider.New(tt.ctx, tt.config)

			// Check error
			if err != nil {
				t.Errorf("New() error = %v, want no error", err)
				return
			}

			// Check decrypter
			if tt.wantDecrypt && decrypter == nil {
				t.Error("New() decrypter is nil, want non-nil")
			}

			// Check cleanup function
			if tt.wantCleanup && cleanup == nil {
				t.Error("New() cleanup is nil, want non-nil")
			}

			// Test cleanup function if it exists
			if cleanup != nil {
				if err := cleanup(); err != nil {
					t.Errorf("cleanup() error = %v", err)
				}
			}

			// Test decrypter functionality if it exists
			if decrypter != nil {
				// Test Decrypt method
				_, err := decrypter.Decrypt(context.Background(), "test", "test", "test")
				if err != nil {
					// We expect an error here due to invalid input, but we just want to ensure the method exists
					if !strings.Contains(err.Error(), "invalid private key") && !strings.Contains(err.Error(), "invalid public key") {
						t.Errorf("Unexpected error type: %v", err)
					}
				}

				// Test Close method
				if err := decrypter.Close(); err != nil {
					t.Errorf("Close() error = %v", err)
				}
			}
		})
	}
}

func TestDecrypterProviderFailure(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		config    map[string]string
		wantError string
	}{
		{
			name:      "Nil context",
			ctx:       nil,
			config:    map[string]string{},
			wantError: "context cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := DecrypterProvider{}
			decrypter, cleanup, err := provider.New(tt.ctx, tt.config)

			// Check error
			if err == nil {
				t.Error("New() error = nil, want error")
				return
			}

			if err.Error() != tt.wantError {
				t.Errorf("New() error = %v, want %v", err, tt.wantError)
			}

			// Check that decrypter and cleanup are nil on error
			if decrypter != nil {
				t.Error("New() decrypter is not nil, want nil")
			}

			if cleanup != nil {
				t.Error("New() cleanup is not nil, want nil")
			}
		})
	}
}

func TestProviderImplementation(t *testing.T) {
	// Test that Provider is properly initialized
	if Provider == nil {
		t.Error("Provider is nil, want non-nil")
	}

	// Test that Provider implements definition.DecrypterProvider
	if _, ok := Provider.(definition.DecrypterProvider); !ok {
		t.Error("Provider does not implement definition.DecrypterProvider")
	}

	// Test Provider type
	if _, ok := Provider.(DecrypterProvider); !ok {
		t.Error("Provider is not of type DecrypterProvider")
	}
}
