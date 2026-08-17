package main

import (
	"context"
	"fmt"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/localcatalogblobstore"
)

// catalogBlobStoreProvider implements definition.CatalogBlobStoreProvider.
type catalogBlobStoreProvider struct{}

// New builds a localcatalogblobstore.Store from config's "root" key.
func (p catalogBlobStoreProvider) New(ctx context.Context, config map[string]string) (definition.CatalogBlobStore, func() error, error) {
	root := config["root"]
	if root == "" {
		return nil, nil, fmt.Errorf("localcatalogblobstore: config \"root\" is required")
	}
	return localcatalogblobstore.New(root), func() error { return nil }, nil
}

// Provider is the exported symbol the plugin manager looks for.
var Provider = catalogBlobStoreProvider{}
