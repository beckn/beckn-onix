package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogcrawler"
)

// catalogCrawlerProvider implements definition.CrawlerProvider.
type catalogCrawlerProvider struct {
	newFunc func(ctx context.Context, signer definition.Signer, km definition.KeyManager, cfg *catalogcrawler.Config) (*catalogcrawler.Crawler, func() error, error)
}

func (p catalogCrawlerProvider) parseConfig(config map[string]string) (*catalogcrawler.Config, error) {
	cfg := &catalogcrawler.Config{}

	if v, exists := config["maxArtifactSize"]; exists && v != "" {
		size, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid maxArtifactSize value '%s': %w", v, err)
		}
		if size <= 0 {
			return nil, fmt.Errorf("maxArtifactSize must be positive, got %d", size)
		}
		cfg.MaxArtifactSize = size
	}

	if v, exists := config["fetchTimeout"]; exists && v != "" {
		timeout, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid fetchTimeout value '%s': %w", v, err)
		}
		cfg.FetchTimeout = timeout
	}

	if v, exists := config["retryMax"]; exists && v != "" {
		retryMax, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid retryMax value '%s': %w", v, err)
		}
		if retryMax < 0 {
			return nil, fmt.Errorf("retryMax must be non-negative, got %d", retryMax)
		}
		cfg.RetryMax = retryMax
	}

	return cfg, nil
}

// New creates a new catalog-crawler plugin instance.
func (p catalogCrawlerProvider) New(ctx context.Context, signer definition.Signer, km definition.KeyManager, config map[string]string) (definition.Crawler, func() error, error) {
	cfg, err := p.parseConfig(config)
	if err != nil {
		log.Errorf(ctx, err, "Failed to parse catalog-crawler configuration")
		return nil, nil, fmt.Errorf("failed to parse catalog-crawler configuration: %w", err)
	}

	crawler, closer, err := p.newFunc(ctx, signer, km, cfg)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create catalog-crawler instance")
		return nil, nil, err
	}

	log.Infof(ctx, "catalog-crawler instance created successfully")
	return crawler, closer, nil
}

// Provider is the exported plugin instance.
var Provider = catalogCrawlerProvider{newFunc: catalogcrawler.New}
