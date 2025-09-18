package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// DeDiRegistry interface defines methods for DeDi registry operations.
type DeDiRegistry interface {
	Lookup(ctx context.Context) (*model.DeDiRecord, error)
}

// DeDiRegistryProvider initializes a new DeDi registry instance.
type DeDiRegistryProvider interface {
	New(context.Context, map[string]string) (DeDiRegistry, func() error, error)
}