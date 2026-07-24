package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
)

// catalogPublisherProvider implements definition.CatalogPublisherProvider.
type catalogPublisherProvider struct {
	newFunc func(ctx context.Context, km definition.KeyManager, cfg *catalogpublisher.Config) (*catalogpublisher.Publisher, func() error, error)
}

func (p catalogPublisherProvider) parseConfig(config map[string]string) (*catalogpublisher.Config, error) {
	cfg := &catalogpublisher.Config{
		KeyID:          config["keyID"],
		Domain:         config["domain"],
		IndexSchemaURL: config["indexSchemaURL"],
		IndexURL:       config["indexURL"],
		CatalogBaseURL: config["catalogBaseURL"],
	}
	if cfg.KeyID == "" {
		return nil, fmt.Errorf("keyID is required")
	}

	if v, exists := config["nextUpdateIn"]; exists && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid nextUpdateIn value '%s': %w", v, err)
		}
		cfg.NextUpdateIn = d
	}

	if v, exists := config["fileValidityIn"]; exists && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid fileValidityIn value '%s': %w", v, err)
		}
		cfg.FileValidityIn = d
	}

	// indexNetworkIds is a comma-separated list -- map[string]string config
	// has no native list support for simple string slices.
	if v, exists := config["indexNetworkIds"]; exists && v != "" {
		cfg.IndexNetworkIds = strings.Split(v, ",")
	}

	// indexAuthMethods/extraManifestFiles are JSON-encoded arrays -- the
	// only fields shaped as more than a flat string or comma-list.
	if v, exists := config["indexAuthMethods"]; exists && v != "" {
		var methods []definition.AuthMethod
		if err := json.Unmarshal([]byte(v), &methods); err != nil {
			return nil, fmt.Errorf("invalid indexAuthMethods value '%s': %w", v, err)
		}
		cfg.IndexAuthMethods = methods
	}
	if v, exists := config["extraManifestFiles"]; exists && v != "" {
		var extra []catalogpublisher.ManifestFileRef
		if err := json.Unmarshal([]byte(v), &extra); err != nil {
			return nil, fmt.Errorf("invalid extraManifestFiles value '%s': %w", v, err)
		}
		cfg.ExtraManifestFiles = extra
	}

	return cfg, nil
}

// New creates a new catalog-publisher plugin instance.
func (p catalogPublisherProvider) New(ctx context.Context, km definition.KeyManager, config map[string]string) (definition.CatalogPublisher, func() error, error) {
	cfg, err := p.parseConfig(config)
	if err != nil {
		log.Errorf(ctx, err, "Failed to parse catalog-publisher configuration")
		return nil, nil, fmt.Errorf("failed to parse catalog-publisher configuration: %w", err)
	}

	publisher, closer, err := p.newFunc(ctx, km, cfg)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create catalog-publisher instance")
		return nil, nil, err
	}

	log.Infof(ctx, "catalog-publisher instance created successfully")
	return publisher, closer, nil
}

// Provider is the exported plugin instance.
var Provider = catalogPublisherProvider{newFunc: catalogpublisher.New}
