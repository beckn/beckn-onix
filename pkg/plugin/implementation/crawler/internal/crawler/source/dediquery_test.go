package source

// dediquery_test.go — covers the DeDi registry /query RegistryClient against an
// httptest server returning fixtures in the live response shape: happy path,
// record filtering (non-live / no catalog_index_url / null meta), dedup across
// networks via NewRegistrySource, empty records, and error paths.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// happyBody is one /query response carrying two provider nodes that publish a
// catalog index.
const happyBody = `{
  "message": "ok",
  "data": {
    "registry_name": "testnet",
    "total_records": 2,
    "records": [
      {"details": {"subscriber_id": "staging.p1.example", "type": "BPP", "signing_public_key": "k1"},
       "meta": {"catalog_index_url": "https://p1.example/beckn/index/becknCatalogs.index.json", "manifestUrl": "https://p1.example/manifest"},
       "state": "live"},
      {"details": {"subscriber_id": "staging.p2.example", "type": "BPP", "signing_public_key": "k2"},
       "meta": {"catalog_index_url": "https://p2.example/beckn/index/becknCatalogs.index.json"},
       "state": "live"}
    ]
  }
}`

func TestDediQueryClient_Providers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/query/beckn.one/testnet" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(happyBody))
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	provs, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(provs) != 2 {
		t.Fatalf("got %d providers, want 2: %+v", len(provs), provs)
	}
	if provs[0].ParticipantID != "staging.p1.example" ||
		provs[0].IndexURL != "https://p1.example/beckn/index/becknCatalogs.index.json" {
		t.Fatalf("provs[0] = %+v", provs[0])
	}
	if provs[1].ParticipantID != "staging.p2.example" {
		t.Fatalf("provs[1] = %+v", provs[1])
	}
}

// mixedBody exercises the record filter: only the first record (live + a
// catalog_index_url) is a provider. The rest must be skipped without panicking:
// a BAP with no catalog_index_url, a not-yet-live provider, a record whose meta
// is null, and a record whose details are null (still a valid index → emitted
// with an empty ParticipantID).
const mixedBody = `{
  "message": "ok",
  "data": {
    "records": [
      {"details": {"subscriber_id": "prov.live.example", "type": "BPP"},
       "meta": {"catalog_index_url": "https://prov.live.example/i.json"},
       "state": "live"},
      {"details": {"subscriber_id": "bap.example", "type": "BAP"},
       "meta": {"manifestUrl": "https://bap.example/m"},
       "state": "live"},
      {"details": {"subscriber_id": "prov.pending.example", "type": "BPP"},
       "meta": {"catalog_index_url": "https://prov.pending.example/i.json"},
       "state": "created"},
      {"details": {"subscriber_id": "prov.nometa.example", "type": "BPP"},
       "meta": null,
       "state": "live"},
      {"details": null,
       "meta": {"catalog_index_url": "https://prov.nodetails.example/i.json"},
       "state": "live"}
    ]
  }
}`

func TestDediQueryClient_FiltersRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mixedBody))
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	provs, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err != nil {
		t.Fatal(err)
	}
	// The live BPP and the null-details-but-valid-index record survive the filter;
	// the BAP, the non-live provider, and the null-meta record are skipped.
	if len(provs) != 2 {
		t.Fatalf("got %d providers, want 2: %+v", len(provs), provs)
	}
	if provs[0].ParticipantID != "prov.live.example" || provs[0].IndexURL != "https://prov.live.example/i.json" {
		t.Fatalf("provs[0] = %+v", provs[0])
	}
	// A record with null details still yields an index ref, just without a
	// participant id.
	if provs[1].IndexURL != "https://prov.nodetails.example/i.json" || provs[1].ParticipantID != "" {
		t.Fatalf("provs[1] = %+v", provs[1])
	}
}

