package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

type RegistryLookup interface {
	Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error)
	Subscribe(ctx context.Context, subscription *model.Subscription) error
}

// RegistryLookupProvider initializes a new registry lookup instance.
type RegistryLookupProvider interface {
	New(context.Context, map[string]string) (RegistryLookup, func() error, error)
}
