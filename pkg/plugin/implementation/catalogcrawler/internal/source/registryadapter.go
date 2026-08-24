package source

// registryadapter.go — a RegistryClient backed by definition.RegistryMetadataLookup,
// the dediregistry plugin interface. Discovery goes through the plugin's own
// QueryByNetwork (DeDi /query/{networkId}, "live" filtering, and meta parsing
// already applied there) rather than a standalone HTTP client -- see #908.

import (
	"context"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// metadataLookupClient adapts a definition.RegistryMetadataLookup into a
// RegistryClient: one Provider per catalog index URL a live record declares
// in meta.catalog_index_urls (a record with more than one URL yields more
// than one Provider, one per catalog per node) -- the same mapping the
// removed direct-HTTP DediQueryClient applied.
type metadataLookupClient struct {
	lookup definition.RegistryMetadataLookup
}

// NewMetadataLookupClient builds a RegistryClient that resolves networkId to
// provider index URLs via lookup.QueryByNetwork.
func NewMetadataLookupClient(lookup definition.RegistryMetadataLookup) RegistryClient {
	return &metadataLookupClient{lookup: lookup}
}

func (c *metadataLookupClient) Providers(ctx context.Context, networkID string) ([]Provider, error) {
	records, err := c.lookup.QueryByNetwork(ctx, networkID)
	if err != nil {
		return nil, err
	}

	var provs []Provider
	for _, rec := range records {
		for _, idxURL := range rec.MetaArrays["catalog_index_urls"] {
			idx := strings.TrimSpace(idxURL)
			if idx == "" {
				continue
			}
			provs = append(provs, Provider{ParticipantID: rec.SubscriberID, IndexURL: idx})
		}
	}
	return provs, nil
}

var _ RegistryClient = (*metadataLookupClient)(nil)
