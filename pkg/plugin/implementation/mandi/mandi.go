// Package mandi serves the network's market price capabilities.
//
// One package per schema pack family, so which plugin owns a capability is
// readable from its binding key: openagrinet:MandiPrice is mandi's,
// openagrinet:WeatherObservation is weather's.
//
// Almost nothing lives here, and that is the point. Recognising a capability,
// resolving the call plan, authenticating, calling with the registry's budget
// and translating in both directions are all internal/upstream's, because none
// of them differ by domain. What this package owns is its name, and
// prerequisites -- the work a mapping cannot express, which is domain knowledge
// by definition.
//
// The upstream this was written against is Agmarknet's Vistaar API, whose
// select takes governed codes for state, district, market and commodity plus a
// date range, all of which a MandiPrice payload carries. So the package is a
// name and nothing else: see prerequisites.go for why that is worth stating.
package mandi

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/upstream"
)

// Config is upstream's, unchanged. Aliased here so a domain plugin's cmd package
// need not know where the machinery lives.
type Config = upstream.Config

// New creates the mandi step.
//
// Which capabilities it answers to is configuration, with no default: a package
// serving a family cannot guess which of them a deployment has providers for.
func New(ctx context.Context, registry definition.ProviderRecordLookup, mapper definition.Mapper,
	cfg *Config) (definition.Step, func() error, error) {
	return upstream.New(ctx, registry, mapper, prerequisites, cfg)
}
