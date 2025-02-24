package plugins

import "context"

// Signer inteface defines the method for signing
type Signer interface {
	Sign(ctx context.Context, body []byte, keyID string) (string, error)
}

type SignerProvider interface {
	// initialize a new signer instance with given config
	New(ctx context.Context, config map[string]string) (Signer, error)
}

// KeyManager is the interface for key management plugin.
type KeyManager interface {
	PrivateKey(ctx context.Context, keyID string) (string, error)
}
