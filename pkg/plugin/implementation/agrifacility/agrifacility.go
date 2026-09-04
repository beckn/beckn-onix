// Package agrifacility serves the network's agriculture facility capabilities.
//
// One package per schema pack family, so which plugin owns a capability is
// readable from its binding key: openagrinet:AgricultureFacility is
// agrifacility's, openagrinet:MandiPrice is mandi's.
//
// Named for the family and not for POCRA, deliberately. A provider is a
// registry row, and more than one could serve this same capability -- a second
// state aggregator would be another row and another mapping, not another
// package.
//
// Almost nothing lives here, and that is the point. Recognising a capability,
// resolving the call plan, authenticating, calling with the registry's budget
// and translating in both directions are all internal/upstream's, because none
// of them differ by domain. What this package owns is its name, and
// prerequisites -- the work a mapping cannot express, which is domain knowledge
// by definition.
//
// The upstream this was written against is POCRA's aggregator, whose search
// takes a category code and a point, both of which an AgricultureFacility
// payload carries. So the package is a name and nothing else: see
// prerequisites.go for why that is worth stating.
package agrifacility

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/upstream"
)

// Config is upstream's, unchanged. Aliased here so a domain plugin's cmd package
// need not know where the machinery lives.
type Config = upstream.Config

// New creates the agriculture facility step.
//
// Which capabilities it answers to is configuration, with no default: a package
// serving a family cannot guess which of them a deployment has providers for.
func New(ctx context.Context, registry definition.ProviderRecordLookup, mapper definition.Mapper,
	cfg *Config) (definition.Step, func() error, error) {
	return upstream.New(ctx, registry, mapper, prerequisites, cfg)
}
