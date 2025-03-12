package definition

import "context"

// Verifier defines the method for verifying signatures.
type Verifier interface {
	// Verify checks the validity of the signature for the given body.
	Verify(ctx context.Context, body []byte, header []byte, publicKeyBase64 string) (bool, error)
	Close() error // Close for releasing resources
}

// VerifierProvider initializes a new Verifier instance with the given config.
type VerifierProvider interface {
	// New creates a new Verifier instance based on the provided config.
	New(ctx context.Context, config map[string]string) (Verifier, func() error, error)
}

// PublicKeyManager is the interface for key management plugin.
type PublicKeyManager interface {
	// PublicKey retrieves the public key for the given subscriberID and keyID.
	PublicKey(ctx context.Context, subscriberID string, keyID string) (string, error)
}
