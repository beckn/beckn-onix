package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// SignValidator defines the method for verifying signatures.
type SignValidator interface {
	// Validate verifies the 3-line signing string for inbound requests.
	// The request body is available as ctx.Body.
	Validate(ctx *model.StepContext, header string, publicKeyBase64 string) error

	// ValidateAck verifies a Beckn v2.0.0 AckSignature per NFH-004 §3.4.
	// The four-line signing string is:
	//   (created): <ts>
	//   (expires): <ts>
	//   digest: BLAKE-512=<base64(blake2b512(body))>
	//   request-signature: <outboundAuthSignature>
	// outboundAuthSignature is the raw Base64 signature value from the original
	// outbound Authorization header's signature="..." attribute. If empty the
	// fourth line is omitted (matches the ackSigner signing-string construction).
	// body is passed explicitly because different call sites hash different bodies:
	// solicited callback bodies differ from synchronous ACK response bodies.
	ValidateAck(ctx *model.StepContext, body []byte, signatureHeader, outboundAuthSignature, publicKeyBase64 string) error
}

// SignValidatorProvider initializes a new Verifier instance with the given config.
type SignValidatorProvider interface {
	// New creates a new Verifier instance based on the provided config.
	New(ctx context.Context, config map[string]string) (SignValidator, func() error, error)
}
