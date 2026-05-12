package definition

import (
	"context"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// Step is executed on the inbound request as part of the processing pipeline.
type Step interface {
	Run(ctx *model.StepContext) error
}

// ResponseStep is executed after all inbound Steps succeed, before the
// synchronous ACK is written back to the caller.
//
// resp is nil on the publisher path (ONIX writes the ACK itself); on the
// URL-routing path resp is the upstream HTTP response so implementations can
// read and restore the actual response body (e.g. for digest computation).
// Implementations set response headers either on ctx.RespHeader (publisher
// path) or on resp.Header (URL-routing path).
type ResponseStep interface {
	RunOnResponse(ctx *model.StepContext, resp *http.Response) error
}

type StepProvider interface {
	New(context.Context, map[string]string) (Step, func(), error)
}
