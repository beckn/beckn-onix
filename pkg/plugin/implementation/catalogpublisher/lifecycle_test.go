package catalogpublisher

// lifecycle_test.go drives one catalog through its whole life -- first
// publish, change, unchanged republish, metadata-only change, compaction,
// gzip and publishLatest switched between calls (a restart with new
// config: a new Publisher over the same blob store), retire, repeat retire
// -- through Publisher.Publish, so through the store adapter and post-write
// verification too. Every call must be reported without errors, and after
// every step the result is read back the way a crawler does: the index and
// every file over HTTP, verified with catalog.Fetcher. It remembers every
// "latest" URL the index ever advertised, because a consumer may still
// hold any of them.
//
// The same table runs against catalog-core alone in its
// pkg/catalog/publisher/lifecycle_test.go.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/publisher"
)

const lifecycleID = "example.test/CAT-1"

type lifecycleWorld struct {
	t       *testing.T
	ctx     context.Context
	km      *fakeKeyManager
	blobs   *fakeBlobStore
	srv     *httptest.Server
	fetcher *catalog.Fetcher

	// settings of the next call
	gzip, latest bool

	// what was last submitted
	items []string
	nets  []string

	// what a consumer remembers
	step         string
	everLatest   map[string]bool   // every latest URL the index ever advertised
	immutable    map[string][]byte // baseline/change URL -> bytes when first seen
	prevEntryRaw []byte
	prevEntryVer int64
	retiredAt    string
}

func newLifecycleWorld(t *testing.T, gz, latest bool) *lifecycleWorld {
	t.Helper()
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	blobs := newFakeBlobStore()
	w := &lifecycleWorld{
		t: t, ctx: context.Background(), km: km, blobs: blobs, srv: newVerifyingBlobServer(t, blobs),
		gzip: gz, latest: latest,
		items: []string{"ITEM-1"}, nets: []string{"net.example"},
		everLatest: map[string]bool{}, immutable: map[string][]byte{},
	}
	w.fetcher = w.publisher().fetcher
	return w
}

// publisher builds a Publisher with the current settings over the shared
// blob store -- what a restart with changed plugin config amounts to.
func (w *lifecycleWorld) publisher() *Publisher {
	w.t.Helper()
	p, _, err := New(w.ctx, w.km, w.blobs, fakeVerifyRegistry{PubB64: base64.StdEncoding.EncodeToString(w.km.pub)}, nil, &Config{
		SubscriberID: "k1", PublicBaseURL: w.srv.URL, AllowPrivateVerifyHosts: true,
		Gzip: w.gzip, PublishLatest: w.latest, NextUpdateIn: time.Hour,
	})
	if err != nil {
		w.t.Fatalf("New: %v", err)
	}
	return p
}

func (w *lifecycleWorld) errorf(format string, a ...any) {
	w.t.Helper()
	w.t.Errorf("[%s] "+format, append([]any{w.step}, a...)...)
}

// call runs one publish call; anything but a clean result fails the step.
func (w *lifecycleWorld) call(req definition.PublishRequest) definition.PublishResult {
	w.t.Helper()
	result, err := w.publisher().Publish(w.ctx, req)
	if err != nil {
		w.t.Fatalf("[%s] Publish: %v", w.step, err)
	}
	if len(result.Errors) != 0 {
		w.errorf("reported errors (REJECTED): %+v", result.Errors)
	}
	return result
}

func (w *lifecycleWorld) submission() definition.PublishRequest {
	return definition.PublishRequest{Catalogs: []definition.CatalogSubmission{{
		CatalogID: lifecycleID, Catalog: lifecycleCatalog(w.items), NetworkIds: w.nets,
	}}}
}

