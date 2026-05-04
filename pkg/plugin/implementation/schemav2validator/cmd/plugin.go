package main

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/schemav2validator"
)

// schemav2ValidatorProvider provides instances of schemav2Validator.
type schemav2ValidatorProvider struct{}

// New initialises a new Schemav2Validator instance.
func (vp schemav2ValidatorProvider) New(ctx context.Context, config map[string]string) (definition.SchemaValidator, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	typeVal, hasType := config["type"]
	locVal, hasLoc := config["location"]

	if !hasType || typeVal == "" {
		return nil, nil, errors.New("type not configured")
	}
	if !hasLoc || locVal == "" {
		return nil, nil, errors.New("location not configured")
	}

	cfg := &schemav2validator.Config{
		Type:     typeVal,
		Location: locVal,
		CacheTTL: 3600,
	}

	if ttlStr, ok := config["cacheTTL"]; ok {
		if ttl, err := strconv.Atoi(ttlStr); err == nil && ttl > 0 {
			cfg.CacheTTL = ttl
		}
	}

	// NEW: Parse extendedSchema_enabled
	if enableStr, ok := config["extendedSchema_enabled"]; ok {
		cfg.EnableExtendedSchema = enableStr == "true"
	}

	// NEW: Parse Extended Schema config (if enabled)
	if cfg.EnableExtendedSchema {
		if v, ok := config["extendedSchema_cacheTTL"]; ok {
			if ttl, err := strconv.Atoi(v); err == nil && ttl > 0 {
				cfg.ExtendedSchemaConfig.CacheTTL = ttl
			}
		}
		if v, ok := config["extendedSchema_maxCacheSize"]; ok {
			if size, err := strconv.Atoi(v); err == nil && size > 0 {
				cfg.ExtendedSchemaConfig.MaxCacheSize = size
			}
		}
		if v, ok := config["extendedSchema_downloadTimeout"]; ok {
			if timeout, err := strconv.Atoi(v); err == nil && timeout > 0 {
				cfg.ExtendedSchemaConfig.DownloadTimeout = timeout
			}
		}
		if v, ok := config["extendedSchema_allowedDomains"]; ok && v != "" {
			cfg.ExtendedSchemaConfig.AllowedDomains = strings.Split(v, ",")
		}
		if v, ok := config["extendedSchema_devTest"]; ok {
			cfg.ExtendedSchemaConfig.DevTest = v == "true"
		}

	}

	return schemav2validator.New(ctx, cfg)
}

// Provider is the exported plugin provider.
var Provider schemav2ValidatorProvider
