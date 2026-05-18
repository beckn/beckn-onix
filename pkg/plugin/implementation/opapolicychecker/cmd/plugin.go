// Package main provides the plugin entry point for the OPA Policy Checker plugin.
// This file is compiled as a Go plugin (.so) and loaded by beckn-onix at runtime.
package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/opapolicychecker"
)

// provider implements the PolicyCheckerProvider interface for plugin loading.
type provider struct{}

func (p provider) New(ctx context.Context, manifestLoader definition.ManifestLoader, cfg map[string]string) (definition.PolicyChecker, func(), error) {
	checker, err := opapolicychecker.NewWithManifestLoader(ctx, manifestLoader, cfg)
	if err != nil {
		return nil, nil, err
	}

	return checker, checker.Close, nil
}

// Provider is the exported symbol that beckn-onix plugin manager looks up.
var Provider = provider{}
