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

// IndexRefs is deduped by URL (a repeated entry in CRAWLER_INDEX_URLS -- a
// typo, a copy-paste, a templated config concatenation -- must not reach the
// crawl loop twice; see registry.go's own per-network dedup for the same
// reasoning on that source).
func (c *configSource) IndexRefs(ctx context.Context) ([]IndexRef, error) {
	seen := make(map[string]bool, len(c.urls))
	refs := make([]IndexRef, 0, len(c.urls))
	for _, u := range c.urls {
		if seen[u] {
			continue
		}
		seen[u] = true
		refs = append(refs, IndexRef{IndexURL: u, Source: KindConfig})
	}
	return refs, nil
}
