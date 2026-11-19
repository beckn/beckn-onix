package catalogpublisher

// handler_identity_test.go is the catalog identity decision table at the
// HTTP boundary: a real request body goes into the catalogPublish handler,
// backed by the real Publisher, the real catalog-core store over an
// in-memory blob store, and real post-write verification over HTTP. Each
// cell checks what the response says against what is in the index and the
// blob store afterwards.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/store"
)

const (
	hID    = "example.test/CAT-1"
	hOther = "other.test/CAT-1" // same last path segment as hID
)

// idHarness is one catalogPublish handler over one blob store.
type idHarness struct {
	t  *testing.T
	bs *fakeBlobStore
	h  http.Handler
}

func newIDHarness(t *testing.T) *idHarness {
	t.Helper()
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()
	p := newVerifiablePublisher(t, km, bs, nil, &Config{SubscriberID: "k1", PublishLatest: true, Gzip: true})
	h, err := NewHandler(context.Background(), newTestManager(p), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &idHarness{t: t, bs: bs, h: h}
}

// hCatalog is one catalog object for a request body.
func hCatalog(id string, active bool, items ...string) string {
	res := make([]string, len(items))
	for i, it := range items {
		res[i] = fmt.Sprintf(`{"id":%q,"descriptor":{"name":%q}}`, it, it)
	}
	return fmt.Sprintf(`{"id":%q,"isActive":%v,"descriptor":{"name":"Test"},"provider":{},"resources":[%s]}`, id, active, strings.Join(res, ","))
}

// hRequest is a catalog/publish request body under construction.
type hRequest struct {
	catalogs   []string
	directives []string
	retire     []string
}

func (r hRequest) body() string {
	retire, _ := json.Marshal(r.retire)
	return fmt.Sprintf(`{"context":{"action":"catalog/publish"},"message":{"catalogs":[%s],"publishDirectives":[%s]},"retire":%s}`,
		strings.Join(r.catalogs, ","), strings.Join(r.directives, ","), retire)
}

// post sends body and returns the HTTP status and the decoded response
// (zero when the status is not 200).
func (h *idHarness) post(body string) (int, publishResponse) {
	h.t.Helper()
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body)))
	var resp publishResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			h.t.Fatalf("parsing response %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, resp
}

// must sends a set-up request in which every catalog has to be ACCEPTED.
func (h *idHarness) must(r hRequest) {
	h.t.Helper()
	code, resp := h.post(r.body())
	if code != http.StatusOK || resp.Status != publishOverallCompleted {
		h.t.Fatalf("set-up request failed: %d %+v", code, resp)
	}
	for _, res := range resp.Results {
		if res.Status != catalogAccepted {
			h.t.Fatalf("set-up request: %s was %s (%s)", res.CatalogID, res.Status, res.Reason)
		}
	}
}

func (h *idHarness) blobs() map[string]string {
	h.bs.mu.Lock()
	defer h.bs.mu.Unlock()
	out := make(map[string]string, len(h.bs.data))
	for p, b := range h.bs.data {
		out[p] = string(b)
	}
	return out
}

// entries returns every index entry's exact bytes, by catalogId.
func (h *idHarness) entries() map[string]string {
	h.t.Helper()
	raw, ok := h.blobs()[store.IndexPath()]
	if !ok {
		return map[string]string{}
	}
	var idx struct {
		Catalogs []json.RawMessage `json:"catalogs"`
	}
	if err := json.Unmarshal([]byte(raw), &idx); err != nil {
		h.t.Fatalf("parsing index: %v", err)
	}
	out := make(map[string]string, len(idx.Catalogs))
	for _, e := range idx.Catalogs {
		var probe struct {
			CatalogID string `json:"catalogId"`
		}
		_ = json.Unmarshal(e, &probe)
		if _, dup := out[probe.CatalogID]; dup {
			h.t.Fatalf("index lists %q twice", probe.CatalogID)
		}
		out[probe.CatalogID] = string(e)
	}
	return out
}

