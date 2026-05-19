package definition

import "context"

// SignValidator defines the method for verifying signatures.
type SignValidator interface {
	// Validate checks the validity of the signature for the given body.
	// Used for inbound request Authorization header verification.
	Validate(ctx context.Context, body []byte, header string, publicKeyBase64 string) error

	// ValidateAck verifies a Beckn v2.0.0 AckSignature per NFH-004 §3.4.
	// The four-line signing string is:
	//   (created): <ts>
	//   (expires): <ts>
	//   digest: BLAKE-512=<base64(blake2b512(ackBody))>
	//   request-signature: <outboundAuthSignature>
	// outboundAuthSignature is the raw Base64 signature value from the original
	// outbound Authorization header's signature="..." attribute. If empty the
	// fourth line is omitted (matches the ackSigner signing-string construction).
	ValidateAck(ctx context.Context, ackBody []byte, signatureHeader, outboundAuthSignature, publicKeyBase64 string) error
}

// SignValidatorProvider initializes a new Verifier instance with the given config.
type SignValidatorProvider interface {
	// New creates a new Verifier instance based on the provided config.
	New(ctx context.Context, config map[string]string) (SignValidator, func() error, error)
}
