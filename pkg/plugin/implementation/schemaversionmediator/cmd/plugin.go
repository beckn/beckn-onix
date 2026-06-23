// Package main provides the plugin entry point for the SchemaVersionMediator plugin.
// This file is compiled as a Go plugin (.so) and loaded by beckn-onix at runtime.
package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/schemaversionmediator"
)

type mediatorProvider struct{}

func (p mediatorProvider) New(ctx context.Context, loader definition.ManifestLoader, cfg map[string]string) (definition.SchemaVersionMediator, func() error, error) {
	return schemaversionmediator.New(ctx, loader, cfg)
}

// Provider is the exported symbol that beckn-onix plugin manager looks up.
var Provider = mediatorProvider{}
