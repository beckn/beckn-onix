package catalogpublisher

// verify_lifecycle_test.go: post-write verification against a file server
// that does not serve what the blob store holds -- a cache in front of it
// that keeps the previous copy of a path for a while, or for good.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
	"github.com/beckn/catalog-core/pkg/catalog/store"
)

// cachingServer serves a fakeBlobStore, except that a frozen path keeps
// returning the bytes it had when it was frozen until its deadline passes.
type cachingServer struct {
	mu     sync.Mutex
	bs     *fakeBlobStore
	frozen map[string]frozenCopy
	*httptest.Server
}

type frozenCopy struct {
	body  []byte
	until time.Time // zero: for good
}

func newCachingServer(t *testing.T, bs *fakeBlobStore) *cachingServer {
	t.Helper()
	c := &cachingServer{bs: bs, frozen: map[string]frozenCopy{}}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		c.mu.Lock()
		f, ok := c.frozen[path]
		c.mu.Unlock()
		if ok && (f.until.IsZero() || time.Now().Before(f.until)) {
			w.Write(f.body)
			return
		}
		body, err := bs.Get(r.Context(), path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(c.Close)
	return c
}

// freeze keeps serving path's current bytes for d (0: for good).
func (c *cachingServer) freeze(t *testing.T, path string, d time.Duration) {
	t.Helper()
	body, err := c.bs.Get(context.Background(), path)
	if err != nil {
		t.Fatalf("freeze %s: %v", path, err)
	}
	f := frozenCopy{body: append([]byte{}, body...)}
	if d > 0 {
		f.until = time.Now().Add(d)
	}
	c.mu.Lock()
	c.frozen[path] = f
	c.mu.Unlock()
}

func (c *cachingServer) thaw() {
	c.mu.Lock()
	c.frozen = map[string]frozenCopy{}
	c.mu.Unlock()
}

type cachedWorld struct {
	t   *testing.T
	bs  *fakeBlobStore
	srv *cachingServer
	p   *Publisher
}

const cachedID = "example.test/CAT-1"

func newCachedWorld(t *testing.T, cfg Config) *cachedWorld {
	t.Helper()
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()
	srv := newCachingServer(t, bs)
	cfg.SubscriberID, cfg.PublicBaseURL, cfg.AllowPrivateVerifyHosts = "k1", srv.URL, true
	p, _, err := New(context.Background(), km, worldStore(bs), fakeVerifyRegistry{PubB64: base64.StdEncoding.EncodeToString(km.pub)}, nil, &cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &cachedWorld{t: t, bs: bs, srv: srv, p: p}
}

func (w *cachedWorld) publish(items ...string) definition.PublishResult {
	w.t.Helper()
	res, err := w.p.Publish(context.Background(), definition.PublishRequest{Catalogs: []definition.CatalogSubmission{{
		CatalogID: cachedID, Catalog: lifecycleCatalog(items),
	}}})
	if err != nil {
		w.t.Fatalf("Publish: %v", err)
	}
	return res
}

func (w *cachedWorld) retire() definition.PublishResult {
	w.t.Helper()
	res, err := w.p.Publish(context.Background(), definition.PublishRequest{Retire: []string{cachedID}})
	if err != nil {
		w.t.Fatalf("Publish(retire): %v", err)
	}
	return res
}

func (w *cachedWorld) indexEntry() json.RawMessage {
	w.t.Helper()
	raw, err := w.bs.Get(context.Background(), store.IndexPath())
	if err != nil {
		w.t.Fatalf("index: %v", err)
	}
	var idx struct {
		Catalogs []json.RawMessage `json:"catalogs"`
	}
	if err := json.Unmarshal(raw, &idx); err != nil || len(idx.Catalogs) != 1 {
		w.t.Fatalf("index: err=%v entries=%d", err, len(idx.Catalogs))
	}
	return idx.Catalogs[0]
}

const retryBudget = verifyRetryBaseDelay * (1<<(verifyRetryAttempts-1) - 1) // 3s at the defaults

// A cache that serves the previous "latest" for a short while after the
// overwrite must not turn a good publish into REJECTED; one that never
// catches up must, after the whole retry budget, and say why.
func TestVerify_LatestServedStale(t *testing.T) {
	latest := store.LatestFilePath(cachedID, false)

	t.Run("for 500ms: accepted", func(t *testing.T) {
		w := newCachedWorld(t, Config{PublishLatest: true})
		if res := w.publish("ITEM-1"); len(res.Errors) != 0 {
			t.Fatalf("first publish: %+v", res.Errors)
		}
		w.srv.freeze(t, latest, 500*time.Millisecond)
		start := time.Now()
		res := w.publish("ITEM-1", "ITEM-2")
		if len(res.Errors) != 0 {
			t.Fatalf("publish behind a briefly stale latest was rejected after %v: %+v", time.Since(start), res.Errors)
		}
	})

	t.Run("for good: rejected after the retry budget", func(t *testing.T) {
		w := newCachedWorld(t, Config{PublishLatest: true})
		w.publish("ITEM-1")
		w.srv.freeze(t, latest, 0)
		start := time.Now()
		res := w.publish("ITEM-1", "ITEM-2")
		elapsed := time.Since(start)
		if len(res.Errors) != 1 || res.Errors[0].Stage != "verify" || !strings.Contains(res.Errors[0].Reason, "earlier copy") {
			t.Fatalf("errors = %+v, want one verify error naming the stale copy", res.Errors)
		}
		if elapsed < retryBudget {
			t.Errorf("gave up after %v, want the whole retry budget (%v)", elapsed, retryBudget)
		}

		// The caller repeats the same publish: unchanged content, and it
		// must still be checked, not waved through.
		res = w.publish("ITEM-1", "ITEM-2")
		if len(res.Catalogs) != 1 || res.Catalogs[0].Changed {
			t.Fatalf("repeat publish: outcome %+v, want unchanged", res.Catalogs)
		}
		if len(res.Errors) != 1 {
			t.Fatalf("repeat publish while latest is still stale: errors = %+v, want it rejected again", res.Errors)
		}
		w.srv.thaw()
		if res = w.publish("ITEM-1", "ITEM-2"); len(res.Errors) != 0 {
			t.Fatalf("repeat publish once the cache caught up: %+v", res.Errors)
		}
	})
}

// A mismatch on a file at an immutable URL is never the previous copy:
// permanent, reported at once.
func TestVerify_ImmutableFileMismatchIsNotRetried(t *testing.T) {
	w := newCachedWorld(t, Config{PublishLatest: true})
	w.publish("ITEM-1")
	// The change file URL is served with other bytes from the start.
	w.bs.Put(context.Background(), store.CatalogFilePath(cachedID, 2, "changes.json", false), []byte(`{"fromVersion":1}`))
	w.srv.freeze(t, store.CatalogFilePath(cachedID, 2, "changes.json", false), 0)
	start := time.Now()
	res := w.publish("ITEM-1", "ITEM-2")
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Reason, "change file") {
		t.Fatalf("errors = %+v, want the change file rejected", res.Errors)
	}
	if elapsed := time.Since(start); elapsed >= verifyRetryBaseDelay {
		t.Errorf("took %v: a digest mismatch on an immutable file was retried", elapsed)
	}
}

// An index that still serves the previous entry must not pass: the files
// it lists are the previous ones, and they verify fine.
func TestVerify_StaleIndexIsNotAccepted(t *testing.T) {
	w := newCachedWorld(t, Config{})
	w.publish("ITEM-1")

	w.srv.freeze(t, store.IndexPath(), 500*time.Millisecond)
	req := definition.PublishRequest{Catalogs: []definition.CatalogSubmission{{CatalogID: cachedID, Catalog: lifecycleCatalog([]string{"ITEM-1"}), ForceBaseline: true}}}
	res, err := w.p.Publish(context.Background(), req)
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("index stale for 500ms: err=%v errors=%+v, want accepted once it catches up", err, res.Errors)
	}

	w.srv.freeze(t, store.IndexPath(), 0)
	res, err = w.p.Publish(context.Background(), req)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Reason, "index") {
		t.Fatalf("index stale for good: errors = %+v, want a verify error about the index", res.Errors)
	}
}

