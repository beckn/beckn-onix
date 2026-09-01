// Package weather serves the network's weather capabilities.
//
// One package per schema pack family, so which plugin owns a capability is
// readable from its binding key: openagrinet:WeatherObservation and
// openagrinet:WeatherAdvisory are weather's, openagrinet:MandiPrice is not.
//
// Almost nothing lives here. Recognising a capability, resolving the call plan,
// authenticating, calling with the registry's budget and translating in both
// directions are all internal/upstream's, because none of them differ by domain.
// What this package owns is its name, and prerequisites -- the work a mapping
// cannot express, which is domain knowledge by definition.
package weather

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/upstream"
)

// Config is upstream's, unchanged. Aliased here so a domain plugin's cmd package
// need not know where the machinery lives.
type Config = upstream.Config

// New creates the weather step.
//
// Which capabilities it answers to is configuration, with no default: a package
// serving a family cannot guess which of them a deployment has providers for.
func New(ctx context.Context, registry definition.ProviderRecordLookup, mapper definition.Mapper,
	cfg *Config) (definition.Step, func() error, error) {
	return upstream.New(ctx, registry, mapper, prerequisites, cfg)
}
