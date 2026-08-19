// Package localcatalogblobstore is a filesystem-backed definition.CatalogBlobStore:
// Get/Put over paths rooted at a local directory. It carries no
// catalog-file vocabulary at all -- see catalogstore.Store for that; this
// package only ever reads/writes raw bytes at a path relative to Root.
package localcatalogblobstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// Store is a filesystem-backed definition.CatalogBlobStore rooted at Root.
type Store struct{ Root string }

// New constructs a Store rooted at root. Plain Go, no onix plugin
// machinery required.
func New(root string) *Store { return &Store{Root: root} }

// Get reads the file at path (a "/"-separated key, per
// definition.CatalogBlobStore's contract) relative to Root, returning
// definition.ErrBlobNotFound if it doesn't exist.
func (s *Store) Get(ctx context.Context, path string) ([]byte, error) {
	b, err := os.ReadFile(s.fullPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, definition.ErrBlobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("localcatalogblobstore: reading %s: %w", path, err)
	}
	return b, nil
}

// Put writes content to the file at path relative to Root, creating
// parent directories as needed. Writes to a temp file in the same
// directory and renames it into place -- rename is atomic on the same
// filesystem, so a concurrent Get() never observes a truncated/partial
// file, matching CatalogBlobStore's documented "atomic per-path
// overwrite" contract.
func (s *Store) Put(ctx context.Context, path string, content []byte) error {
	full := s.fullPath(path)
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("localcatalogblobstore: creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("localcatalogblobstore: creating temp file in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("localcatalogblobstore: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("localcatalogblobstore: writing %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("localcatalogblobstore: writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), full); err != nil {
		return fmt.Errorf("localcatalogblobstore: writing %s: %w", path, err)
	}
	return nil
}

// fullPath converts a "/"-separated blob key into an OS-native path under
// Root.
func (s *Store) fullPath(path string) string {
	return filepath.Join(s.Root, filepath.FromSlash(path))
}
