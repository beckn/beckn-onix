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

	typeVal := config["type"]
	locVal := config["location"]

	// Primary spec is optional — a non-Beckn deployment may rely solely on auxiliary specs.
	// Validation that at least something is loaded happens inside schemav2validator.New.
	if typeVal != "" && locVal == "" {
		return nil, nil, errors.New("location not configured")
	}
	if locVal != "" && typeVal == "" {
		return nil, nil, errors.New("type not configured")
	}

	cfg := &schemav2validator.Config{
		Type:     typeVal,
		Location: locVal,
		CacheTTL: 3600,
	}

	// Parse auxiliary specs from comma-separated auxiliary_types and auxiliary_locations.
	// Both lists must have the same length.
	auxTypes := config["auxiliary_types"]
	auxLocations := config["auxiliary_locations"]

	if auxTypes != "" || auxLocations != "" {
		// Guard against one key being set without the other before splitting —
		// strings.Split("", ",") returns [""] (length 1), not [], which would
		// pass the length check but produce a misleading empty-entry error.
		if auxTypes == "" {
			return nil, nil, errors.New("auxiliary_locations is set but auxiliary_types is missing")
		}
		if auxLocations == "" {
			return nil, nil, errors.New("auxiliary_types is set but auxiliary_locations is missing")
		}

		types := strings.Split(auxTypes, ",")
		locations := strings.Split(auxLocations, ",")

		if len(types) != len(locations) {
			return nil, nil, errors.New("auxiliary_types and auxiliary_locations must have the same number of comma-separated entries")
		}

		for i := range types {
			t := strings.TrimSpace(types[i])
			l := strings.TrimSpace(locations[i])
			if t == "" || l == "" {
				return nil, nil, errors.New("auxiliary_types and auxiliary_locations entries must not be empty")
			}
			cfg.Auxiliary = append(cfg.Auxiliary, schemav2validator.AuxSpec{Type: t, Location: l})
		}
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
		if v, ok := config["extendedSchema_localSchemaPath"]; ok && v != "" {
			cfg.ExtendedSchemaConfig.LocalSchemaPath = v
		}

	}

	return schemav2validator.New(ctx, cfg)
}

// Provider is the exported plugin provider.
var Provider schemav2ValidatorProvider
