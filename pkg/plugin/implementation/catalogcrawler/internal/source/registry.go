package source

// registry.go — the registry-backed Source: resolves index refs by asking a
// RegistryClient for the providers of each configured networkId, deduped by
// index URL so a provider in multiple networks is crawled once.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
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
// providers of each configured networkId (deduped by index URL). log may be
// nil (slog.Default() is used then).
//
// Logs a line per network on both outcomes:
//   - success: providers_found count -- 0 with no error is the common
//     failure mode in practice (e.g. a provider's registered meta doesn't
//     match the shape this crawler expects) and is otherwise silent, so it
//     is logged rather than only visible in a debugger.
//   - failure: the registry lookup error.
func NewRegistrySource(client RegistryClient, networkIDs []string, log *slog.Logger) crawlmanager.Source {
	if log == nil {
		log = slog.Default()
	}
	return &registrySource{client: client, networkIDs: networkIDs, log: log}
}

type registrySource struct {
	client     RegistryClient
	networkIDs []string
	log        *slog.Logger
}

func (r *registrySource) Discover(ctx context.Context) ([]crawlmanager.IndexRef, error) {
	seen := make(map[string]bool)
	var refs []crawlmanager.IndexRef
	for _, net := range r.networkIDs {
		provs, err := r.client.Providers(ctx, net)
		if err != nil {
			r.log.ErrorContext(ctx, "catalogcrawler: registry lookup failed", "networkId", net, "error", err)
			return nil, fmt.Errorf("catalogcrawler: registry lookup %q: %w", net, err)
		}
		r.log.InfoContext(ctx, "catalogcrawler: registry lookup succeeded", "networkId", net, "providersFound", len(provs))
		for _, p := range provs {
			r.log.DebugContext(ctx, "catalogcrawler: registry lookup provider", "networkId", net, "participantId", p.ParticipantID, "indexUrl", p.IndexURL)
			if p.IndexURL == "" || seen[p.IndexURL] {
				continue
			}
			seen[p.IndexURL] = true
			refs = append(refs, crawlmanager.IndexRef{IndexURL: p.IndexURL, ParticipantID: p.ParticipantID})
		}
	}
	return refs, nil
}