func (h *idHarness) entry(id string) (catalog.CatalogEntry, bool) {
	h.t.Helper()
	raw, ok := h.entries()[id]
	if !ok {
		return catalog.CatalogEntry{}, false
	}
	var e catalog.CatalogEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		h.t.Fatalf("parsing entry of %s: %v", id, err)
	}
	return e, true
}

// fileChanges lists the catalog files (everything but the index) that were
// added, removed or rewritten between two snapshots.
func fileChanges(before, after map[string]string) []string {
	var out []string
	for p, b := range after {
		if p == store.IndexPath() {
			continue
		}
		if old, ok := before[p]; !ok || old != b {
			out = append(out, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func sameEntries(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// resultFor returns the response rows for id.
func resultFor(resp publishResponse, id string) []catalogProcessingResult {
	var out []catalogProcessingResult
	for _, r := range resp.Results {
		if r.CatalogID == id {
			out = append(out, r)
		}
	}
	return out
}

// Prior states, as request sequences.
func hPriorNone(t *testing.T) *idHarness { return newIDHarness(t) }

func hDirective(id string, extra string) string {
	return fmt.Sprintf(`{"catalogId":%q,"catalogType":"REGULAR","visibleTo":["net-a"]%s}`, id, extra)
}

// hPriorActive: published then updated -- file version 2, entryVersion 2.
func hPriorActive(t *testing.T) *idHarness {
	h := newIDHarness(t)
	h.must(hRequest{catalogs: []string{hCatalog(hID, true, "ITEM-1")}, directives: []string{hDirective(hID, "")}})
	h.must(hRequest{catalogs: []string{hCatalog(hID, true, "ITEM-1", "ITEM-2")}, directives: []string{hDirective(hID, "")}})
	return h
}

func hPriorPaused(t *testing.T) *idHarness {
	h := newIDHarness(t)
	h.must(hRequest{catalogs: []string{hCatalog(hID, true, "ITEM-1")}, directives: []string{hDirective(hID, "")}})
	h.must(hRequest{catalogs: []string{hCatalog(hID, false, "ITEM-1", "ITEM-2")}, directives: []string{hDirective(hID, "")}})
	return h
}

func hPriorRetired(t *testing.T) *idHarness {
	h := hPriorActive(t)
	h.must(hRequest{retire: []string{hID}})
	return h
}

func hPriorNameTakenLive(t *testing.T) *idHarness {
	h := newIDHarness(t)
	h.must(hRequest{catalogs: []string{hCatalog(hOther, true, "OTHER-1")}})
	return h
}

func hPriorNameTakenRetired(t *testing.T) *idHarness {
	h := hPriorNameTakenLive(t)
	h.must(hRequest{retire: []string{hOther}})
	return h
}

type hWant struct {
	status catalogProcessingStatus
	reason string // substring of the row's reason ("" = do not check)

	// For ACCEPTED rows: the index entry afterwards.
	version      int   // file version in the response row (0 for a retire)
	entryVersion int64 // entry's entryVersion
	retired      bool
	isActive     bool
	catalogType  string
}

func TestHandlerIdentityDecisionTable(t *testing.T) {
	pub := func(cat string, directive string) hRequest {
		return hRequest{catalogs: []string{cat}, directives: []string{directive}}
	}
	same := hCatalog(hID, true, "ITEM-1", "ITEM-2")
	samePaused := hCatalog(hID, false, "ITEM-1", "ITEM-2")
	newer := hCatalog(hID, true, "ITEM-1", "ITEM-2", "ITEM-3")
	std := hDirective(hID, "")
	moreNetworks := fmt.Sprintf(`{"catalogId":%q,"catalogType":"REGULAR","visibleTo":["net-a","net-b"]}`, hID)
	master := fmt.Sprintf(`{"catalogId":%q,"catalogType":"MASTER","visibleTo":["net-a"]}`, hID)
	force := hDirective(hID, `,"forceBaseline":true`)
	retire := hRequest{retire: []string{hID}}

	accepted := func(version int, ev int64, active bool) hWant {
		return hWant{status: catalogAccepted, version: version, entryVersion: ev, isActive: active}
	}
	retiredOK := func(ev int64) hWant {
		return hWant{status: catalogAccepted, reason: "retired", entryVersion: ev, retired: true}
	}
	rejected := func(reason string) hWant { return hWant{status: catalogRejected, reason: reason} }
	masterAt := func(version int, ev int64) hWant {
		w := accepted(version, ev, true)
		w.catalogType = "MASTER"
		return w
	}

	cells := []struct {
		name  string
		prior func(*testing.T) *idHarness
		req   hRequest
		want  hWant
	}{
		{"none/publish", hPriorNone, pub(newer, std), accepted(1, 1, true)},
		{"none/publish paused", hPriorNone, pub(samePaused, std), accepted(1, 1, false)},
		{"none/publish as MASTER", hPriorNone, pub(newer, master), masterAt(1, 1)},
		{"none/force baseline", hPriorNone, pub(newer, force), accepted(1, 1, true)},
		{"none/retire", hPriorNone, retire, rejected("not found")},

		{"active/new content", hPriorActive, pub(newer, std), accepted(3, 3, true)},
		{"active/same content", hPriorActive, pub(same, std), accepted(2, 2, true)},
		{"active/metadata only", hPriorActive, pub(same, moreNetworks), accepted(2, 3, true)},
		{"active/pause", hPriorActive, pub(samePaused, std), accepted(3, 3, false)},
		{"active/type to MASTER", hPriorActive, pub(same, master), masterAt(2, 3)},
		{"active/force baseline", hPriorActive, pub(same, force), accepted(3, 3, true)},
		{"active/retire", hPriorActive, retire, retiredOK(3)},

		{"paused/resume", hPriorPaused, pub(same, std), accepted(3, 3, true)},
		{"paused/same content", hPriorPaused, pub(samePaused, std), accepted(2, 2, false)},
		{"paused/retire", hPriorPaused, retire, retiredOK(3)},

		{"retired/new content", hPriorRetired, pub(newer, std), rejected("new catalogId")},
		{"retired/same content", hPriorRetired, pub(same, std), rejected("new catalogId")},
		{"retired/metadata only", hPriorRetired, pub(same, moreNetworks), rejected("new catalogId")},
		{"retired/type to MASTER", hPriorRetired, pub(same, master), rejected("new catalogId")},
		{"retired/force baseline", hPriorRetired, pub(newer, force), rejected("new catalogId")},
		{"retired/retire again", hPriorRetired, retire, hWant{status: catalogAccepted, reason: "retired", entryVersion: 3, retired: true}},

		{"name taken by live id/publish", hPriorNameTakenLive, pub(newer, std), rejected(hOther)},
		{"name taken by live id/force baseline", hPriorNameTakenLive, pub(newer, force), rejected(hOther)},
		{"name taken by live id/retire", hPriorNameTakenLive, retire, rejected("not found")},

		{"name taken by retired id/publish", hPriorNameTakenRetired, pub(newer, std), rejected(hOther)},
		{"name taken by retired id/retire", hPriorNameTakenRetired, retire, rejected("not found")},
	}

	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			h := c.prior(t)
			entriesBefore, blobsBefore := h.entries(), h.blobs()

			code, resp := h.post(c.req.body())
			if code != http.StatusOK || resp.Status != publishOverallCompleted {
				t.Fatalf("HTTP %d, %+v", code, resp)
			}
			rows := resultFor(resp, hID)
			if len(rows) != 1 || len(resp.Results) != 1 {
				t.Fatalf("want exactly one result row, for %s; got %+v", hID, resp.Results)
			}
			row := rows[0]
			if row.Status != c.want.status {
				t.Fatalf("status = %s (%s), want %s", row.Status, row.Reason, c.want.status)
			}
			if c.want.reason != "" && !strings.Contains(row.Reason, c.want.reason) {
				t.Errorf("reason %q does not mention %q", row.Reason, c.want.reason)
			}
			entriesAfter, blobsAfter := h.entries(), h.blobs()

			if c.want.status == catalogRejected {
				if !sameEntries(entriesBefore, entriesAfter) {
					t.Errorf("REJECTED, but index entries changed:\n before %v\n after  %v", entriesBefore, entriesAfter)
				}
				if diff := fileChanges(blobsBefore, blobsAfter); len(diff) != 0 {
					t.Errorf("REJECTED, but files changed: %v", diff)
				}
				return
			}

			e, ok := h.entry(hID)
			if !ok {
				t.Fatalf("ACCEPTED, but %s is not in the index", hID)
			}
			if e.EntryVersion != c.want.entryVersion {
				t.Errorf("entryVersion = %d, want %d", e.EntryVersion, c.want.entryVersion)
			}
			if e.IsRetired() != c.want.retired {
				t.Errorf("retired = %v, want %v", e.IsRetired(), c.want.retired)
			}
			if row.Version != c.want.version {
				t.Errorf("response version = %d, want %d", row.Version, c.want.version)
			}
			if !c.want.retired && (e.IsActive == nil || *e.IsActive != c.want.isActive) {
				t.Errorf("isActive = %v, want %v", e.IsActive, c.want.isActive)
			}
			if c.want.catalogType != "" && e.CatalogType != c.want.catalogType {
				t.Errorf("catalogType = %q, want %q", e.CatalogType, c.want.catalogType)
			}
			for _, p := range fileChanges(blobsBefore, blobsAfter) {
				if _, wasThere := blobsBefore[p]; wasThere && !strings.Contains(p, ".latest.") {
					t.Errorf("immutable file %s was overwritten (CON-TBD-23)", p)
				}
			}
			for id, raw := range entriesBefore {
				if id != hID && entriesAfter[id] != raw {
					t.Errorf("entry of %s changed", id)
				}
			}
		})
	}
}

// TestHandlerIdentity_RetireUnknownIsRejectedNotRetired: catalog-core#5 as
// the caller sees it. Before, the row said ACCEPTED / "retired".
func TestHandlerIdentity_RetireUnknownIsRejectedNotRetired(t *testing.T) {
	h := hPriorActive(t)
	entriesBefore := h.entries()
	for _, id := range []string{"example.test/NEVER", "example.test/cat-1", "", " "} {
		code, resp := h.post(hRequest{retire: []string{id}}.body())
		rows := resultFor(resp, id)
		if code != http.StatusOK || len(rows) != 1 || rows[0].Status != catalogRejected || !strings.Contains(rows[0].Reason, "not found") {
			t.Errorf("retire %q: HTTP %d rows %+v, want one REJECTED row saying not found", id, code, resp.Results)
		}
	}
	if !sameEntries(entriesBefore, h.entries()) {
		t.Errorf("index entries changed: %v", h.entries())
	}
}

// TestHandlerIdentity_MixedBatch: one request with a valid publish, a valid
// retire and one of each kind of refusal. Every catalog gets exactly one
// row, and the valid ones are done.
func TestHandlerIdentity_MixedBatch(t *testing.T) {
	h := newIDHarness(t)
	h.must(hRequest{catalogs: []string{
		hCatalog("example.test/LIVE", true, "L-1"), hCatalog("example.test/GONE", true, "G-1"), hCatalog("example.test/TO-RETIRE", true, "R-1"),
	}})
	h.must(hRequest{retire: []string{"example.test/GONE"}})

	code, resp := h.post(hRequest{
		catalogs: []string{
			hCatalog("example.test/GONE", true, "G-2"),        // retired
			hCatalog("other.test/LIVE", true, "X-1"),          // file names taken
			hCatalog("example.test/BAD ID", true, "X-2"),      // unsafe new id
			`{"id":"example.test/NO-DESCRIPTOR"}`,             // invalid content
			hCatalog("example.test/NEW", true, "N-1"),         // valid
			hCatalog("example.test/LIVE", true, "L-1", "L-2"), // valid update
		},
		retire: []string{"example.test/NEVER", "example.test/TO-RETIRE"},
	}.body())
	if code != http.StatusOK || resp.Status != publishOverallCompleted {
		t.Fatalf("HTTP %d %+v", code, resp)
	}
	want := map[string]catalogProcessingStatus{
		"example.test/GONE": catalogRejected, "other.test/LIVE": catalogRejected, "example.test/BAD ID": catalogRejected,
		"example.test/NO-DESCRIPTOR": catalogRejected, "example.test/NEVER": catalogRejected,
		"example.test/NEW": catalogAccepted, "example.test/LIVE": catalogAccepted, "example.test/TO-RETIRE": catalogAccepted,
	}
	if len(resp.Results) != len(want) {
		t.Errorf("want %d rows, got %+v", len(want), resp.Results)
	}
	for id, status := range want {
		rows := resultFor(resp, id)
		if len(rows) != 1 || rows[0].Status != status {
			t.Errorf("%s: rows %+v, want one %s", id, rows, status)
		}
	}
	if e, ok := h.entry("example.test/NEW"); !ok || e.EntryVersion != 1 {
		t.Errorf("example.test/NEW not published")
	}
	if e, ok := h.entry("example.test/LIVE"); !ok || e.EntryVersion != 2 {
		t.Errorf("example.test/LIVE not updated: %+v", e)
	}
	if e, ok := h.entry("example.test/TO-RETIRE"); !ok || !e.IsRetired() {
		t.Errorf("example.test/TO-RETIRE not retired")
	}
	if e, ok := h.entry("example.test/GONE"); !ok || !e.IsRetired() || e.EntryVersion != 2 {
		t.Errorf("example.test/GONE is no longer the tombstone it was: %+v", e)
	}
	if n := len(h.entries()); n != 4 {
		t.Errorf("index has %d entries, want 4 (GONE, LIVE, NEW, TO-RETIRE)", n)
	}
}

// TestHandlerIdentity_SharedLastSegment: catalog-core#6 as the caller sees
// it. The second id is REJECTED and the first one's files are its own.
func TestHandlerIdentity_SharedLastSegment(t *testing.T) {
	h := newIDHarness(t)
	h.must(hRequest{catalogs: []string{hCatalog("a.example/CAT-1", true, "A-1")}})
	blobsBefore, entriesBefore := h.blobs(), h.entries()

	for _, id := range []string{"b.example/CAT-1", "CAT-1", "a.example/cat-1"} {
		_, resp := h.post(hRequest{catalogs: []string{hCatalog(id, true, "B-1")}}.body())
		rows := resultFor(resp, id)
		if len(rows) != 1 || rows[0].Status != catalogRejected || !strings.Contains(rows[0].Reason, "a.example/CAT-1") {
			t.Errorf("%s: rows %+v, want REJECTED naming a.example/CAT-1", id, resp.Results)
		}
	}
	if diff := fileChanges(blobsBefore, h.blobs()); len(diff) != 0 || !sameEntries(entriesBefore, h.entries()) {
		t.Errorf("a rejected id changed the first catalog: files %v", diff)
	}
	// Both in one request: the first listed is published, the second is not.
	h2 := newIDHarness(t)
	_, resp := h2.post(hRequest{catalogs: []string{hCatalog("a.example/CAT-1", true, "A-1"), hCatalog("b.example/CAT-1", true, "B-1")}}.body())
	if a, b := resultFor(resp, "a.example/CAT-1"), resultFor(resp, "b.example/CAT-1"); len(a) != 1 || a[0].Status != catalogAccepted || len(b) != 1 || b[0].Status != catalogRejected {
		t.Errorf("both in one request: %+v", resp.Results)
	}
}

// TestHandlerIdentity_SameIdInCatalogsAndRetire: the submission wins, as
// documented, and the response now says the retire was not done.
func TestHandlerIdentity_SameIdInCatalogsAndRetire(t *testing.T) {
	h := hPriorActive(t)
	code, resp := h.post(hRequest{catalogs: []string{hCatalog(hID, true, "ITEM-9")}, retire: []string{hID}}.body())
	if code != http.StatusOK || len(resp.Results) != 1 || resp.Results[0].Status != catalogAccepted || resp.Results[0].Reason == "retired" {
		t.Fatalf("HTTP %d %+v, want one ACCEPTED publish row", code, resp)
	}
	if e, _ := h.entry(hID); e.IsRetired() || e.EntryVersion != 3 {
		t.Errorf("entry: %+v, want live at entryVersion 3", e)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], hID) || !strings.Contains(resp.Warnings[0], "retire") {
		t.Errorf("warnings = %v, want one saying the retire of %s was ignored", resp.Warnings, hID)
	}
}

// TestHandlerIdentity_SameIdTwiceInRetire: one tombstone, one row.
func TestHandlerIdentity_SameIdTwiceInRetire(t *testing.T) {
	h := hPriorActive(t)
	code, resp := h.post(hRequest{retire: []string{hID, hID}}.body())
	if code != http.StatusOK || len(resp.Results) != 1 || resp.Results[0].Status != catalogAccepted {
		t.Fatalf("HTTP %d %+v, want exactly one ACCEPTED row", code, resp.Results)
	}
	if e, _ := h.entry(hID); !e.IsRetired() || e.EntryVersion != 3 {
		t.Errorf("entry: %+v, want a tombstone at entryVersion 3", e)
	}
}

// TestHandlerIdentity_SameIdTwiceInCatalogs: refused as a whole request,
// before anything is written (existing rule, pinned here).
func TestHandlerIdentity_SameIdTwiceInCatalogs(t *testing.T) {
	h := newIDHarness(t)
	code, _ := h.post(hRequest{catalogs: []string{hCatalog(hID, true, "A"), hCatalog(hID, true, "B")}}.body())
	if code != http.StatusBadRequest {
		t.Fatalf("HTTP %d, want 400", code)
	}
	if len(h.blobs()) != 0 {
		t.Errorf("a refused request wrote %d blobs", len(h.blobs()))
	}
}

// TestHandlerIdentity_IdIsNotTiedToTheSubscriber pins what the README and
// NFH-014 say today: the catalog's id is used verbatim, and its prefix is
// not required to be the publishing subscriber (NFH-014 lists enforcement
// as an open question). The subscriber here is "example.test".
func TestHandlerIdentity_IdIsNotTiedToTheSubscriber(t *testing.T) {
	h := newIDHarness(t)
	for _, id := range []string{"CAT-DEMO-1", "someone-else.test/CAT-2"} {
		_, resp := h.post(hRequest{catalogs: []string{hCatalog(id, true, "I-1")}}.body())
		if rows := resultFor(resp, id); len(rows) != 1 || rows[0].Status != catalogAccepted {
			t.Errorf("%s: %+v, want ACCEPTED", id, resp.Results)
		}
	}
}

// TestHandlerIdentity_MetadataIsRestatedOnEveryPublish pins an existing
// rule that surprises: catalogType and visibleTo are taken from this
// request's publishDirectives entry, not carried over. An update that
// leaves the entry out makes a MASTER catalog REGULAR and a
// network-scoped one visible to all, with an entryVersion bump.
func TestHandlerIdentity_MetadataIsRestatedOnEveryPublish(t *testing.T) {
	h := newIDHarness(t)
	cat := hCatalog(hID, true, "ITEM-1")
	h.must(hRequest{catalogs: []string{cat}, directives: []string{fmt.Sprintf(`{"catalogId":%q,"catalogType":"MASTER","visibleTo":["net-a"]}`, hID)}})
	if e, _ := h.entry(hID); e.CatalogType != "MASTER" || len(e.NetworkIDs) != 1 || e.EntryVersion != 1 {
		t.Fatalf("set-up entry: %+v", e)
	}
	h.must(hRequest{catalogs: []string{cat}})
	if e, _ := h.entry(hID); e.CatalogType != "REGULAR" || len(e.NetworkIDs) != 0 || e.EntryVersion != 2 {
		t.Errorf("after an update without publishDirectives: catalogType=%q networkIds=%v entryVersion=%d; want REGULAR, none, 2", e.CatalogType, e.NetworkIDs, e.EntryVersion)
	}
}
