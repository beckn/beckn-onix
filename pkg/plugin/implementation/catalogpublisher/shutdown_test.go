package catalogpublisher

// shutdown_test.go -- the catalog publish handler under the adapter's
// shutdown path (cmd/adapter: SIGTERM, http.Server.Shutdown with a 10s
// limit, then process exit) and under a process that dies part-way, over
// the real local blob store with its file lock.
//
// Shutdown waits for a request in flight, so a publish that is under way
// when SIGTERM arrives finishes and answers. One that outlasts the limit is
// cut off by the process exiting, which for the files is the same as the
// process dying: the second test covers that at every write.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/store"
)

// tempFilesUnder lists the blob store's unfinished temp files under root.
func tempFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasPrefix(info.Name(), ".tmp-") {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// lockIsFree reports whether a conditional write on the blob root goes
// through at once: nothing is holding the blob store's lock.
func lockIsFree(t *testing.T, p *Publisher) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cas := p.blobStore.(definition.ConditionalCatalogBlobStore)
	_, err := cas.PutIfVersion(ctx, "lock-probe", []byte(time.Now().String()), "no such version")
	return errors.Is(err, definition.ErrBlobPreconditionFailed)
}

// pausedAtIndexWrite holds this replica's first conditional write of the
// index until release is closed, and says when it got there.
type pausedAtIndexWrite struct {
	definition.ConditionalCatalogBlobStore
	once    sync.Once
	reached chan struct{}
	release chan struct{}
}

func (s *pausedAtIndexWrite) PutIfVersion(ctx context.Context, path string, content []byte, ifVersion string) (string, error) {
	if path == store.IndexPath() {
		s.once.Do(func() { close(s.reached); <-s.release })
	}
	return s.ConditionalCatalogBlobStore.PutIfVersion(ctx, path, content, ifVersion)
}

