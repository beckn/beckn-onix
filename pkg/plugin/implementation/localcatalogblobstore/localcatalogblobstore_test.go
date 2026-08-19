package localcatalogblobstore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/localcatalogblobstore"
)

func TestPutThenGet_RoundTrips(t *testing.T) {
	store := localcatalogblobstore.New(t.TempDir())
	if err := store.Put(context.Background(), "index/becknCatalogs.index.json", []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(context.Background(), "index/becknCatalogs.index.json")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestGet_Missing_ReturnsErrBlobNotFound(t *testing.T) {
	store := localcatalogblobstore.New(t.TempDir())
	_, err := store.Get(context.Background(), "index/becknCatalogs.index.json")
	if !errors.Is(err, definition.ErrBlobNotFound) {
		t.Errorf("got %v, want ErrBlobNotFound", err)
	}
}

func TestPut_CreatesParentDirs(t *testing.T) {
	root := t.TempDir()
	store := localcatalogblobstore.New(root)
	if err := store.Put(context.Background(), "catalogs/changes/CAT-1.v2.changes.json", []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "catalogs", "changes", "CAT-1.v2.changes.json")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestPut_Overwrites(t *testing.T) {
	store := localcatalogblobstore.New(t.TempDir())
	ctx := context.Background()
	if err := store.Put(ctx, "f", []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	if err := store.Put(ctx, "f", []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	got, err := store.Get(ctx, "f")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("got %q, want v2", got)
	}
}
