package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/jsonmapper"
)

// jsonMapperProvider implements definition.MapperProvider.
type jsonMapperProvider struct{}

// newMapperFunc creates a new mapper. Indirected for tests.
var newMapperFunc = jsonmapper.New

// parseConfig turns the plugin config map into a typed Config. Anything absent
// is left zero: jsonmapper.New applies the defaults, so they live in one place.

func (o jsonMapperProvider) parseConfig(config map[string]string) (*jsonmapper.Config, error) {
	cfg := &jsonmapper.Config{}

	if err := parseDuration(config, "fetchTimeout", &cfg.FetchTimeout); err != nil {
		return nil, err
	}
	if err := parseDuration(config, "cacheTTL", &cfg.CacheTTL); err != nil {
		return nil, err
	}
	if err := parseDuration(config, "negativeTTL", &cfg.NegativeTTL); err != nil {
		return nil, err
	}
	if err := parseInt64(config, "maxMappingBytes", &cfg.MaxMappingBytes); err != nil {
		return nil, err
	}

	if raw, exists := config["maxCacheEntries"]; exists && raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid maxCacheEntries value '%s': %w", raw, err)
		}
		if value <= 0 {
			return nil, fmt.Errorf("maxCacheEntries must be positive, got %d", value)
		}
		cfg.MaxCacheEntries = value
	}

	return cfg, nil
}

// parseDuration reads an optional duration setting into target.
func parseDuration(config map[string]string, key string, target *time.Duration) error {
	raw, exists := config[key]
	if !exists || raw == "" {
		return nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid %s value '%s': %w", key, raw, err)
	}
	if value <= 0 {
		return fmt.Errorf("%s must be positive, got %v", key, value)
	}
	*target = value
	return nil
}

// parseInt64 reads an optional byte-count setting into target.
func parseInt64(config map[string]string, key string, target *int64) error {
	raw, exists := config[key]
	if !exists || raw == "" {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid %s value '%s': %w", key, raw, err)
	}
	if value <= 0 {
		return fmt.Errorf("%s must be positive, got %d", key, value)
	}
	*target = value
	return nil
}

// New creates a new JSON mapper plugin instance.
func (o jsonMapperProvider) New(ctx context.Context, config map[string]string) (definition.Mapper, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	cfg, err := o.parseConfig(config)
	if err != nil {
		log.Errorf(ctx, err, "Failed to parse JSON mapper configuration")
		return nil, nil, fmt.Errorf("failed to parse oan mapper configuration: %w", err)
	}

	mapper, closer, err := newMapperFunc(ctx, cfg)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create JSON mapper instance")
		return nil, nil, err
	}

	log.Infof(ctx, "JSON mapper instance created successfully")
	return mapper, closer, nil
}

// Provider is the exported plugin instance.
var Provider = jsonMapperProvider{}

// Compile-time proof the provider satisfies the interface the manager asserts
// against. A mismatch is otherwise a runtime cast failure at startup.
var _ definition.MapperProvider = Provider