// SIGTERM arrives while a publish is between its file writes and its index
// write. The adapter calls http.Server.Shutdown: the request finishes, the
// caller gets its answer, the catalog is fully published and verifiable,
// and nothing is left held or half-written.
func TestShutdown_PublishInFlightCompletesAndAnswers(t *testing.T) {
	const id = "example.test/CAT-1"
	root := t.TempDir()
	paused := &pausedAtIndexWrite{reached: make(chan struct{}), release: make(chan struct{})}
	replicas := newReplicaPublishersAt(t, root, 2, func(i int, bs definition.CatalogBlobStore) definition.CatalogBlobStore {
		if i != 0 {
			return bs
		}
		paused.ConditionalCatalogBlobStore = bs.(definition.ConditionalCatalogBlobStore)
		return paused
	})
	h, err := NewHandler(context.Background(), newTestManager(replicas[0]), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	type answer struct {
		code int
		body []byte
		err  error
	}
	answered := make(chan answer, 1)
	go func() {
		body := hRequest{catalogs: []string{hCatalog(id, true, "ITEM-1")}}.body()
		resp, err := http.Post(srv.URL+"/catalog/publish", "application/json", bytes.NewBufferString(body))
		if err != nil {
			answered <- answer{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		answered <- answer{code: resp.StatusCode, body: b}
	}()
	<-paused.reached

	// What cmd/adapter's shutdown does on SIGTERM.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Config.Shutdown(shutdownCtx) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned (%v) while a publish was still in flight", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(paused.release)

	a := <-answered
	if a.err != nil || a.code != http.StatusOK {
		t.Fatalf("the caller of the in-flight publish got err=%v status=%d", a.err, a.code)
	}
	var resp publishResponse
	if err := json.Unmarshal(a.body, &resp); err != nil || len(resp.Results) != 1 || resp.Results[0].Status != catalogAccepted {
		t.Fatalf("response = %s (err %v), want the catalog ACCEPTED", a.body, err)
	}
	if err := <-shutdownDone; err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	verifyWholeCatalog(t, replicas[1], id)
	if !lockIsFree(t, replicas[1]) {
		t.Errorf("the blob store lock is still held after shutdown")
	}
	if left := tempFilesUnder(t, root); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

var errProcessGone = errors.New("injected: process exited")

// goneAfter fails its Nth write and every later one without touching the
// blob root: the process was gone by then.
type goneAfter struct {
	definition.ConditionalCatalogBlobStore
	writes atomic.Int64
	dieAt  int64
}

func (g *goneAfter) gone() bool {
	n := g.writes.Add(1)
	return g.dieAt > 0 && n >= g.dieAt
}

func (g *goneAfter) Put(ctx context.Context, path string, content []byte) error {
	if g.gone() {
		return errProcessGone
	}
	return g.ConditionalCatalogBlobStore.Put(ctx, path, content)
}

func (g *goneAfter) PutIfVersion(ctx context.Context, path string, content []byte, ifVersion string) (string, error) {
	if g.gone() {
		return "", errProcessGone
	}
	return g.ConditionalCatalogBlobStore.PutIfVersion(ctx, path, content, ifVersion)
}

// The adapter exits (shutdown limit reached, or killed) at each write of an
// update and of a retire. Whatever was written by then, a consumer never
// finds a listed baseline or change file missing or wrong, the lock is not
// left held, and another replica -- or the restarted one -- repeating the
// request completes it and is ACCEPTED.
func TestProcessExitsAtEachWrite_OtherReplicaCompletes(t *testing.T) {
	const id = "example.test/CAT-1"
	requests := map[string]definition.PublishRequest{
		"update": {Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogNamed("CAT-1", "second")}}},
		"retire": {Retire: []string{id}},
	}
	for name, req := range requests {
		t.Run(name, func(t *testing.T) {
			world := func(t *testing.T, dieAt int64) (dying, healthy *Publisher, counter *goneAfter, root string) {
				root = t.TempDir()
				counter = &goneAfter{dieAt: dieAt}
				replicas := newReplicaPublishersAt(t, root, 2, func(i int, bs definition.CatalogBlobStore) definition.CatalogBlobStore {
					if i != 0 {
						return bs
					}
					counter.ConditionalCatalogBlobStore = bs.(definition.ConditionalCatalogBlobStore)
					return counter
				})
				res, err := replicas[1].Publish(context.Background(), definition.PublishRequest{
					Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogNamed("CAT-1", "first")}},
				})
				if err != nil || len(res.Errors) != 0 {
					t.Fatalf("set-up: err=%v errors=%+v", err, res.Errors)
				}
				return replicas[0], replicas[1], counter, root
			}

			p, _, counter, _ := world(t, 0)
			if res, err := p.Publish(context.Background(), req); err != nil || len(res.Errors) != 0 {
				t.Fatalf("undisturbed: err=%v errors=%+v", err, res.Errors)
			}
			total := counter.writes.Load()
			if total < 2 {
				t.Fatalf("the request made %d writes", total)
			}

			for dieAt := int64(1); dieAt <= total; dieAt++ {
				t.Run(fmt.Sprintf("exits at write %d of %d", dieAt, total), func(t *testing.T) {
					dying, healthy, _, root := world(t, dieAt)
					if _, err := dying.Publish(context.Background(), req); !errors.Is(err, errProcessGone) {
						t.Fatalf("Publish = %v, want the injected exit", err)
					}

					// What a consumer sees right now.
					idxRes, err := healthy.fetcher.FetchIndex(context.Background(), healthy.IndexURL(), catalog.IndexConditions{})
					if err != nil {
						t.Fatalf("the index is not readable after the exit: %v", err)
					}
					entry, ok := catalog.FindCatalog(idxRes.Index, id)
					if !ok {
						t.Fatalf("%s is no longer in the index", id)
					}
					if !entry.IsRetired() {
						for _, fe := range append([]catalog.FileEntry{entry.Baseline}, entry.Changes...) {
							if _, err := healthy.fetcher.FetchFile(context.Background(), "example.test", id, fe); err != nil {
								t.Errorf("after the exit, %s does not verify: %v", fe.URL, err)
							}
						}
					}
					if !lockIsFree(t, healthy) {
						t.Fatalf("the blob store lock is still held")
					}

					// The other replica repeats the request.
					res, err := healthy.Publish(context.Background(), req)
					if err != nil || len(res.Errors) != 0 {
						t.Fatalf("repeat: err=%v errors=%+v", err, res.Errors)
					}
					idxRes, err = healthy.fetcher.FetchIndex(context.Background(), healthy.IndexURL(), catalog.IndexConditions{})
					if err != nil {
						t.Fatal(err)
					}
					entry, _ = catalog.FindCatalog(idxRes.Index, id)
					if name == "retire" {
						if !entry.IsRetired() {
							t.Errorf("after the repeat the catalog is not retired")
						}
						raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(store.LatestFilePath(id, true))))
						if err != nil {
							t.Fatalf("reading latest: %v", err)
						}
						plain, err := gunzipForTest(raw)
						if err != nil {
							t.Fatalf("latest is not gzip: %v", err)
						}
						var doc catalog.CatalogFileDoc
						if err := json.Unmarshal(plain, &doc); err != nil || doc.RetiredAt == nil || doc.RetiredAt.Format(time.RFC3339Nano) != entry.RetiredAt {
							t.Errorf("latest does not carry the tombstone's retiredAt (%s): %+v, err %v", entry.RetiredAt, doc.RetiredAt, err)
						}
					} else {
						verifyWholeCatalog(t, healthy, id)
					}
					if left := tempFilesUnder(t, root); len(left) != 0 {
						t.Errorf("temp files left behind: %v", left)
					}
				})
			}
		})
	}
}
