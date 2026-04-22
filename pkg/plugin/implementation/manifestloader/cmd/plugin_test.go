package main

import (
	"context"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

type stubCache struct{}

func (stubCache) Get(context.Context, string) (string, error)              { return "", nil }
func (stubCache) Set(context.Context, string, string, time.Duration) error { return nil }
func (stubCache) Delete(context.Context, string) error                     { return nil }
func (stubCache) Clear(context.Context) error                              { return nil }

type stubRegistry struct{}

func (stubRegistry) LookupRegistry(ctx context.Context, namespaceIdentifier, registryName string) (*model.RegistryMetadata, error) {
	return &model.RegistryMetadata{}, nil
}

func TestManifestLoaderProvider_New(t *testing.T) {
	ctx := context.Background()
	provider := manifestLoaderProvider{}
	loader, closer, err := provider.New(ctx, stubCache{}, stubRegistry{}, map[string]string{
		"cacheTTL":              "2h",
		"fetchTimeoutSeconds":   "10",
		"disableCache":          "true",
		"forceRefreshOnStartup": "true",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if loader == nil {
		t.Fatal("expected loader")
	}
	if closer == nil {
		t.Fatal("expected closer")
	}
}
