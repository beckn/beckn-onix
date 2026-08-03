package main

import (
	"context"
	"fmt"
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
		SubscriberID:  config["subscriberId"],
		PublicBaseURL: config["catalogBaseURL"],
	}
	if cfg.SubscriberID == "" {
		return nil, fmt.Errorf("subscriberId is required")
	}

	if v, exists := config["nextUpdateIn"]; exists && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid nextUpdateIn value '%s': %w", v, err)
		}
		cfg.NextUpdateIn = d
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
