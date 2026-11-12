package catalogcrawler

// pg_reader_test.go — the crawler run for real against the publisher states
// deployed publishers are known to produce: the Postgres store, the
// Discovery sink (HTTP push to a recording server), crawlmanager and the
// catalog-core Fetcher, against a publishing node whose index and files the
// test sets directly. Only the registry key lookup is replaced by a static
// key.
//
// Needs a Postgres the test may create schemas in:
//
//	CATALOGCRAWLER_TEST_DSN=postgres://postgres:pw@127.0.0.1:5432/crawler go test ./pkg/plugin/implementation/catalogcrawler/
//
// Skipped when the variable is unset. Each test runs in its own schema.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogcrawler/internal/sink"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogcrawler/internal/store"
	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
	"github.com/beckn/catalog-core/pkg/security/artifactsigner"
)

const pgKeyID = "key-1"

var pgSchemaSeq atomic.Int64

// pgStore opens a store in a fresh schema of the test database.
func pgStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("CATALOGCRAWLER_TEST_DSN")
	if dsn == "" {
		t.Skip("CATALOGCRAWLER_TEST_DSN not set")
	}
	admin, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("t_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), pgSchemaSeq.Add(1))
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) //nolint:errcheck
		admin.Close()
	})
	return store.New(db), db
}

// pgPublisher is a publishing node whose index and files the test controls.
type pgPublisher struct {
	t    *testing.T
	srv  *httptest.Server
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey

	mu      sync.Mutex
	entries []catalog.CatalogEntry
	files   map[string][]byte
	etag    string
	onIndex func() // called (unlocked) before each index response
	delay   map[string]time.Duration
}

func newPGPublisher(t *testing.T) *pgPublisher {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &pgPublisher{t: t, pub: pub, priv: priv, files: map[string][]byte{}, delay: map[string]time.Duration{}}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pgPublisher) sign(doc any) (string, []byte) {
	p.t.Helper()
	unsigned, err := json.Marshal(doc)
	if err != nil {
		p.t.Fatal(err)
	}
	sig, err := artifactsigner.SignJSON(unsigned, "signature", p.priv)
	if err != nil {
		p.t.Fatal(err)
	}
	return sig, unsigned
}

func (p *pgPublisher) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	d := p.delay[r.URL.Path]
	hook := p.onIndex
	p.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	if r.URL.Path == "/index" && hook != nil {
		hook()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.URL.Path != "/index" {
		b, ok := p.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b) //nolint:errcheck
		return
	}
	if p.etag != "" {
		if r.Header.Get("If-None-Match") == p.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", p.etag)
	}
	raws := make([]json.RawMessage, 0, len(p.entries))
	for _, e := range p.entries {
		sig, _ := p.sign(e)
		e.Signature = catalog.EntrySignature{KeyID: pgKeyID, Value: sig}
		b, _ := json.Marshal(e)
		raws = append(raws, b)
	}
	out, _ := json.Marshal(map[string]any{"nodeId": "node-1", "catalogs": raws})
	w.Write(out) //nolint:errcheck
}

func (p *pgPublisher) setLive(id string, entryVersion, version int64, marker string) {
	p.t.Helper()
	doc := catalog.CatalogFileDoc{
		CatalogID: id, Version: version, NextUpdate: time.Now(),
		Catalog: json.RawMessage(fmt.Sprintf(`{"id":%q,"descriptor":{"name":%q},"provider":{"id":"prov"},"resources":[{"id":"r1"}]}`, id, marker)),
	}
	sig, _ := p.sign(doc)
	doc.Signature = catalog.FileSignature{KeyID: pgKeyID, Canonicalization: "JCS", Value: sig}
	body, _ := json.Marshal(doc)
	path := fmt.Sprintf("/files/%s.v%d.json", strings.ReplaceAll(id, "/", "_"), version)
	sum := sha256.Sum256(body)
	fe := catalog.FileEntry{Version: version, URL: p.srv.URL + path, Digest: "sha-256:" + hex.EncodeToString(sum[:])}
	p.mu.Lock()
	p.files[path] = body
	p.mu.Unlock()
	p.setEntry(catalog.CatalogEntry{CatalogID: id, EntryVersion: entryVersion, CatalogType: "REGULAR", Baseline: fe})
}

func (p *pgPublisher) setTombstone(id string, entryVersion int64, retiredAt string) {
	p.setEntry(catalog.CatalogEntry{CatalogID: id, EntryVersion: entryVersion, RetiredAt: retiredAt})
}

