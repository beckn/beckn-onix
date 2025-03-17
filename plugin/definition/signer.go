package definition

import "context"

// Signer defines the method for signing.
type Signer interface {
	// Sign generates a signature for the given body and privateKeyBase64.
	Sign(ctx context.Context, body []byte, privateKeyBase64 string) (string, error)
}

// SignerProvider initializes a new signer instance with the given config.
type SignerProvider interface {
	// New creates a new signer instance based on the provided config.
	New(ctx context.Context, config map[string]string) (Signer, func(), error)
}
