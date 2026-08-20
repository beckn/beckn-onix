// Package source implements crawlmanager.Source: a fixed config list, and a
// registry-backed lookup (registry.go/dediquery.go). Ported from the
// catalog-crawler prototype's own source package, adapted to crawlmanager's
// Discover/IndexRef naming.
package source

import (
	"context"

	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
)

// NewConfigSource resolves a fixed list of index URLs from config.
func NewConfigSource(indexURLs []string) crawlmanager.Source { return &configSource{urls: indexURLs} }

type configSource struct{ urls []string }

// Discover is deduped by URL (a repeated entry in config -- a typo, a
// copy-paste, a templated config concatenation -- must not reach the crawl
// loop twice; see registry.go's own per-network dedup for the same reasoning
// on that source).
func (c *configSource) Discover(context.Context) ([]crawlmanager.IndexRef, error) {
	seen := make(map[string]bool, len(c.urls))
	refs := make([]crawlmanager.IndexRef, 0, len(c.urls))
	for _, u := range c.urls {
		if seen[u] {
			continue
		}
		seen[u] = true
		refs = append(refs, crawlmanager.IndexRef{IndexURL: u})
	}
	return refs, nil
}
