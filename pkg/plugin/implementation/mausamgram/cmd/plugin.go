package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/mausamgram"
)

// mausamgramProvider implements definition.ProviderStepProvider.
type mausamgramProvider struct{}

// newStepFunc creates a new step. Indirected for tests.
var newStepFunc = mausamgram.New

// parseConfig turns the plugin config map into a typed Config. Anything absent
// is left zero: mausamgram.New applies the defaults and validates the auth
// scheme, so those rules live in one place.
func (p mausamgramProvider) parseConfig(config map[string]string) (*mausamgram.Config, error) {
	cfg := &mausamgram.Config{
		BindingKey:     config["bindingKey"],
		AuthScheme:     config["authScheme"],
		UsernameEnv:    config["usernameEnv"],
		PasswordEnv:    config["passwordEnv"],
		HeaderName:     config["headerName"],
		HeaderValueEnv: config["headerValueEnv"],
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

// New creates a new mausamgram provider step instance.
func (p mausamgramProvider) New(ctx context.Context, registry definition.ProviderRecordLookup, mapper definition.Mapper, config map[string]string) (definition.Step, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	cfg, err := p.parseConfig(config)
	if err != nil {
		log.Errorf(ctx, err, "Failed to parse mausamgram configuration")
		return nil, nil, fmt.Errorf("failed to parse mausamgram configuration: %w", err)
	}

	step, closer, err := newStepFunc(ctx, registry, mapper, cfg)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create mausamgram step")
		return nil, nil, err
	}

	log.Infof(ctx, "Mausamgram step created successfully")
	return step, closer, nil
}

// Provider is the exported plugin instance.
var Provider = mausamgramProvider{}

// Compile-time proof the provider satisfies the interface the manager asserts
// against. A mismatch is otherwise a runtime cast failure at startup.
var _ definition.ProviderStepProvider = Provider
