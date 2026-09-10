// Package main provides the plugin entry point for the JSONataTranslator
// plugin. This file is compiled as a Go plugin (.so) and loaded by
// beckn-onix at runtime.
package main

import (
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/jsonatatranslator"
)

// Provider is the exported symbol that the beckn-onix plugin manager looks up.
var Provider = jsonatatranslator.Provider
