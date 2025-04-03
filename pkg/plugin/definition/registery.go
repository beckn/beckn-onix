package definition

import (
	"context"

	"github.com/beckn/beckn-onix/pkg/model"
)

type RegistryLookupProvider interface {
	New(context.Context, map[string]string) (RegistryLookup, func() error, error)
}

type RegistryLookup interface {
	Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error)
}
