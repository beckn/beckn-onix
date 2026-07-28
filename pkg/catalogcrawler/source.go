package catalogcrawler

import (
	"context"
	"fmt"
)

// Source kinds recorded on crawler_index.source.
const (
	SourceRegistry = "registry"
	SourceConfig   = "config"
	SourceOnDemand = "on_demand"
)

// IndexRef is one index to crawl and where it came from.
type IndexRef struct {
	IndexURL      string
	ParticipantID string
	Source        string
}

// Source resolves the set of index URLs the crawler should poll.
type Source interface {
	IndexURLs(ctx context.Context) ([]IndexRef, error)
}

// Provider is a registry lookup result: a participant and its index URL.
type Provider struct {
	ParticipantID string
	IndexURL      string
}

// RegistryClient resolves a networkId to its member providers + index URLs.
type RegistryClient interface {
	Providers(ctx context.Context, networkID string) ([]Provider, error)
}

// NewConfigSource resolves a fixed list of index URLs from config.
func NewConfigSource(indexURLs []string) Source { return &configSource{urls: indexURLs} }

type configSource struct{ urls []string }

func (c *configSource) IndexURLs(ctx context.Context) ([]IndexRef, error) {
	refs := make([]IndexRef, 0, len(c.urls))
	for _, u := range c.urls {
		refs = append(refs, IndexRef{IndexURL: u, Source: SourceConfig})
	}
	return refs, nil
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

func (r *registrySource) IndexURLs(ctx context.Context) ([]IndexRef, error) {
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
			refs = append(refs, IndexRef{IndexURL: p.IndexURL, ParticipantID: p.ParticipantID, Source: SourceRegistry})
		}
	}
	return refs, nil
}
