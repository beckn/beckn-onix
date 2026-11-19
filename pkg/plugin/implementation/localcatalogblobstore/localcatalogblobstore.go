// Package localcatalogblobstore is a filesystem-backed definition.CatalogBlobStore:
// Get/Put over paths rooted at a local directory. It carries no
// catalog-file vocabulary at all -- see catalogstore.Store for that; this
// package only ever reads/writes raw bytes at a path relative to Root.
//
// It also implements definition.ConditionalCatalogBlobStore, so several
// adapter processes can share one Root (a shared volume): see
// PutIfVersion.
package localcatalogblobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// lockFileName is the file under Root that PutIfVersion locks. A dot
// file, so it is never mistaken for a blob. It sits inside catalogs/,
// next to the temp files Put creates there, so that the top level of
// Root holds only the two directories of the published layout (index/
// and catalogs/) and nothing an operator syncing or auditing the root
// has to explain. Every adapter sharing Root must agree on this path.
const lockFileName = "catalogs/.catalogblobstore.lock"

// Store is a filesystem-backed definition.CatalogBlobStore rooted at Root.
type Store struct{ Root string }

var _ definition.ConditionalCatalogBlobStore = (*Store)(nil)

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

// GetVersioned is Get plus the content's version: a hash of the bytes.
// A content hash, rather than a modification time or inode, cannot
// mistake two different contents for one version whatever the
// filesystem's timestamp resolution.
func (s *Store) GetVersioned(ctx context.Context, path string) ([]byte, string, error) {
	b, err := s.Get(ctx, path)
	if err != nil {
		return nil, "", err
	}
	return b, versionOf(b), nil
}

func versionOf(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Put writes content to the file at path relative to Root, creating
// parent directories as needed. It writes a temp file in the same
// directory, syncs it, renames it into place and syncs the directory:
// rename is atomic on one filesystem, so a concurrent Get never observes
// a truncated or partial file, and the syncs mean that after a crash or
// power loss the path holds either the old content or the new, never an
// empty or short file.
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
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("localcatalogblobstore: writing %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("localcatalogblobstore: syncing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("localcatalogblobstore: writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), full); err != nil {
		return fmt.Errorf("localcatalogblobstore: writing %s: %w", path, err)
	}
	syncDir(dir)
	return nil
}

// syncDir makes a rename in dir durable. Best effort: some filesystems
// do not support syncing a directory, and the rename has already
// happened.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// PutIfVersion writes content at path only if the file's current
// version is ifVersion (empty: only if the file does not exist), and
// returns the new version; otherwise it returns
// definition.ErrBlobPreconditionFailed and writes nothing.
//
// The compare and the rename happen under an exclusive advisory lock
// (flock) on one lock file under Root, so they are atomic with respect
// to every other PutIfVersion on this Root -- from this process, or from
// any other process that has the same directory mounted. The kernel
// drops the lock if the process dies, so there is no stale lock to
// clean up. Readers and plain Put take no lock; they are safe because
// every write is a rename.
//
// This relies on the filesystem enforcing flock between the processes
// that share it. A local Linux filesystem does, including a Docker or
// Kubernetes volume mounted into several containers on one node; NFS
// does where the server supports locking. Some do not and report no
// error: a macOS host directory bind-mounted into Docker Desktop
// containers is one (see lock). If the lock cannot be taken, or turns
// out not to have been exclusive, PutIfVersion fails rather than report
// an unprotected write as safe.
func (s *Store) PutIfVersion(ctx context.Context, path string, content []byte, ifVersion string) (version string, err error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return "", fmt.Errorf("localcatalogblobstore: locking %s: %w", s.Root, err)
	}
	defer func() {
		if uerr := unlock(); uerr != nil && (err == nil || errors.Is(err, definition.ErrBlobPreconditionFailed)) {
			version, err = "", fmt.Errorf("localcatalogblobstore: %s: %w", s.Root, uerr)
		}
	}()

	current := ""
	b, err := s.Get(ctx, path)
	switch {
	case err == nil:
		current = versionOf(b)
	case !errors.Is(err, definition.ErrBlobNotFound):
		return "", err
	}
	if current != ifVersion {
		return "", fmt.Errorf("localcatalogblobstore: %s: %w", path, definition.ErrBlobPreconditionFailed)
	}
	if err := s.Put(ctx, path, content); err != nil {
		return "", err
	}
	return versionOf(content), nil
}

// fullPath converts a "/"-separated blob key into an OS-native path under
// Root.
func (s *Store) fullPath(path string) string {
	return filepath.Join(s.Root, filepath.FromSlash(path))
}
