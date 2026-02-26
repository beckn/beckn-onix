// Package main provides the plugin entry point for the Policy Enforcer plugin.
// This file is compiled as a Go plugin (.so) and loaded by beckn-onix at runtime.
package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/policyenforcer"
)

// provider implements the PolicyEnforcerProvider interface for plugin loading.
type provider struct{}

// New creates a new PolicyEnforcer instance.
func (p provider) New(ctx context.Context, cfg map[string]string) (definition.PolicyEnforcer, func(), error) {
	enforcer, err := policyenforcer.New(cfg)
	if err != nil {
		return nil, nil, err
	}

	return enforcer, enforcer.Close, nil
}

// Provider is the exported symbol that beckn-onix plugin manager looks up.
var Provider = provider{}
