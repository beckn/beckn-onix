package definition

import "context"

// Signer defines the method for signing.
type Signer interface {
	// Sign generates a signature for the given body and privateKeyBase64.
	// The signature is created with the given timestamps: createdAt (signature creation time)
	// and expiresAt (signature expiration time).
	Sign(ctx context.Context, body []byte, privateKeyBase64 string, createdAt, expiresAt int64) (string, error)

	// SignAck generates a signature for a synchronous Ack response using the
	// NFH-004 §3.4 four-line signing string:
	//   (created): <ts>
	//   (expires): <ts>
	//   digest: BLAKE-512=<base64(blake2b512(ackBody))>
	//   request-signature: <requestSignature>
	// requestSignature is the raw Base64 value from the inbound Authorization
	// header's signature="..." attribute. If empty the fourth line is omitted.
	SignAck(ctx context.Context, ackBody []byte, requestSignature, privateKeyBase64 string, createdAt, expiresAt int64) (string, error)
}

// SignerProvider initializes a new signer instance with the given config.
type SignerProvider interface {
	// New creates a new signer instance based on the provided config.
	New(ctx context.Context, config map[string]string) (Signer, func() error, error)
}
