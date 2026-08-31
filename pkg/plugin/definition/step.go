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

// ProviderStep is a Step that serves one provider capability end to end: it
// resolves whatever the provider needs beyond the Beckn payload, calls it, and
// turns the answer back into Beckn.
//
// It is a plain Step at the pipeline's edge -- ProviderStepProvider exists only
// because it needs a registry and a mapper handed to it, which StepProvider
// cannot do. Everything provider-specific lives inside: the prerequisites the
// old per-provider services performed before a call (a station id resolved from
// coordinates, a token minted from credentials), and the call itself.
//
// A step that is handed a request for a capability it does not serve must do
// nothing and return nil. That is the whole dispatch mechanism: several provider
// steps sit in one pipeline, each recognises its own work, and adding a provider
// is one more entry rather than a change to a routing table.
type ProviderStepProvider interface {
	New(ctx context.Context, registry ProviderRecordLookup, mapper Mapper, config map[string]string) (Step, func() error, error)
}
