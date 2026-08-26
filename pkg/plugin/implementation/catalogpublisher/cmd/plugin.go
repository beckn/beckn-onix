package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
)

// catalogPublisherProvider implements definition.CatalogPublisherProvider.
type catalogPublisherProvider struct {
	newFunc func(ctx context.Context, km definition.KeyManager, blobStore definition.CatalogBlobStore, registry definition.RegistryLookup, registryMetadata definition.RegistryMetadataLookup, cfg *catalogpublisher.Config) (*catalogpublisher.Publisher, func() error, error)
}

func (p catalogPublisherProvider) parseConfig(config map[string]string) (*catalogpublisher.Config, error) {
	cfg := &catalogpublisher.Config{
		SubscriberID:  config["subscriberId"],
		PublicBaseURL: config["catalogBaseURL"],
		// PublishLatest/Gzip default to on for this plugin entry point --
		// callers who want either off set publishLatest/gzip: "false"
		// explicitly. catalogpublisher.Config's own zero value stays false
		// for direct programmatic callers; these defaults belong to how
		// the plugin is wired, not the library itself.
		PublishLatest: true,
		Gzip:          true,
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

	if v, exists := config["publishLatest"]; exists && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid publishLatest value '%s': %w", v, err)
		}
		cfg.PublishLatest = b
	}

	if v, exists := config["gzip"]; exists && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid gzip value '%s': %w", v, err)
		}
		cfg.Gzip = b
	}

	// CompactionChangeCountThreshold/CompactionSizeRatioThreshold default
	// to 0 (disabled) here too, unlike PublishLatest/Gzip -- triggering
	// compaction changes write/version behavior, so it stays opt-in rather
	// than on by default.
	if v, exists := config["compactionChangeCountThreshold"]; exists && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid compactionChangeCountThreshold value '%s': %w", v, err)
		}
		cfg.CompactionChangeCountThreshold = n
	}

	if v, exists := config["compactionSizeRatioThreshold"]; exists && v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid compactionSizeRatioThreshold value '%s': %w", v, err)
		}
		cfg.CompactionSizeRatioThreshold = f
	}

	checkCatalogIndexLink, err := catalogpublisher.ParseCheckCatalogIndexLink(config)
	if err != nil {
		return nil, err
	}
	cfg.CheckCatalogIndexLink = checkCatalogIndexLink

	return cfg, nil
}

// New creates a new catalog-publisher plugin instance.
func (p catalogPublisherProvider) New(ctx context.Context, km definition.KeyManager, blobStore definition.CatalogBlobStore, registry definition.RegistryLookup, registryMetadata definition.RegistryMetadataLookup, config map[string]string) (definition.CatalogPublisher, func() error, error) {
	cfg, err := p.parseConfig(config)
	if err != nil {
		log.Errorf(ctx, err, "Failed to parse catalog-publisher configuration")
		return nil, nil, fmt.Errorf("failed to parse catalog-publisher configuration: %w", err)
	}

	publisher, closer, err := p.newFunc(ctx, km, blobStore, registry, registryMetadata, cfg)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create catalog-publisher instance")
		return nil, nil, err
	}

	log.Infof(ctx, "catalog-publisher instance created successfully")
	return publisher, closer, nil
}

// Provider is the exported plugin instance.
var Provider = catalogPublisherProvider{newFunc: catalogpublisher.New}
