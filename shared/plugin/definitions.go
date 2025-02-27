package plugins

import "context"

// Signer defines the method for signing.
type Signer interface {
	// Sign generates a signature for the given body and privateKeyBase64.
	Sign(ctx context.Context, body []byte, privateKeyBase64 string) (string, error)
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

// Validator defines the method for verifying signatures.
type Validator interface {
	// Verify checks the validity of the signature for the given body.
	Verify(ctx context.Context, body []byte, header []byte, publicKeyBase64 string) (bool, error)
}

// ValidatorProvider initializes a new validator instance with the given config.
type ValidatorProvider interface {
	// New creates a new validator instance based on the provided config.
	New(ctx context.Context, config map[string]string) (Validator, error)
}

// PublicKeyManager is the interface for key management plugin.
type PublicKeyManager interface {
	// PublicKey retrieves the public key for the given subscriberID and keyID.
	PublicKey(ctx context.Context, subscriberID string, keyID string) (string, error)
}
