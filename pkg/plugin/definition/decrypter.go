package definition

import "context"

// Decrypter defines the methods for decryption.
type Decrypter interface {
	// Decrypt decrypts the given body using the provided privateKeyBase64 and publicKeyBase64.
	Decrypt(ctx context.Context, encryptedData string, privateKeyBase64, publicKeyBase64 string) (string, error)
}

// DecrypterProvider initializes a new decrypter instance with the given config.
type DecrypterProvider interface {
	// New creates a new decrypter instance based on the provided config.
	New(ctx context.Context, config map[string]string) (Decrypter, func() error, error)
}
