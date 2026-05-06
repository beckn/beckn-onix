// Package main is the plugin entry point compiled with -buildmode=plugin.
package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/agentenginetransportwrapper"
)

type agentEngineProvider struct{}

func (p agentEngineProvider) New(ctx context.Context, config map[string]any) (definition.TransportWrapper, func(), error) {
	w, closer, err := agentenginetransportwrapper.New(ctx, config)
	if err != nil {
		// Return a true nil interface (not a typed nil) so callers can rely on
		// `wrapper == nil` checks.
		return nil, nil, err
	}
	return w, closer, nil
}

// Provider is the symbol the plugin manager looks up.
var Provider = agentEngineProvider{}