// TestRegistrySource_DedupsAcrossNetworks_ViaDediQuery drives the full source:
// two networks answered by one registry, sharing a provider index URL, must
// yield that URL once, tagged as a registry source.
func TestRegistrySource_DedupsAcrossNetworks_ViaDediQuery(t *testing.T) {
	// shared appears in both networks; each network also has one of its own.
	const shared = "https://shared.example/i.json"
	byPath := map[string]string{
		"/query/beckn.one/net1": `{"data":{"records":[
			{"details":{"subscriber_id":"a"},"meta":{"catalog_index_url":"` + shared + `"},"state":"live"},
			{"details":{"subscriber_id":"b"},"meta":{"catalog_index_url":"https://b.example/i.json"},"state":"live"}]}}`,
		"/query/beckn.one/net2": `{"data":{"records":[
			{"details":{"subscriber_id":"a"},"meta":{"catalog_index_url":"` + shared + `"},"state":"live"},
			{"details":{"subscriber_id":"c"},"meta":{"catalog_index_url":"https://c.example/i.json"},"state":"live"}]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := byPath[r.URL.Path]
		if !ok {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	src := NewRegistrySource(NewDediQueryClient(srv.URL, 5*time.Second), []string{"beckn.one/net1", "beckn.one/net2"})
	refs, err := src.IndexRefs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// shared + b + c = 3, not 4.
	if len(refs) != 3 {
		t.Fatalf("got %d refs, want 3 (deduped): %+v", len(refs), refs)
	}
	seen := map[string]int{}
	for _, r := range refs {
		if r.Source != KindRegistry {
			t.Errorf("ref %+v source != registry", r)
		}
		seen[r.IndexURL]++
	}
	if seen[shared] != 1 {
		t.Fatalf("shared index URL appears %d times, want 1", seen[shared])
	}
}

func TestDediQueryClient_EmptyRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":"ok","data":{"total_records":0,"records":[]}}`))
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	provs, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err != nil {
		t.Fatalf("empty records is not an error: %v", err)
	}
	if len(provs) != 0 {
		t.Fatalf("got %d providers, want 0", len(provs))
	}
}

func TestDediQueryClient_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	if _, err := c.Providers(context.Background(), "beckn.one/testnet"); err == nil {
		t.Fatal("expected an error on a non-200 response")
	}
}

func TestDediQueryClient_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("this is not json"))
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	if _, err := c.Providers(context.Background(), "beckn.one/testnet"); err == nil {
		t.Fatal("expected a parse error on a non-JSON body")
	}
}

func TestDediQueryClient_NetworkError(t *testing.T) {
	// A server that is closed before the request: the GET fails at the transport.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close()

	c := NewDediQueryClient(base, 2*time.Second)
	if _, err := c.Providers(context.Background(), "beckn.one/testnet"); err == nil {
		t.Fatal("expected a transport error when the registry is unreachable")
	}
}

// A response larger than the in-memory cap must be rejected (DoS guard), not
// read unbounded. The body is whitespace so the size check — not a parse error —
// is what trips.
func TestDediQueryClient_RejectsOversizeBody(t *testing.T) {
	oversized := bytes.Repeat([]byte(" "), maxQueryBytes+1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(oversized)
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	_, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err == nil {
		t.Fatal("expected an error when the response exceeds the size cap")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("error %q should report the size cap", err.Error())
	}
}

// The per-lookup timeout must actually bound a registry that never answers, so a
// stalled lookup cannot wedge a crawl pass. The handler blocks until the test
// releases it; the tiny client budget must fire first and return an error fast.
func TestDediQueryClient_TimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release // never answers within the client's budget
	}))
	defer srv.Close()
	defer close(release) // unblock the handler so the server shuts down cleanly

	c := NewDediQueryClient(srv.URL, 50*time.Millisecond)
	start := time.Now()
	_, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err == nil {
		t.Fatal("expected a timeout error when the registry never answers")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Providers took %v; the per-lookup timeout did not bound it", elapsed)
	}
}

// A non-positive timeout means "no per-lookup deadline" — the lookup still works,
// it just relies on the outer context / http.Client rather than a client budget.
func TestDediQueryClient_ZeroTimeoutNoLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(happyBody))
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 0)
	provs, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(provs) != 2 {
		t.Fatalf("got %d providers, want 2", len(provs))
	}
}

// A catalog_index_url that is only whitespace is not a usable URL and must be
// trimmed to empty and skipped, exactly like a missing field.
func TestDediQueryClient_SkipsWhitespaceOnlyIndexURL(t *testing.T) {
	const body = `{"data":{"records":[
		{"details":{"subscriber_id":"ws.example"},"meta":{"catalog_index_url":"   "},"state":"live"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	provs, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(provs) != 0 {
		t.Fatalf("whitespace-only catalog_index_url must be skipped, got %+v", provs)
	}
}
