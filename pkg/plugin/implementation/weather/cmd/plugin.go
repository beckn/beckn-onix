package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/weather"
)

// weatherProvider implements definition.ProviderStepProvider.
type weatherProvider struct{}

// newStepFunc creates a new step. Indirected for tests.
var newStepFunc = weather.New

// parseConfig turns the plugin config map into a typed Config. Anything absent
// is left zero: weather.New applies the defaults and validates the auth
// scheme, so those rules live in one place.
func (p weatherProvider) parseConfig(config map[string]string) (*weather.Config, error) {
	cfg := &weather.Config{
		BindingKeys: splitList(config["bindingKeys"]),
		// Absent means the Beckn v2 convention. See upstream.Config for why
		// this is a default rather than something to set.
		ProviderIDAt:     config["providerIdAt"],
		CapabilityCodeAt: config["capabilityCodeAt"],
		AuthScheme:       config["authScheme"],
		UsernameEnv:      config["usernameEnv"],
		PasswordEnv:      config["passwordEnv"],
		HeaderName:       config["headerName"],
		HeaderValueEnv:   config["headerValueEnv"],
		QueryName:        config["queryName"],
		QueryValueEnv:    config["queryValueEnv"],
	}

	if raw, exists := config["maxResponseBytes"]; exists && raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid maxResponseBytes value '%s': %w", raw, err)
		}
		if value <= 0 {
			return nil, fmt.Errorf("maxResponseBytes must be positive, got %d", value)
		}
		cfg.MaxResponseBytes = value
	}

	return cfg, nil
}

// New creates a new weather provider step instance.
func (p weatherProvider) New(ctx context.Context, registry definition.ProviderRecordLookup, mapper definition.Mapper, config map[string]string) (definition.Step, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	cfg, err := p.parseConfig(config)
	if err != nil {
		log.Errorf(ctx, err, "Failed to parse weather configuration")
		return nil, nil, fmt.Errorf("failed to parse weather configuration: %w", err)
	}

	step, closer, err := newStepFunc(ctx, registry, mapper, cfg)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create weather step")
		return nil, nil, err
	}

	log.Infof(ctx, "Weather step created successfully")
	return step, closer, nil
}

// splitList reads a comma-separated config value, which is how a list reaches a
// plugin -- the config is map[string]string. Blanks are dropped and spaces
// trimmed, so a trailing comma or a wrapped line is not a config error.
//
// A comma is unambiguous here: a binding key separates its own halves with a
// pipe.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// Provider is the exported plugin instance.
var Provider = weatherProvider{}

// Compile-time proof the provider satisfies the interface the manager asserts
// against. A mismatch is otherwise a runtime cast failure at startup.
var _ definition.ProviderStepProvider = Provider
