package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// SchemaVersionMediator mediates schema version differences in a Beckn payload.
// It walks the payload, checks compatibility against the node manifest, fetches
// translation artifacts, dispatches to the appropriate translator, and enforces
// the configured data-loss policy.
//
// Mediate is direction-agnostic: both the receiver handler (inbound) and the
// caller handler (outbound) invoke it via their respective StepContext.
type SchemaVersionMediator interface {
	Mediate(ctx *model.StepContext) error
}

// SchemaVersionMediatorProvider initializes a SchemaVersionMediator with its
// dependencies. loader is injected so the mediator can fetch the node manifest
// for the network at runtime without owning the fetch/cache lifecycle.
type SchemaVersionMediatorProvider interface {
	New(ctx context.Context, loader ManifestLoader, config map[string]string) (SchemaVersionMediator, func() error, error)
}
