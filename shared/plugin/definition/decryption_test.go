package definition

import (
	"context"
	"errors"
	"testing"
)

// MockDecrypter implements Decrypter interface for testing
type MockDecrypter struct {
	decryptFunc func(ctx context.Context, encryptedData string, privateKeyBase64, publicKeyBase64 string) (string, error)
	closeFunc   func() error
}

func (m *MockDecrypter) Decrypt(ctx context.Context, encryptedData string, privateKeyBase64, publicKeyBase64 string) (string, error) {
	return m.decryptFunc(ctx, encryptedData, privateKeyBase64, publicKeyBase64)
}

func (m *MockDecrypter) Close() error {
	return m.closeFunc()
}

// MockDecrypterProvider implements DecrypterProvider interface for testing
type MockDecrypterProvider struct {
	newFunc func(ctx context.Context, config map[string]string) (Decrypter, func() error, error)
}

func (m *MockDecrypterProvider) New(ctx context.Context, config map[string]string) (Decrypter, func() error, error) {
	return m.newFunc(ctx, config)
}

func TestDecrypterProviderSuccess(t *testing.T) {
	tests := []struct {
		name          string
		ctx           context.Context
		config        map[string]string
		expectedData  string
		mockDecrypter *MockDecrypter
	}{
		{
			name:   "Valid decryption with empty config",
			ctx:    context.Background(),
			config: map[string]string{},
			mockDecrypter: &MockDecrypter{
				decryptFunc: func(ctx context.Context, encryptedData string, privateKeyBase64, publicKeyBase64 string) (string, error) {
					return "decrypted data", nil
				},
				closeFunc: func() error {
					return nil
				},
			},
			expectedData: "decrypted data",
		},
		{
			name: "Valid decryption with config",
			ctx:  context.Background(),
			config: map[string]string{
				"algorithm": "AES",
			},
			mockDecrypter: &MockDecrypter{
				decryptFunc: func(ctx context.Context, encryptedData string, privateKeyBase64, publicKeyBase64 string) (string, error) {
					return "decrypted data with config", nil
				},
				closeFunc: func() error {
					return nil
				},
			},
			expectedData: "decrypted data with config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &MockDecrypterProvider{
				newFunc: func(ctx context.Context, config map[string]string) (Decrypter, func() error, error) {
					return tt.mockDecrypter, tt.mockDecrypter.Close, nil
				},
			}

			decrypter, cleanup, err := provider.New(tt.ctx, tt.config)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer cleanup()

			result, err := decrypter.Decrypt(tt.ctx, "encrypted", "private-key", "public-key")
			if err != nil {
				t.Errorf("Decrypt() error = %v", err)
			}
			if result != tt.expectedData {
				t.Errorf("Decrypt() = %v, want %v", result, tt.expectedData)
			}
		})
	}
}

func TestDecrypterProviderFailure(t *testing.T) {
	tests := []struct {
		name          string
		ctx           context.Context
		config        map[string]string
		expectedError string
		mockDecrypter *MockDecrypter
	}{
		{
			name:          "Nil context",
			ctx:           nil,
			config:        map[string]string{},
			expectedError: "context cannot be nil",
		},
		{
			name: "Decryption error",
			ctx:  context.Background(),
			config: map[string]string{
				"algorithm": "invalid",
			},
			mockDecrypter: &MockDecrypter{
				decryptFunc: func(ctx context.Context, encryptedData string, privateKeyBase64, publicKeyBase64 string) (string, error) {
					return "", errors.New("decryption failed")
				},
				closeFunc: func() error {
					return nil
				},
			},
			expectedError: "decryption failed",
		},
		{
			name: "Invalid private key",
			ctx:  context.Background(),
			config: map[string]string{
				"algorithm": "AES",
			},
			mockDecrypter: &MockDecrypter{
				decryptFunc: func(ctx context.Context, encryptedData string, privateKeyBase64, publicKeyBase64 string) (string, error) {
					return "", errors.New("invalid private key")
				},
				closeFunc: func() error {
					return nil
				},
			},
			expectedError: "invalid private key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &MockDecrypterProvider{
				newFunc: func(ctx context.Context, config map[string]string) (Decrypter, func() error, error) {
					if tt.ctx == nil {
						return nil, nil, errors.New(tt.expectedError)
					}
					return tt.mockDecrypter, tt.mockDecrypter.Close, nil
				},
			}

			decrypter, cleanup, err := provider.New(tt.ctx, tt.config)
			if tt.ctx == nil {
				if err == nil || err.Error() != tt.expectedError {
					t.Errorf("New() error = %v, want %v", err, tt.expectedError)
				}
				return
			}
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer func() {
				if cleanup != nil {
					cleanup()
				}
			}()

			_, err = decrypter.Decrypt(tt.ctx, "encrypted", "private-key", "public-key")
			if err == nil || err.Error() != tt.expectedError {
				t.Errorf("Decrypt() error = %v, want %v", err, tt.expectedError)
			}
		})
	}
}

func TestDecrypterClose(t *testing.T) {
	tests := []struct {
		name        string
		closeError  error
		wantCleanup bool
	}{
		{
			name:        "Successful cleanup",
			closeError:  nil,
			wantCleanup: true,
		},
		{
			name:        "Cleanup error",
			closeError:  errors.New("cleanup failed"),
			wantCleanup: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanupCalled := false
			mockDecrypter := &MockDecrypter{
				closeFunc: func() error {
					cleanupCalled = true
					return tt.closeError
				},
			}

			provider := &MockDecrypterProvider{
				newFunc: func(ctx context.Context, config map[string]string) (Decrypter, func() error, error) {
					return mockDecrypter, mockDecrypter.Close, nil
				},
			}

			_, cleanup, err := provider.New(context.Background(), nil)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			err = cleanup()
			if (err != nil) != (tt.closeError != nil) {
				t.Errorf("cleanup() error = %v, want %v", err, tt.closeError)
			}
			if cleanupCalled != tt.wantCleanup {
				t.Errorf("cleanup called = %v, want %v", cleanupCalled, tt.wantCleanup)
			}
		})
	}
}
