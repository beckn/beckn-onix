package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// ManifestLoader fetches, verifies, caches, and returns manifest content.
type ManifestLoader interface {
	GetByNetworkID(ctx context.Context, networkID string) (*model.ManifestDocument, error)
	GetByMetadata(ctx context.Context, metadata model.ManifestMetadata) (*model.ManifestDocument, error)
	// GetBySubscriberID fetches the node manifest for a specific subscriber.
	// The subscriberID is the fully-qualified three-part DeDi reference (namespace/registry/recordName).
	GetBySubscriberID(ctx context.Context, subscriberID string) (*model.ManifestDocument, error)
}

// ManifestLoaderProvider initializes a manifest loader instance with its dependencies.
// registry is used for subscriber-level node lookups (LookupNode).
// metaRegistry is used for network-level manifest lookups (LookupRegistry).
type ManifestLoaderProvider interface {
	New(context.Context, Cache, RegistryLookup, RegistryMetadataLookup, map[string]string) (ManifestLoader, func() error, error)
}
