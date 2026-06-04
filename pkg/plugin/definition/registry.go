package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

type RegistryLookup interface {
	// Lookup looks up a registry entry to obtain public keys to validate the signature of the incoming message.
	Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error)

	// LookupNode looks up a subscriber record by its fully-qualified NodeID.
	// nodeID must be in namespace/registry/recordName format (exactly 3 non-empty parts separated by "/").
	// Returns the subscriber's Subscription including URL, type, and domain.
	// Implementations backed by the standard Beckn registry should return an appropriate error.
	LookupNode(ctx context.Context, nodeID string) (*model.Subscription, error)
}

// RegistryMetadataLookup fetches registry-level metadata for a DeDi registry path.
type RegistryMetadataLookup interface {
	// LookupRegistry fetches registry-level metadata for the given namespace/registry path.
	LookupRegistry(ctx context.Context, namespaceIdentifier, registryName string) (*model.RegistryMetadata, error)
}

// RegistryLookupProvider initializes a new registry lookup instance.
type RegistryLookupProvider interface {
	New(context.Context, map[string]string) (RegistryLookup, func() error, error)
}
