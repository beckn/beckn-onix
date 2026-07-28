package source

import (
	"context"
	"fmt"
)

// Provider is a registry lookup result: a participant and its index URL.
type Provider struct {
	ParticipantID string
	IndexURL      string
}

// RegistryClient resolves a networkId to its member providers + index URLs.
type RegistryClient interface {
	Providers(ctx context.Context, networkID string) ([]Provider, error)
}

// NewRegistrySource resolves index URLs by asking the registry for the
// providers of each configured networkId (deduped by index URL).
func NewRegistrySource(client RegistryClient, networkIDs []string) Source {
	return &registrySource{client: client, networkIDs: networkIDs}
}

type registrySource struct {
	client     RegistryClient
	networkIDs []string
}

func (r *registrySource) IndexRefs(ctx context.Context) ([]IndexRef, error) {
	seen := make(map[string]bool)
	var refs []IndexRef
	for _, net := range r.networkIDs {
		provs, err := r.client.Providers(ctx, net)
		if err != nil {
			return nil, fmt.Errorf("catalogcrawler: registry lookup %q: %w", net, err)
		}
		for _, p := range provs {
			if p.IndexURL == "" || seen[p.IndexURL] {
				continue
			}
			seen[p.IndexURL] = true
			refs = append(refs, IndexRef{IndexURL: p.IndexURL, ParticipantID: p.ParticipantID, Source: KindRegistry})
		}
	}
	return refs, nil
}
