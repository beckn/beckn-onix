// Package main provides the plugin entry point for the VC Validator
// processing step. It is compiled as a Go plugin (.so) via
//
//	go build -buildmode=plugin -o plugins/vcvalidator.so \
//	    ./pkg/plugin/implementation/vcvalidator/cmd/plugin.go
//
// and loaded by the beckn-onix plugin manager at runtime, which looks up the
// exported Provider symbol.
package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/vcvalidator"
)

type provider struct{}

// New builds the VC validator step from its YAML config map.
func (p provider) New(ctx context.Context, cfg map[string]string) (definition.Step, func(), error) {
	step, err := vcvalidator.New(cfg)
	return step, nil, err
}

// Provider is the exported symbol that the beckn-onix plugin manager looks up.
var Provider = provider{}

// Compile-time assurance that Provider satisfies the step plugin contract.
var _ definition.StepProvider = Provider
