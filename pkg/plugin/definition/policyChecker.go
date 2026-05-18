package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// PolicyChecker interface for policy checking on incoming messages.
type PolicyChecker interface {
	CheckPolicy(ctx *model.StepContext) error
}

// PolicyCheckerProvider interface for creating policy checkers.
type PolicyCheckerProvider interface {
	New(ctx context.Context, manifestLoader ManifestLoader, config map[string]string) (PolicyChecker, func(), error)
}
