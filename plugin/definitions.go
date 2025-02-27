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

// Validator inteface defines the method for signing
type Validator interface {
	Verify(ctx context.Context, body []byte, sigtnature []byte) (bool, error)
}

type ValidatorProvider interface {
	// initialize a new validator instance with given config
	New(ctx context.Context, config map[string]string) (Validator, error)
}

// KeyManager is the interface for key management plugin.
type PublicKeyManager interface {
	PublicKey(ctx context.Context, subscriberID string, keyID string) (string, error)
}
