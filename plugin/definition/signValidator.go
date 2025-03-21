package definition

import "context"

// Validator defines the method for verifying signatures.
type SignValidator interface {
	// Verify checks the validity of the signature for the given body.
	Verify(ctx context.Context, body []byte, header string, publicKeyBase64 string) (bool, error)
}

// ValidatorProvider initializes a new validator instance with the given config.
type SignValidatorProvider interface {
	// New creates a new validator instance based on the provided config.
	New(ctx context.Context, config map[string]string) (SignValidator, error)
}