// An unchanged republish is verified: what is served at the latest URL has
// to match the entry even though this call wrote no file.
func TestVerify_UnchangedOutcomeIsVerified(t *testing.T) {
	w := newCachedWorld(t, Config{PublishLatest: true, Gzip: true})
	w.publish("ITEM-1")
	path := store.LatestFilePath(cachedID, true)
	good, _ := w.bs.Get(context.Background(), path)

	// The server serves something else at the latest URL; the blob store
	// is intact, so the publisher has nothing to repair.
	w.bs.Put(context.Background(), path, []byte("not the latest file"))
	w.srv.freeze(t, path, 0)
	w.bs.Put(context.Background(), path, good)

	res := w.publish("ITEM-1")
	if len(res.Catalogs) != 1 || res.Catalogs[0].Changed || res.Catalogs[0].LatestContent != nil {
		t.Fatalf("outcome = %+v, want unchanged with no latest write", res.Catalogs)
	}
	if len(res.Errors) != 1 || res.Errors[0].Stage != "verify" {
		t.Fatalf("errors = %+v, want the unchanged outcome rejected at verification", res.Errors)
	}
	w.srv.thaw()
	if res = w.publish("ITEM-1"); len(res.Errors) != 0 {
		t.Fatalf("after the server is fixed: %+v", res.Errors)
	}
}

