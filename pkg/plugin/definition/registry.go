package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

type RegistryLookup interface {
	Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error)
}
