package definition

import "context"

// Encrypter defines the methods for encryption.
type Encrypter interface {
	// Encrypt encrypts the given body using the provided privateKeyBase64 and publicKeyBase64.
	Encrypt(ctx context.Context, data string, privateKeyBase64, publicKeyBase64 string) (string, error)
}

// EncrypterProvider initializes a new encrypter instance with the given config.
type EncrypterProvider interface {
	// New creates a new encrypter instance based on the provided config.
	New(ctx context.Context, config map[string]string) (Encrypter, func() error, error)
}
