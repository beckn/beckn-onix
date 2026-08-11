package source

// registry.go — the registry-backed Source: resolves index refs by asking a
// RegistryClient for the providers of each configured networkId, deduped by
// index URL so a provider in multiple networks is crawled once.

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

// Logger is the minimal logging port this package needs. It is declared
// locally (rather than importing runner.Logger) because runner already
// imports source — importing back would cycle. runner.Logger satisfies this
// interface structurally, so callers just pass it through. nil is valid and
// treated as a no-op, so existing callers that don't need registry-lookup
// visibility aren't forced to supply one.
type Logger interface {
	Debug(event string, kv ...any)
	Info(event string, kv ...any)
}

type noopLogger struct{}

func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Info(string, ...any)  {}

// NewRegistrySource resolves index URLs by asking the registry for the
// providers of each configured networkId (deduped by index URL).
//
// Logs an Info line per network on both outcomes:
//   - success: providers_found count — 0 with no error is the common failure
//     mode in practice (e.g. a provider's registered meta doesn't match the
//     shape this crawler expects) and is otherwise silent, so it gets an Info
//     line rather than only Debug.
//   - failure: the registry lookup error.
//
// Debug adds per-provider detail (participant id + index URL) on success.
func NewRegistrySource(client RegistryClient, networkIDs []string, logger Logger) Source {
	if logger == nil {
		logger = noopLogger{}
	}
	return &registrySource{client: client, networkIDs: networkIDs, logger: logger}
}

type registrySource struct {
	client     RegistryClient
	networkIDs []string
	logger     Logger
}

func (r *registrySource) IndexRefs(ctx context.Context) ([]IndexRef, error) {
	seen := make(map[string]bool)
	var refs []IndexRef
	for _, net := range r.networkIDs {
		provs, err := r.client.Providers(ctx, net)
		if err != nil {
			r.logger.Info("registry lookup failed",
				"component", "crawl", "stage", "registry_lookup",
				"network_id", net, "error", err.Error())
			return nil, fmt.Errorf("crawler: registry lookup %q: %w", net, err)
		}
		r.logger.Info("registry lookup succeeded",
			"component", "crawl", "stage", "registry_lookup",
			"network_id", net, "providers_found", len(provs))
		for _, p := range provs {
			r.logger.Debug("registry lookup provider",
				"component", "crawl", "stage", "registry_lookup",
				"network_id", net, "participant_id", p.ParticipantID, "index_url", p.IndexURL)
			if p.IndexURL == "" || seen[p.IndexURL] {
				continue
			}
			seen[p.IndexURL] = true
			refs = append(refs, IndexRef{IndexURL: p.IndexURL, ParticipantID: p.ParticipantID, Source: KindRegistry})
		}
	}
	return refs, nil
}