func (p *pgPublisher) setEntry(e catalog.CatalogEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.entries {
		if p.entries[i].CatalogID == e.CatalogID {
			p.entries[i] = e
			return
		}
	}
	p.entries = append(p.entries, e)
}

// pgDiscovery records every /push the sink makes.
type pgDiscovery struct {
	srv    *httptest.Server
	mu     sync.Mutex
	pushes map[string][]string // catalogId -> each push's descriptor.name, or "WIPE"
}

func newPGDiscovery(t *testing.T) *pgDiscovery {
	d := &pgDiscovery{pushes: map[string][]string{}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Message struct {
				Catalogs []struct {
					ID         string `json:"id"`
					Descriptor struct {
						Name string `json:"name"`
					} `json:"descriptor"`
					Resources []json.RawMessage `json:"resources"`
				} `json:"catalogs"`
			} `json:"message"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Message.Catalogs) != 1 {
			http.Error(w, "bad push", http.StatusBadRequest)
			return
		}
		c := req.Message.Catalogs[0]
		what := c.Descriptor.Name
		if len(c.Resources) == 0 {
			what = "WIPE"
		}
		d.mu.Lock()
		d.pushes[c.ID] = append(d.pushes[c.ID], what)
		d.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *pgDiscovery) history(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.pushes[id], ",")
}

func (d *pgDiscovery) serving(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	h := d.pushes[id]
	if len(h) == 0 {
		return ""
	}
	return h[len(h)-1]
}

type pgRig struct {
	t    *testing.T
	pub  *pgPublisher
	disc *pgDiscovery
	st   *store.Store
	db   *sql.DB
	p    crawlmanager.Params
}

func newPGRig(t *testing.T) *pgRig {
	t.Helper()
	st, db := pgStore(t)
	r := &pgRig{t: t, pub: newPGPublisher(t), disc: newPGDiscovery(t), st: st, db: db}
	keys := crawler.StaticKeys(map[string]ed25519.PublicKey{pgKeyID: r.pub.pub})
	r.p = crawlmanager.Params{
		Fetcher: catalog.NewFetcher(crawler.NewClient(5*time.Second, 1<<20, true), keys, 1<<20),
		Source:  stubSource{refs: []crawlmanager.IndexRef{{ParticipantID: "node-1", IndexURL: r.pub.srv.URL + "/index"}}},
		Sink:    sink.NewDiscoverySink(r.disc.srv.URL, "ds.test", "https://ds.test", 1<<20, 5*time.Second),
		Store:   st,
	}
	return r
}

func (r *pgRig) poll() {
	r.t.Helper()
	if err := r.p.PollIndexes(context.Background()); err != nil {
		r.t.Fatalf("PollIndexes: %v", err)
	}
}

func (r *pgRig) drain() []error {
	r.t.Helper()
	var errs []error
	for i := 0; i < 50; i++ {
		claimed, err := r.p.SyncNext(context.Background())
		if !claimed {
			if err != nil {
				r.t.Fatalf("SyncNext: %v", err)
			}
			return errs
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	r.t.Fatal("drain: queue did not empty")
	return nil
}

func (r *pgRig) crawl() []error {
	r.t.Helper()
	r.poll()
	return r.drain()
}

// row is one catalog's persisted state.
type pgRow struct {
	status, reason        string
	version, entryVersion int64
	queueStatus, queueOp  string
}

func (r *pgRig) row(id string) pgRow {
	r.t.Helper()
	var (
		out            pgRow
		st, rsn, qs, q sql.NullString
		v, ev          sql.NullInt64
	)
	err := r.db.QueryRow(`SELECT c.status, c.reason, c.version, c.entry_version, q.status, q.op
		FROM (SELECT $1::text AS id) x
		LEFT JOIN crawler_catalog c ON c.catalog_id = x.id
		LEFT JOIN crawler_queue q ON q.catalog_id = x.id`, id).Scan(&st, &rsn, &v, &ev, &qs, &q)
	if err != nil {
		r.t.Fatalf("row: %v", err)
	}
	out.status, out.reason, out.version, out.entryVersion, out.queueStatus, out.queueOp = st.String, rsn.String, v.Int64, ev.Int64, qs.String, q.String
	return out
}

func (r *pgRig) want(id, serving, status string) {
	r.t.Helper()
	if got := r.disc.serving(id); got != serving {
		r.t.Errorf("%s: discovery serves %q, want %q (push history: %s)", id, got, serving, r.disc.history(id))
	}
	if got := r.row(id); got.status != status {
		r.t.Errorf("%s: stored status %q, want %q (row %+v)", id, got.status, status, got)
	}
}

const pgCatA = "node-1/CAT-A"

// A plain publish, update, retire: the path every other scenario builds on.
func TestPG_PublishUpdateRetire(t *testing.T) {
	r := newPGRig(t)
	r.pub.setLive(pgCatA, 1, 1, "v1")
	r.crawl()
	r.pub.setLive(pgCatA, 2, 2, "v2")
	r.crawl()
	r.want(pgCatA, "v2", "active")
	r.pub.setTombstone(pgCatA, 3, "2026-11-12T10:00:00Z")
	if errs := r.crawl(); len(errs) != 0 {
		t.Fatalf("retire failed: %v", errs)
	}
	r.want(pgCatA, "WIPE", "retired")
	if got := r.disc.history(pgCatA); got != "v1,v2,WIPE" {
		t.Errorf("push history %q", got)
	}
	if row := r.row(pgCatA); row.queueStatus != "" {
		t.Errorf("queue row left behind: %+v", row)
	}
}

// v1.9.2 repeat retire: the only tombstone the crawler sees is at ev1,
// below its ev2 cursor.
func TestPG_RegressedTombstone_Retires(t *testing.T) {
	r := newPGRig(t)
	r.pub.setLive(pgCatA, 2, 2, "v2")
	r.crawl()
	r.pub.setTombstone(pgCatA, 1, "2026-11-12T10:00:05Z")
	r.crawl()
	r.want(pgCatA, "WIPE", "retired")
}

// The tombstone is rewritten (ev3, then ev1 with a new retiredAt, then
// again) while unrelated entries keep changing the index.
func TestPG_RepeatedTombstone_WipesOnce(t *testing.T) {
	r := newPGRig(t)
	r.pub.setLive(pgCatA, 2, 2, "v2")
	r.crawl()
	r.pub.setTombstone(pgCatA, 3, "2026-11-12T10:00:00Z")
	r.crawl()
	r.pub.setTombstone(pgCatA, 1, "2026-11-12T10:00:05Z")
	r.crawl()
	r.pub.setTombstone(pgCatA, 1, "2026-11-12T10:00:09Z")
	r.crawl()
	if got := r.disc.history(pgCatA); got != "v2,WIPE" {
		t.Errorf("push history %q, want one wipe", got)
	}
	if row := r.row(pgCatA); row.status != "retired" || row.entryVersion != 2 {
		t.Errorf("row %+v, want retired with the ev2 lineage kept", row)
	}
}

// catalog-core#4: a retired id is listed again (versions restarting at 1
// on v1.9.2, entryVersion continuing on v0.2.1).
func TestPG_RelistedAfterRetirement_NotServed(t *testing.T) {
	for _, relistEV := range []int64{1, 4} {
		t.Run(fmt.Sprintf("relisted at ev%d", relistEV), func(t *testing.T) {
			r := newPGRig(t)
			r.pub.setLive(pgCatA, 2, 2, "v2")
			r.crawl()
			r.pub.setTombstone(pgCatA, 3, "2026-11-12T10:00:00Z")
			r.crawl()
			r.pub.setLive(pgCatA, relistEV, 1, "relisted")
			r.crawl()
			r.pub.setLive(pgCatA, relistEV+1, 2, "relisted-2")
			r.crawl()
			r.want(pgCatA, "WIPE", "retired")
			if row := r.row(pgCatA); !strings.Contains(row.reason, "after it was retired") {
				t.Errorf("re-listing not visible in the stored reason: %+v", row)
			}
			rows, err := r.st.Status(context.Background(), "node-1", pgCatA)
			if err != nil || len(rows) != 1 || !rows[0].Retired || rows[0].Reason == "" {
				t.Errorf("status = %+v, %v; want retired with a reason", rows, err)
			}
		})
	}
}

// CON-TBD-11 against the real store.
func TestPG_RegressedLiveEntry_NotApplied(t *testing.T) {
	r := newPGRig(t)
	r.pub.setLive(pgCatA, 3, 3, "v3")
	r.crawl()
	r.pub.setLive(pgCatA, 1, 1, "old")
	r.crawl()
	r.want(pgCatA, "v3", "active")
	if row := r.row(pgCatA); row.entryVersion != 3 || row.version != 3 {
		t.Errorf("cursor moved: %+v", row)
	}
}

// A retire settled without an index URL -- what catalog-core up to v0.2.1
// passes -- must still be recorded, and must keep the URL the row had.
func TestPG_RetireSettleWithoutIndexURL(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	r.pub.setLive(pgCatA, 1, 1, "v1")
	r.crawl()
	if err := r.st.Enqueue(ctx, crawlmanager.QueueItem{CatalogID: pgCatA, IndexURL: r.pub.srv.URL + "/index", Op: crawlmanager.OpRetire}); err != nil {
		t.Fatal(err)
	}
	item, err := r.st.ClaimNext(ctx)
	if err != nil || item == nil {
		t.Fatalf("claim: %v %v", item, err)
	}
	if err := r.st.Complete(ctx, item.ID, item.ClaimID, crawlmanager.CatalogCursor{CatalogID: pgCatA, Retired: true}); err != nil {
		t.Fatalf("settling a retire without an index URL: %v", err)
	}
	c, seen, err := r.st.GetCatalogCursor(ctx, pgCatA)
	if err != nil || !seen || !c.Retired || c.IndexURL != r.pub.srv.URL+"/index" {
		t.Errorf("cursor = %+v seen=%v err=%v, want retired with its index URL kept", c, seen, err)
	}
}

// A worker whose claim has expired and been taken over by another worker
// must not write the cursor when it finally finishes.
func TestPG_StaleClaimCannotOverwriteCursor(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	r.pub.setLive(pgCatA, 4, 4, "v4")
	r.poll()
	slow, err := r.st.ClaimNext(ctx)
	if err != nil || slow == nil {
		t.Fatalf("claim: %v %v", slow, err)
	}
	// The slow worker's lease runs out; a second worker claims and finishes
	// against a newer index.
	if _, err := r.db.Exec(`UPDATE crawler_queue SET claimed_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	fast, err := r.st.ClaimNext(ctx)
	if err != nil || fast == nil {
		t.Fatalf("second claim: %v %v", fast, err)
	}
	if err := r.st.Complete(ctx, fast.ID, fast.ClaimID, crawlmanager.CatalogCursor{CatalogID: pgCatA, IndexURL: fast.IndexURL, Version: 5, EntryVersion: 5}); err != nil {
		t.Fatal(err)
	}
	// The slow worker now settles what it resolved earlier.
	err = r.st.Complete(ctx, slow.ID, slow.ClaimID, crawlmanager.CatalogCursor{CatalogID: pgCatA, IndexURL: slow.IndexURL, Version: 4, EntryVersion: 4})
	if err == nil {
		t.Errorf("Complete with a lost claim returned nil")
	}
	if row := r.row(pgCatA); row.entryVersion != 5 || row.version != 5 {
		t.Errorf("cursor = %+v, want ev5/v5 (the stale worker overwrote it)", row)
	}
	// What the stale worker pushed may have replaced newer content at the
	// destination, so the catalog must be queued for a fresh sync.
	if row := r.row(pgCatA); row.queueStatus != "queued" {
		t.Errorf("queue = %+v, want a fresh queued item after a lost claim", row)
	}
}

// The same, with the second worker still in flight when the first one
// finishes late.
func TestPG_StaleClaimWhileNewClaimInFlight(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	r.pub.setLive(pgCatA, 4, 4, "v4")
	r.poll()
	slow, _ := r.st.ClaimNext(ctx)
	if _, err := r.db.Exec(`UPDATE crawler_queue SET claimed_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	fast, err := r.st.ClaimNext(ctx)
	if err != nil || slow == nil || fast == nil {
		t.Fatalf("claims: %v %v %v", slow, fast, err)
	}
	if err := r.st.Complete(ctx, slow.ID, slow.ClaimID, crawlmanager.CatalogCursor{CatalogID: pgCatA, IndexURL: slow.IndexURL, Version: 4, EntryVersion: 4}); err == nil {
		t.Errorf("Complete with a lost claim returned nil")
	}
	if row := r.row(pgCatA); row.status != "" || row.queueStatus != "in_progress" {
		t.Errorf("row = %+v, want no cursor written and the new claim untouched", row)
	}
	if err := r.st.Complete(ctx, fast.ID, fast.ClaimID, crawlmanager.CatalogCursor{CatalogID: pgCatA, IndexURL: fast.IndexURL, Version: 5, EntryVersion: 5}); err != nil {
		t.Fatal(err)
	}
	// The late worker's push may have landed after the new worker's.
	if row := r.row(pgCatA); row.entryVersion != 5 || row.queueStatus != "queued" {
		t.Errorf("row = %+v, want ev5 and a fresh queued item", row)
	}
}

// A change announced while a sync of the same catalog is in flight must
// survive that sync completing. Otherwise, with an index that answers 304,
// nothing re-announces it until the publisher publishes again.
func TestPG_EnqueueDuringInFlightSync_IsNotLost(t *testing.T) {
	for _, op := range []crawlmanager.Op{crawlmanager.OpSync, crawlmanager.OpRetire} {
		t.Run(string(op), func(t *testing.T) {
			r := newPGRig(t)
			ctx := context.Background()
			r.pub.setLive(pgCatA, 1, 1, "v1")
			r.poll()
			inflight, err := r.st.ClaimNext(ctx)
			if err != nil || inflight == nil {
				t.Fatalf("claim: %v %v", inflight, err)
			}
			// While it is in flight, a poll announces a newer state.
			if err := r.st.Enqueue(ctx, crawlmanager.QueueItem{CatalogID: pgCatA, IndexURL: inflight.IndexURL, Op: op}); err != nil {
				t.Fatal(err)
			}
			if err := r.st.Complete(ctx, inflight.ID, inflight.ClaimID, crawlmanager.CatalogCursor{CatalogID: pgCatA, IndexURL: inflight.IndexURL, Version: 1, EntryVersion: 1}); err != nil {
				t.Fatal(err)
			}
			next, err := r.st.ClaimNext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if next == nil {
				t.Fatalf("the %s announced during the in-flight sync was lost", op)
			}
			if next.Op != op || next.Attempts != 0 {
				t.Errorf("next item = %+v, want op %s with a fresh attempt budget", next, op)
			}
		})
	}
}

// The same, end to end: the publisher retires while the sync of the last
// live version is being fetched, and its index answers 304 afterwards.
func TestPG_RetiredDuringInFlightSync_WithETag_StillRetires(t *testing.T) {
	r := newPGRig(t)
	r.pub.mu.Lock()
	r.pub.etag = `"e1"`
	r.pub.mu.Unlock()
	r.pub.setLive(pgCatA, 1, 1, "v1")
	r.poll()

	// The sync's own index read triggers the retirement and a poll that
	// sees it, before the sync completes.
	var once sync.Once
	fileDelay := make(chan struct{})
	r.pub.mu.Lock()
	r.pub.onIndex = func() {
		once.Do(func() {
			go func() {
				// runs after the sync has read the live index
				time.Sleep(50 * time.Millisecond)
				r.pub.setTombstone(pgCatA, 2, "2026-11-12T10:00:00Z")
				r.pub.mu.Lock()
				r.pub.etag = `"e2"`
				r.pub.mu.Unlock()
				r.p.PollIndexes(context.Background()) //nolint:errcheck
				close(fileDelay)
			}()
		})
	}
	r.pub.delay["/files/node-1_CAT-A.v1.json"] = 400 * time.Millisecond
	r.pub.mu.Unlock()

	r.drain()
	<-fileDelay
	r.pub.mu.Lock()
	r.pub.onIndex = nil
	r.pub.mu.Unlock()
	// Later polls get 304; later drains must find the retire.
	r.crawl()
	r.crawl()
	r.want(pgCatA, "WIPE", "retired")
}

// Several crawler replicas (or the scheduled tick and /crawl/trigger)
// polling and syncing the same publisher at once.
func TestPG_ConcurrentCrawlsOfOnePublisher(t *testing.T) {
	r := newPGRig(t)
	const catalogs, workers = 12, 6
	ids := make([]string, catalogs)
	for i := range ids {
		ids[i] = fmt.Sprintf("node-1/CAT-%02d", i)
		r.pub.setLive(ids[i], 1, 1, "v1")
	}
	run := func() {
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := r.p.PollIndexes(context.Background()); err != nil {
					t.Errorf("poll: %v", err)
				}
				for {
					claimed, err := r.p.SyncNext(context.Background())
					if err != nil {
						t.Errorf("sync: %v", err)
					}
					if !claimed {
						return
					}
				}
			}()
		}
		wg.Wait()
		r.drain()
	}
	run()
	for i, id := range ids {
		if i%3 == 0 {
			r.pub.setTombstone(id, 2, "2026-11-12T10:00:00Z")
		} else {
			r.pub.setLive(id, 2, 2, "v2")
		}
	}
	run()
	for i, id := range ids {
		row := r.row(id)
		if i%3 == 0 {
			if r.disc.serving(id) != "WIPE" || row.status != "retired" {
				t.Errorf("%s: serving %q, row %+v; want wiped and retired", id, r.disc.serving(id), row)
			}
		} else if r.disc.serving(id) != "v2" || row.status != "active" || row.entryVersion != 2 {
			t.Errorf("%s: serving %q, row %+v; want v2 active ev2", id, r.disc.serving(id), row)
		}
		if row.queueStatus != "" {
			t.Errorf("%s: queue row left behind: %+v", id, row)
		}
	}
}
