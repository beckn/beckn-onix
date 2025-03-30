package definition

import "context"

// SignValidator defines the method for verifying signatures.
type SignValidator interface {
	// Validate checks the validity of the signature for the given body.
	Validate(ctx context.Context, body []byte, header string, publicKeyBase64 string) error
}

// SignValidatorProvider initializes a new Verifier instance with the given config.
type SignValidatorProvider interface {
	// New creates a new Verifier instance based on the provided config.
	New(ctx context.Context, config map[string]string) (SignValidator, func() error, error)
}
