// Package main provides the plugin entry point for the VC Validator
// middleware. It is compiled as a Go plugin (.so) via
//
//	go build -buildmode=plugin -o plugins/vcvalidator.so \
//	    ./pkg/plugin/implementation/vcvalidator/cmd/plugin.go
//
// and loaded by the beckn-onix plugin manager at runtime, which looks up the
// exported Provider symbol.
package main

import (
	"context"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/vcvalidator"
)

type provider struct{}

// New builds the VC validator middleware from its YAML config map.
func (p provider) New(ctx context.Context, cfg map[string]string) (func(http.Handler) http.Handler, error) {
	return vcvalidator.NewMiddleware(cfg)
}

// Provider is the exported symbol that the beckn-onix plugin manager looks up.
var Provider = provider{}

// Compile-time assurance that Provider satisfies the middleware plugin contract.
var _ definition.MiddlewareProvider = Provider