func lifecycleCatalog(items []string) json.RawMessage {
	resources := make([]string, len(items))
	for i, id := range items {
		resources[i] = `{"id":"` + id + `","descriptor":{"name":"` + id + `"}}`
	}
	return json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[` + strings.Join(resources, ",") + `]}`)
}

func (w *lifecycleWorld) get(url string) ([]byte, int) {
	w.t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		w.t.Fatalf("[%s] GET %s: %v", w.step, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode
}

// servedDoc fetches a "latest" URL the way a consumer holding only that URL
// does: decode by the URL's suffix, parse, and verify the file's own
// signature and catalogId through Fetcher.
func (w *lifecycleWorld) servedDoc(url string) (catalog.CatalogFileDoc, bool) {
	w.t.Helper()
	body, status := w.get(url)
	if status != http.StatusOK {
		w.errorf("%s: status %d", url, status)
		return catalog.CatalogFileDoc{}, false
	}
	if strings.HasSuffix(url, ".gz") {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			w.errorf("%s: not gzip although named .gz: %v", url, err)
			return catalog.CatalogFileDoc{}, false
		}
		if body, err = io.ReadAll(zr); err != nil {
			w.errorf("%s: gunzip: %v", url, err)
			return catalog.CatalogFileDoc{}, false
		}
	}
	var doc catalog.CatalogFileDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		w.errorf("%s: not a catalog file: %v", url, err)
		return catalog.CatalogFileDoc{}, false
	}
	sum := sha256.Sum256(body)
	fe := catalog.FileEntry{Version: doc.Version, URL: url, Digest: "sha-256:" + hex.EncodeToString(sum[:])}
	if _, err := w.fetcher.FetchFile(w.ctx, "example.test", lifecycleID, fe); err != nil {
		w.errorf("%s: does not verify as this catalog's file: %v", url, err)
		return catalog.CatalogFileDoc{}, false
	}
	return doc, true
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// check asserts the NFH-014 invariants on what is being served right now.
func (w *lifecycleWorld) check() {
	w.t.Helper()
	indexURL := publisher.IndexURL(w.srv.URL)

	// The entry, raw (for the "did the entry change" rule) and verified.
	rawIdx, status := w.get(indexURL)
	if status != http.StatusOK {
		w.t.Fatalf("[%s] index: status %d", w.step, status)
	}
	var idxDoc struct {
		NextUpdate time.Time         `json:"next_update"`
		Catalogs   []json.RawMessage `json:"catalogs"`
	}
	if err := json.Unmarshal(rawIdx, &idxDoc); err != nil {
		w.t.Fatalf("[%s] index: %v", w.step, err)
	}
	var entryRaw []byte
	for _, raw := range idxDoc.Catalogs {
		var probe struct {
			CatalogID string `json:"catalogId"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.CatalogID == lifecycleID {
			entryRaw = raw
		}
	}
	if !idxDoc.NextUpdate.After(time.Now()) {
		w.errorf("index next_update %s is not in the future", idxDoc.NextUpdate)
	}
	res, err := w.fetcher.FetchIndex(w.ctx, indexURL, catalog.IndexConditions{})
	if err != nil {
		w.t.Fatalf("[%s] FetchIndex: %v", w.step, err)
	}
	if len(res.Dropped) != 0 {
		w.errorf("index entries dropped: %+v", res.Dropped)
	}
	entry, ok := catalog.FindCatalog(res.Index, lifecycleID)
	if !ok || entryRaw == nil {
		w.t.Fatalf("[%s] entry not in the index", w.step)
	}

	// entryVersion: never regresses (CON-TBD-03); bumps on any change to the
	// entry and only then (NFH-014 Versioning).
	if w.prevEntryRaw != nil {
		changed := !bytes.Equal(w.prevEntryRaw, entryRaw)
		switch {
		case entry.EntryVersion < w.prevEntryVer:
			w.errorf("entryVersion regressed %d -> %d", w.prevEntryVer, entry.EntryVersion)
		case changed && entry.EntryVersion == w.prevEntryVer:
			w.errorf("entry changed but entryVersion stayed %d:\n before %s\n after  %s", entry.EntryVersion, w.prevEntryRaw, entryRaw)
		case !changed && entry.EntryVersion != w.prevEntryVer:
			w.errorf("entryVersion moved without the entry changing")
		}
	}
	w.prevEntryRaw, w.prevEntryVer = append([]byte{}, entryRaw...), entry.EntryVersion

	if w.retiredAt != "" {
		// CON-TBD-05: retiredAt never unset, never moved.
		if entry.RetiredAt != w.retiredAt {
			w.errorf("entry retiredAt = %q, was %q", entry.RetiredAt, w.retiredAt)
		}
	}

	if entry.IsRetired() {
		w.retiredAt = entry.RetiredAt
		if entry.Latest != nil || entry.Baseline.URL != "" {
			w.errorf("tombstone entry still lists files: %+v", entry)
		}
	} else {
		want := lifecycleCatalog(w.items)
		// Baseline and change files: verifiable, and immutable (CON-TBD-23).
		refs := append([]catalog.FileEntry{entry.Baseline}, entry.Changes...)
		for _, fe := range refs {
			if _, err := w.fetcher.FetchFile(w.ctx, "example.test", lifecycleID, fe); err != nil {
				w.errorf("%s: %v", fe.URL, err)
			}
			body, _ := w.get(fe.URL)
			if first, seen := w.immutable[fe.URL]; seen && !bytes.Equal(first, body) {
				w.errorf("%s was overwritten in place", fe.URL)
			} else if !seen {
				w.immutable[fe.URL] = body
			}
		}
		// latest: digest in sync with what is served (CON-TBD-36), version
		// is the current content-lineage version, content is what was
		// last submitted.
		if entry.Latest != nil {
			w.everLatest[entry.Latest.URL] = true
			got, err := w.fetcher.FetchFile(w.ctx, "example.test", lifecycleID, *entry.Latest)
			if err != nil {
				w.errorf("latest: %v", err)
			} else if !sameJSON(got, want) {
				w.errorf("latest content is not what was last submitted:\n got  %s\n want %s", got, want)
			}
			if entry.Latest.Version != entry.LatestVersion() {
				w.errorf("latest.version = %d, current version is %d", entry.Latest.Version, entry.LatestVersion())
			}
			if body, _ := w.get(entry.Latest.URL); entry.Latest.Size != int64(len(body)) {
				w.errorf("latest.size = %d, served %d bytes", entry.Latest.Size, len(body))
			}
			if gz := strings.HasSuffix(entry.Latest.URL, ".gz"); gz != (entry.Latest.Encoding == "gzip") {
				w.errorf("latest url %s and encoding %q disagree", entry.Latest.URL, entry.Latest.Encoding)
			}
		}
	}

	// Every latest URL a consumer could still hold.
	urls := make([]string, 0, len(w.everLatest))
	for u := range w.everLatest {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	for _, u := range urls {
		doc, ok := w.servedDoc(u)
		if !ok {
			continue
		}
		switch {
		case entry.IsRetired():
			// CON-TBD-38: the final write carries retiredAt, at every URL
			// that was advertised as "latest".
			if doc.RetiredAt == nil {
				w.errorf("%s still serves the live catalog after retirement (no retiredAt)", u)
			} else if doc.RetiredAt.Format(time.RFC3339Nano) != entry.RetiredAt {
				w.errorf("%s retiredAt %s differs from the index entry's %s", u, doc.RetiredAt.Format(time.RFC3339Nano), entry.RetiredAt)
			}
		case doc.RetiredAt != nil:
			w.errorf("%s carries retiredAt although the catalog is live", u)
		case entry.Latest != nil && entry.Latest.URL != u:
			// The stable URL moved: this one is frozen at old content and
			// nothing tells its consumer.
			w.errorf("%s was advertised as latest and is now abandoned for %s", u, entry.Latest.URL)
		}
	}

	// Nothing unservable under a "latest" name in the blob store.
	w.blobs.mu.Lock()
	defer w.blobs.mu.Unlock()
	for key, b := range w.blobs.data {
		if !strings.Contains(key, ".latest.") {
			continue
		}
		isGzip := len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
		if isGzip != strings.HasSuffix(key, ".gz") {
			w.errorf("blob %s: content encoding does not match its name", key)
		}
	}
}

