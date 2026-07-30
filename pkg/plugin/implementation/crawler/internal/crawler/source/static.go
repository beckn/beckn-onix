// Package source resolves the set of index refs the crawler should poll —
// from a static config list or (later) a registry lookup. Framework-agnostic.
package source

import "context"

// Source kinds recorded on crawler_index.source.
const (
	KindRegistry = "registry"
	KindConfig   = "config"
	KindOnDemand = "on_demand"
)

// IndexRef is one index to crawl and where it came from.
type IndexRef struct {
	IndexURL      string
	ParticipantID string
	Source        string
}

// Source resolves the set of index refs the crawler should poll.
type Source interface {
	IndexRefs(ctx context.Context) ([]IndexRef, error)
}

// NewConfigSource resolves a fixed list of index URLs from config.
func NewConfigSource(indexURLs []string) Source { return &configSource{urls: indexURLs} }

type configSource struct{ urls []string }

func (c *configSource) IndexRefs(ctx context.Context) ([]IndexRef, error) {
	refs := make([]IndexRef, 0, len(c.urls))
	for _, u := range c.urls {
		refs = append(refs, IndexRef{IndexURL: u, Source: KindConfig})
	}
	return refs, nil
}
