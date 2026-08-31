package definition

import (
	"context"
	"errors"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// RegistryLookup resolves Beckn subscriber identities using generic Beckn protocol parameters.
// Inputs are subscriber-ID and key-ID as carried in the Beckn Authorization header.
type RegistryLookup interface {
	// Lookup finds a registry entry by subscriberID and keyID (from the Authorization header)
	// and returns the subscriber's public keys for incoming message signature validation.
	// Input: req.SubscriberID (Beckn subscriber_id), req.KeyID (Beckn key_id).
	Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error)
}

// RegistryMetadataLookup resolves DeDi registry and node records using DeDi-native path parameters.
// All inputs use the DeDi namespace/registry(/recordName) path convention — these are not generic
// Beckn params and are not interchangeable with the subscriberID/keyID used by RegistryLookup.
type RegistryMetadataLookup interface {
	// LookupRegistry fetches registry-level metadata for a DeDi network registry.
	// Input: namespaceIdentifier (DeDi namespace, e.g. "nfh.global"),
	//        registryName (DeDi registry name, e.g. "retail.network.production").
	// Returns registry metadata including manifest URLs used by ManifestLoader.
	LookupRegistry(ctx context.Context, namespaceIdentifier, registryName string) (*model.RegistryMetadata, error)

	// LookupNode fetches the full subscriber record for a DeDi node by its NodeID.
	// Input: nodeID must be a fully-qualified three-part DeDi path in
	//        namespace/registry/recordName format (e.g. "nfh.global/subscribers.beckn.one/bpp.energy.com").
	// Returns a SubscriberRecord with subscriber identity/endpoint data and any node manifest
	// metadata from the same DeDi response. Meta is empty (not an error) when the participant
	// has not yet published a node manifest.
	// The full SubscriberRecord is available for any plugin to consume — manifest discovery
	// (ManifestLoader) is the first use case but not the only one.
	LookupNode(ctx context.Context, nodeID string) (*model.SubscriberRecord, error)

	// QueryByNetwork fetches all subscriber records belonging to a DeDi network registry.
	// Input: networkID in namespace/registryName DeDi path form (e.g. "beckn.one/testnet").
	// Only records with state=="live" are returned. Meta/MetaArrays follow the same shape
	// as LookupNode (e.g. MetaArrays["catalog_index_urls"]). AllowedNetworkIDs is not
	// applied, same as LookupNode -- this is a discovery read, not a trust decision.
	QueryByNetwork(ctx context.Context, networkID string) ([]model.SubscriberRecord, error)
}

// RegistryLookupProvider initializes a new registry lookup instance.
type RegistryLookupProvider interface {
	New(context.Context, Cache, map[string]string) (RegistryLookup, func() error, error)
}

// ErrProviderRecordNotFound reports that no usable call plan exists for a
// binding key. It is returned for an absent binding, an absent participant, and
// for either of them being inactive -- all of which mean the same thing to a
// caller: this capability cannot be served right now. The distinction between
// them is observable in the plugin's own logs and metrics, and is not something
// a caller can act on differently.
var ErrProviderRecordNotFound = errors.New("provider record not found")

// ProviderRecordLookup resolves a provider capability into a call plan.
//
// It is separate from RegistryLookup because it answers a different question
// about a different party. RegistryLookup answers "what is the SENDER's public
// key", keyed by the identity in an inbound Authorization header.
// ProviderRecordLookup answers "how do I call the UPSTREAM provider", keyed by
// a capability binding taken from the request body. The two have different
// subjects, different cache lifetimes, and different failure meanings, so they
// are not folded together.
//
// A registry plugin may implement one, the other, or both. Callers obtain this
// by type-asserting a RegistryLookup, the same way RegistryMetadataLookup is
// obtained -- a plugin that does not implement it yields nil, and the consumer
// decides whether that is fatal.
type ProviderRecordLookup interface {
	// ProviderRecord resolves bindingKey ("<participantId>|<capabilityCode>")
	// into everything needed to call the provider.
	//
	// Returns ErrProviderRecordNotFound when no usable plan exists. Any other
	// error is a transport or decoding failure -- the registry could not be
	// consulted, which is not the same as it answering "no".
	ProviderRecord(ctx context.Context, bindingKey string) (*model.ProviderRecord, error)
}
