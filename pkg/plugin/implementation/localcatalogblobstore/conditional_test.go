package localcatalogblobstore_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/localcatalogblobstore"
)

func TestPutIfVersion_CreateOnlyThenMatchThenMismatch(t *testing.T) {
	s := localcatalogblobstore.New(t.TempDir())
	ctx := context.Background()

	if _, _, err := s.GetVersioned(ctx, "index/i.json"); !errors.Is(err, definition.ErrBlobNotFound) {
		t.Fatalf("GetVersioned on a missing file: %v", err)
	}
	if _, err := s.PutIfVersion(ctx, "index/i.json", []byte("x"), "sha256:nope"); !errors.Is(err, definition.ErrBlobPreconditionFailed) {
		t.Fatalf("a version named for a missing file: got %v", err)
	}
	v1, err := s.PutIfVersion(ctx, "index/i.json", []byte("one"), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.PutIfVersion(ctx, "index/i.json", []byte("again"), ""); !errors.Is(err, definition.ErrBlobPreconditionFailed) {
		t.Fatalf("create over an existing file: got %v", err)
	}
	got, read, err := s.GetVersioned(ctx, "index/i.json")
	if err != nil || string(got) != "one" || read != v1 || v1 == "" {
		t.Fatalf("GetVersioned = %q, %q, %v; PutIfVersion returned %q", got, read, err, v1)
	}
	v2, err := s.PutIfVersion(ctx, "index/i.json", []byte("two"), v1)
	if err != nil || v2 == v1 {
		t.Fatalf("replace at the current version: %q, %v", v2, err)
	}
	if _, err := s.PutIfVersion(ctx, "index/i.json", []byte("three"), v1); !errors.Is(err, definition.ErrBlobPreconditionFailed) {
		t.Fatalf("replace at a stale version: got %v", err)
	}
	if got, _ := s.Get(ctx, "index/i.json"); string(got) != "two" {
		t.Errorf("a refused write changed the file: %q", got)
	}
	// A plain Put moves the version too.
	if err := s.Put(ctx, "index/i.json", []byte("four")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIfVersion(ctx, "index/i.json", []byte("five"), v2); !errors.Is(err, definition.ErrBlobPreconditionFailed) {
		t.Fatalf("replace after a plain Put: got %v", err)
	}
}

// Several Store values over one directory -- what several adapter
// processes sharing a volume are -- increment one counter through
// read / PutIfVersion. No increment may be lost.
func TestPutIfVersion_ManyWritersOneRoot_NoLostUpdate(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	const stores, perStore, rounds = 4, 4, 25
	var wg sync.WaitGroup
	for range stores {
		s := localcatalogblobstore.New(root) // its own value: shares nothing but the directory
		for range perStore {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range rounds {
					for {
						cur, version, err := s.GetVersioned(ctx, "counter")
						if err != nil && !errors.Is(err, definition.ErrBlobNotFound) {
							t.Errorf("GetVersioned: %v", err)
							return
						}
						n, _ := strconv.Atoi(string(cur))
						_, err = s.PutIfVersion(ctx, "counter", []byte(strconv.Itoa(n+1)), version)
						if err == nil {
							break
						}
						if !errors.Is(err, definition.ErrBlobPreconditionFailed) {
							t.Errorf("PutIfVersion: %v", err)
							return
						}
					}
				}
			}()
		}
	}
	wg.Wait()
	got, err := localcatalogblobstore.New(root).Get(ctx, "counter")
	if err != nil {
		t.Fatal(err)
	}
	if want := strconv.Itoa(stores * perStore * rounds); string(got) != want {
		t.Fatalf("counter = %s, want %s: increments were lost", got, want)
	}
}

// A reader never sees an empty or partial file while writers replace
// it, and nothing but the blob and the lock file is left behind.
func TestPut_ReadersSeeWholeFilesAndNoTempFilesRemain(t *testing.T) {
	root := t.TempDir()
	s := localcatalogblobstore.New(root)
	ctx := context.Background()
	a, b := bytes.Repeat([]byte("a"), 1<<20), bytes.Repeat([]byte("b"), 1<<20)
	if err := s.Put(ctx, "catalogs/big.json", a); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				content := a
				if (i+w)%2 == 0 {
					content = b
				}
				if w == 0 {
					if err := s.Put(ctx, "catalogs/big.json", content); err != nil {
						t.Errorf("Put: %v", err)
						return
					}
					continue
				}
				_, version, _ := s.GetVersioned(ctx, "catalogs/big.json")
				if _, err := s.PutIfVersion(ctx, "catalogs/big.json", content, version); err != nil && !errors.Is(err, definition.ErrBlobPreconditionFailed) {
					t.Errorf("PutIfVersion: %v", err)
					return
				}
			}
		}()
	}
	for range 300 {
		got, err := s.Get(ctx, "catalogs/big.json")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
			t.Fatalf("read %d bytes that are neither version of the file", len(got))
		}
	}
	close(stop)
	wg.Wait()

	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if got := strings.Join(files, " "); got != ".catalogblobstore.lock catalogs/big.json" {
		t.Errorf("files left under the root: %s", got)
	}
}

// A PutIfVersion waiting for the lock gives up when its context ends.
func TestPutIfVersion_StopsWaitingWhenContextEnds(t *testing.T) {
	root := t.TempDir()
	s := localcatalogblobstore.New(root)
	release, err := localcatalogblobstore.HoldLockForTest(s)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := localcatalogblobstore.New(root).PutIfVersion(ctx, "f", []byte("x"), "")
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	release()
	if _, err := s.PutIfVersion(context.Background(), "f", []byte("x"), ""); err != nil {
		t.Fatalf("after release: %v", err)
	}
}
