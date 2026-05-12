package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// Step is executed on the inbound request as part of the processing pipeline.
type Step interface {
	Run(ctx *model.StepContext) error
}

// ResponseStep is executed after all inbound Steps succeed, before the
// synchronous ACK is written back to the caller. Implementations set response
// headers on ctx.RespHeader (e.g. the Signature header for NFH-004 AckSigner).
type ResponseStep interface {
	RunOnResponse(ctx *model.StepContext) error
}

type StepProvider interface {
	New(context.Context, map[string]string) (Step, func(), error)
}
