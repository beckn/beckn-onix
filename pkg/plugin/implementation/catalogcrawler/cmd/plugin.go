package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogcrawler"
)

// catalogCrawlerProvider implements definition.CrawlerProvider.
type catalogCrawlerProvider struct{}

func (catalogCrawlerProvider) New(ctx context.Context, validator definition.SchemaValidator, config map[string]string) (definition.Crawler, func() error, error) {
	return catalogcrawler.New(ctx, validator, config)
}

// Provider is the exported plugin symbol the plugin manager looks up.
var Provider = catalogCrawlerProvider{}
