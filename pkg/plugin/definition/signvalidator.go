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

	// ValidateAck verifies the 4-line signing string per NFH-004 §3.4:
	//   (created): <ts>
	//   (expires): <ts>
	//   digest: BLAKE-512=<base64(blake2b512(body))>
	//   request-signature: <outboundAuthSignature>  (omitted when empty)
	// body is passed explicitly because the two call sites hash different bodies:
	// the on_search request body (step.go) vs the ACK response body (responsestep.go).
	ValidateAck(ctx *model.StepContext, body []byte, signatureHeader, outboundAuthSignature, publicKeyBase64 string) error
}

// SignValidatorProvider initializes a new Verifier instance with the given config.
type SignValidatorProvider interface {
	// New creates a new Verifier instance based on the provided config.
	New(ctx context.Context, config map[string]string) (SignValidator, func() error, error)
}
