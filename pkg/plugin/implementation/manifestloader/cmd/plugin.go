package main

import (
	"context"
	"strconv"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/manifestloader"
)

type manifestLoaderProvider struct{}

var newManifestLoaderFunc = manifestloader.New

func (p manifestLoaderProvider) New(ctx context.Context, cache definition.Cache, registry definition.RegistryMetadataLookup, cfg map[string]string) (definition.ManifestLoader, func() error, error) {
	config := &manifestloader.Config{}
	if raw := cfg["cacheTTL"]; raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, nil, err
		}
		config.CacheTTL = d
	}
	if raw := cfg["fetchTimeoutSeconds"]; raw != "" {
		secs, err := strconv.Atoi(raw)
		if err != nil {
			return nil, nil, err
		}
		config.FetchTimeout = time.Duration(secs) * time.Second
	}
	log.Debugf(ctx, "ManifestLoader config mapped: %+v", config)
	return newManifestLoaderFunc(ctx, cache, registry, config)
}

var Provider = manifestLoaderProvider{}
