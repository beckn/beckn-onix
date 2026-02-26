package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// PolicyEnforcer interface for policy enforcement on incoming messages.
type PolicyEnforcer interface {
	Run(ctx *model.StepContext) error
}

// PolicyEnforcerProvider interface for creating policy enforcers.
type PolicyEnforcerProvider interface {
	New(ctx context.Context, config map[string]string) (PolicyEnforcer, func(), error)
}
