package definition

import "context"

// Signer defines the method for signing.
type Signer interface {
	// Sign generates a signature for the given body and privateKeyBase64.
	// The signature is created with the given timestamps: createdAt (signature creation time)
	// and expiresAt (signature expiration time).
	Sign(ctx context.Context, body []byte, privateKeyBase64 string, createdAt, expiresAt int64) (string, error)
	Close() error // Close for releasing resources
}

// SignerProvider initializes a new signer instance with the given config.
type SignerProvider interface {
	// New creates a new signer instance based on the provided config.
	New(ctx context.Context, config map[string]string) (Signer, error)
}

// PrivateKeyManager is the interface for key management plugin.
type PrivateKeyManager interface {
	// PrivateKey retrieves the private key for the given subscriberID and keyID.
	PrivateKey(ctx context.Context, subscriberID string, keyID string) (string, error)
}
