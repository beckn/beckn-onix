package definition

import (
	"context"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// Step processes the inbound request before routing.
type Step interface {
	Run(ctx *model.StepContext) error
}

// ResponseStep processes the upstream response before it is forwarded to the caller.
// A step may implement Step, ResponseStep, or both depending on which phase it needs.
// Steps implementing ResponseStep are automatically registered for response-phase
// invocation by the handler — no separate configuration is required.
type ResponseStep interface {
	RunOnResponse(ctx *model.StepContext, resp *http.Response) error
}

type StepProvider interface {
	New(context.Context, map[string]string) (Step, func(), error)
}
