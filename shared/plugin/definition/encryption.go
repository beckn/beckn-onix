package definition

import "context"
// Encrypter defines the methods for encryption.
type Encrypter interface {
	// Encrypt encrypts the given body using the provided publicKeyBase64.
	Encrypt(ctx context.Context, data string, publicKeyBase64 string) (string, error)

	// Close for releasing resources
	Close() error
}

// EncrypterProvider initializes a new encrypter instance with the given config.
type EncrypterProvider interface {
	// New creates a new encrypter instance based on the provided config.
	New(ctx context.Context, config map[string]string) (Encrypter, error)
}

// PublicKeyManager is the interface for key management plugin to fetch public keys.
type PublicKeyManager interface {
	// PublicKey retrieves the public key for encryption for the given subscriberID and keyID.
	PublicKey(ctx context.Context, subscriberID string, keyID string) (string, error)
}
