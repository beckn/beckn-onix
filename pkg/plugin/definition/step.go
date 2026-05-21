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
// synchronous ACK is written back to the caller.
//
// rctx is nil on the publisher path (ONIX writes the ACK itself); on the
// URL-routing path rctx carries the pre-read upstream response body, headers,
// and status code. Header is a shared reference — mutations (e.g. writing a
// Signature header) are forwarded by ReverseProxy without explicit write-back.
type ResponseStep interface {
	RunOnResponse(ctx *model.StepContext, rctx *model.ResponseStepContext) error
}

type StepProvider interface {
	New(context.Context, map[string]string) (Step, func(), error)
}
