package definition

import (
	"context"
	"testing"
)

// MockEncrypter implements the Encrypter interface for testing
type MockEncrypter struct {
	encryptFunc func(ctx context.Context, data string, publicKeyBase64 string) (string, error)
	closeFunc   func() error
}

func (m *MockEncrypter) Encrypt(ctx context.Context, data string, publicKeyBase64 string) (string, error) {
	if m.encryptFunc != nil {
		return m.encryptFunc(ctx, data, publicKeyBase64)
	}
	return "", nil
}

func (m *MockEncrypter) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// TestEncrypterInterface tests the Encrypter interface implementation
func TestEncrypterInterface(t *testing.T) {
	tests := []struct {
		name      string
		setup     func() *MockEncrypter
		input     string
		pubKey    string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "Successful encryption",
			setup: func() *MockEncrypter {
				return &MockEncrypter{
					encryptFunc: func(ctx context.Context, data string, publicKeyBase64 string) (string, error) {
						return "encrypted:" + data, nil
					},
				}
			},
			input:   "test data",
			pubKey:  "test-key",
			wantErr: false,
		},
		{
			name: "Empty input",
			setup: func() *MockEncrypter {
				return &MockEncrypter{
					encryptFunc: func(ctx context.Context, data string, publicKeyBase64 string) (string, error) {
						return "encrypted:", nil
					},
				}
			},
			input:   "",
			pubKey:  "test-key",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter := tt.setup()
			ctx := context.Background()

			result, err := encrypter.Encrypt(ctx, tt.input, tt.pubKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("Encrypt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && result == "" {
				t.Error("Encrypt() returned empty result")
			}
		})
	}
}

// TestEncrypterClose tests the Close method
func TestEncrypterClose(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *MockEncrypter
		wantErr bool
	}{
		{
			name: "Successful close",
			setup: func() *MockEncrypter {
				return &MockEncrypter{
					closeFunc: func() error {
						return nil
					},
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypter := tt.setup()
			err := encrypter.Close()
			if (err != nil) != tt.wantErr {
				t.Errorf("Close() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestEncrypterProvider tests the EncrypterProvider interface
func TestEncrypterProvider(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "Valid config",
			config: map[string]string{
				"algorithm": "RSA",
				"keySize":   "2048",
			},
			wantErr: false,
		},
		{
			name:    "Empty config",
			config:  map[string]string{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockProvider := &MockEncrypterProvider{}
			ctx := context.Background()

			encrypter, err := mockProvider.New(ctx, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && encrypter == nil {
				t.Error("New() returned nil encrypter")
			}
		})
	}
}

// MockEncrypterProvider implements the EncrypterProvider interface for testing
type MockEncrypterProvider struct{}

func (p *MockEncrypterProvider) New(ctx context.Context, config map[string]string) (Encrypter, error) {
	return &MockEncrypter{}, nil
}