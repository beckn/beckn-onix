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
// parent directories as needed.
func (s *Store) Put(ctx context.Context, path string, content []byte) error {
	full := s.fullPath(path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("localcatalogblobstore: creating %s: %w", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return fmt.Errorf("localcatalogblobstore: writing %s: %w", path, err)
	}
	return nil
}

// fullPath converts a "/"-separated blob key into an OS-native path under
// Root.
func (s *Store) fullPath(path string) string {
	return filepath.Join(s.Root, filepath.FromSlash(path))
}