// run applies ops in order, checking after every publish call.
func (w *lifecycleWorld) run(ops []string) {
	w.t.Helper()
	for i, op := range ops {
		w.step = fmt.Sprintf("%d:%s gzip=%v latest=%v", i+1, op, w.gzip, w.latest)
		var modes string
		var result definition.PublishResult
		switch op {
		case "gzip":
			w.gzip = !w.gzip
			continue
		case "latest":
			w.latest = !w.latest
			continue
		case "publish":
			result, modes = w.call(w.submission()), "baseline"
		case "change":
			w.items = append(w.items, fmt.Sprintf("ITEM-%d", len(w.items)+1))
			result, modes = w.call(w.submission()), "change"
		case "unchanged":
			// "metadata" is right when the entry itself had to change
			// (its latest reference appeared, went away or was repaired).
			result, modes = w.call(w.submission()), "unchanged metadata"
		case "metadata":
			w.nets = append(w.nets, fmt.Sprintf("net%d.example", len(w.nets)))
			result, modes = w.call(w.submission()), "metadata"
		case "compact":
			req := w.submission()
			req.Catalogs[0].ForceBaseline = true
			result, modes = w.call(req), "baseline"
		case "retire":
			w.call(definition.PublishRequest{Retire: []string{lifecycleID}})
		default:
			w.t.Fatalf("unknown op %q", op)
		}
		if modes != "" {
			if len(result.Catalogs) != 1 || !strings.Contains(modes, result.Catalogs[0].Mode) {
				w.errorf("outcomes = %d, want one with mode in %q", len(result.Catalogs), modes)
			}
		}
		w.check()
	}
}

var lifecycleSequences = []struct {
	name string
	ops  string
}{
	{"unchanged right after the first publish", "publish unchanged unchanged change unchanged retire"},
	{"whole life", "publish change unchanged metadata unchanged compact unchanged change retire retire"},
	{"gzip toggled then unchanged", "publish change gzip unchanged unchanged retire retire"},
	{"gzip toggled then change", "publish gzip change gzip metadata change retire retire"},
	{"gzip toggled then compaction", "publish change gzip compact change retire"},
	{"gzip toggled then retire", "publish change gzip retire retire"},
	{"gzip toggled before repeat retire", "publish change retire gzip retire"},
	{"latest toggled then unchanged", "publish latest unchanged unchanged latest unchanged unchanged retire retire"},
	{"latest toggled then change", "publish change latest change latest change retire retire"},
	{"latest toggled then retire", "publish change latest retire retire"},
	{"latest toggled, publish, then retire", "publish change latest change retire retire"},
	{"latest toggled before repeat retire", "publish change retire latest retire"},
	{"both toggled", "publish gzip latest change latest change gzip metadata retire retire"},
	{"both toggled then retire", "publish change latest metadata gzip latest unchanged gzip retire retire"},
}

func TestLifecycle_LatestStaysConsistent(t *testing.T) {
	for _, seq := range lifecycleSequences {
		for _, gz := range []bool{true, false} {
			for _, latest := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/gzip=%v/latest=%v", seq.name, gz, latest), func(t *testing.T) {
					newLifecycleWorld(t, gz, latest).run(strings.Fields(seq.ops))
				})
			}
		}
	}
}