// beckn-onix#947: a retire REJECTED because the final latest could not be
// verified must not turn into ACCEPTED just by being repeated. The repeat
// signs and writes nothing; it is verified all the same.
func TestVerify_RepeatRetireIsVerified(t *testing.T) {
	w := newCachedWorld(t, Config{PublishLatest: true})
	w.publish("ITEM-1")
	w.srv.freeze(t, store.LatestFilePath(cachedID, false), 0)

	if res := w.retire(); len(res.Errors) != 1 || res.Errors[0].Stage != "verify" {
		t.Fatalf("retire behind a stale latest: errors = %+v, want one verify error", res.Errors)
	}
	tombstone := w.indexEntry()

	res := w.retire()
	if len(res.Errors) != 1 || res.Errors[0].Stage != "verify" {
		t.Fatalf("repeat retire, latest still stale: errors = %+v, want it rejected again", res.Errors)
	}
	if len(res.Retirements) != 1 || !res.Retirements[0].AlreadyRetired || res.Retirements[0].SignedEntry != nil {
		t.Errorf("repeat retire outcome = %+v, want AlreadyRetired with nothing signed", res.Retirements)
	}

	w.srv.thaw()
	if res = w.retire(); len(res.Errors) != 0 {
		t.Fatalf("repeat retire once latest is served: %+v", res.Errors)
	}
	if string(tombstone) != string(w.indexEntry()) {
		t.Errorf("the tombstone entry changed across repeat retires")
	}
}

// A retire done by a release that named the final file from the current
// gzip setting (beckn-onix#944) left the advertised latest live. A repeat
// retire on this release repairs it and verifies both files.
func TestVerify_RepeatRetireRepairsLatestLeftLive(t *testing.T) {
	w := newCachedWorld(t, Config{PublishLatest: true, Gzip: true})
	w.publish("ITEM-1")
	gzPath, plainPath := store.LatestFilePath(cachedID, true), store.LatestFilePath(cachedID, false)
	live, _ := w.bs.Get(context.Background(), gzPath)
	if res := w.retire(); len(res.Errors) != 0 {
		t.Fatalf("retire: %+v", res.Errors)
	}
	tombstoneGz, _ := w.bs.Get(context.Background(), gzPath)
	plain, err := gunzipForTest(tombstoneGz)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	// What #944 left: tombstone under the plain name, live file under .gz.
	w.bs.Put(context.Background(), plainPath, plain)
	w.bs.Put(context.Background(), gzPath, live)

	res := w.retire()
	if len(res.Errors) != 0 {
		t.Fatalf("repeat retire: %+v", res.Errors)
	}
	if len(res.RetiredLatest) != 2 {
		t.Fatalf("RetiredLatest = %d files, want both latest names", len(res.RetiredLatest))
	}
	for _, path := range []string{gzPath, plainPath} {
		fe := catalog.FileEntry{URL: w.srv.URL + "/" + path}
		body, err := crawler.NewClient(5*time.Second, 1<<20, true).Get(context.Background(), fe.URL)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if strings.HasSuffix(path, ".gz") {
			if body, err = gunzipForTest(body); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}
		var doc catalog.CatalogFileDoc
		if err := json.Unmarshal(body, &doc); err != nil || doc.RetiredAt == nil {
			t.Errorf("%s: err=%v retiredAt=%v, want a tombstone", path, err, doc.RetiredAt)
		}
	}
}

func gunzipForTest(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}
