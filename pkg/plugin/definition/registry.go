package definition

import (
	"context"

	"github.com/beckn/beckn-onix/pkg/model"
)

type RegistryLookup interface {
	Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error)
}
