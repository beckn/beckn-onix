package main

import (
	"context"
	"fmt"

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
		IndexURL:       config["indexURL"],
		CatalogBaseURL: config["catalogBaseURL"],
	}
	if cfg.KeyID == "" {
		return nil, fmt.Errorf("keyID is required")
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
