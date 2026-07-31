package crawler_test

// e2e_test.go — the whole chain in one test, with nothing stubbed that the
// crawler is responsible for.
//
// A publisher origin serves a real signed catalog index and its catalog files.
// A registry answers key lookups the way the network registry will. Discovery
// receives the push. Postgres is real (set CRAWLER_TEST_DB_DSN). Everything in
// between — fetch, size caps, digest, per-file signature verification against
// the registry key, changeset resolution, batching, the work queue, the version
// cursor — is the production code path.
//
// The point is to prove the signature gate is LIVE rather than merely present:
// the happy path must push, and a catalog signed with the wrong key must park
// with nothing reaching Discovery.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	crawler "github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/config"
)

const (
	e2eSubscriberID = "sunrise-ev.example.org"
	e2eKeyID        = "e2e-key-1"
	e2eNetworkID    = "beckn.one/testnet"
)

// uniqueID keeps each run hermetic. The catalog cursor is persisted in Postgres
// and keyed by catalogId, so a fixed id would let one run inherit a previous
// run's version and silently skip the work under test.
func uniqueID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

// e2eRegistry is the network registry: it answers Lookup by {subscriber, key}
// exactly as definition.RegistryLookup does, and counts calls so the test can
// show the key cache is working.
type e2eRegistry struct {
	pub    ed25519.PublicKey
	status string
	calls  int
}

func (r *e2eRegistry) Lookup(_ context.Context, req *model.Subscription) ([]model.Subscription, error) {
	r.calls++
	if req.KeyID != e2eKeyID {
		return nil, nil
	}
	status := r.status
	if status == "" {
		status = "SUBSCRIBED"
	}
	var sub model.Subscription
	sub.KeyID = req.KeyID
	sub.SigningPublicKey = base64.StdEncoding.EncodeToString(r.pub)
	sub.Status = status
	return []model.Subscription{sub}, nil
}

// signTuple reproduces artifactsigner.SignFileTuple: an Ed25519 signature over
// the JCS-canonical {catalogId, version, url, digest, validUntil}.
func signTuple(t *testing.T, priv ed25519.PrivateKey, catalogID string, version int, url, digest string, validUntil time.Time) string {
	t.Helper()
	canonical, err := json.Marshal(map[string]any{
		"catalogId":  catalogID,
		"version":    version,
		"url":        url,
		"digest":     digest,
		"validUntil": validUntil,
	})
	if err != nil {
		t.Fatalf("canonicalize tuple: %v", err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, canonical))
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha-256:" + hex.EncodeToString(sum[:])
}

// baselineCatalog is a minimal v2.0 catalog document with two resources.
func baselineCatalog(catalogID string) []byte {
	return []byte(`{
  "id": "` + catalogID + `",
  "descriptor": {"name": "Sunrise EV Charging"},
  "provider": {"id": "prov-sunrise", "descriptor": {"name": "Sunrise Mobility"}},
  "resources": [
    {"id": "res-chg-001", "descriptor": {"name": "Charger MG Road"}},
    {"id": "res-chg-002", "descriptor": {"name": "Charger Indiranagar"}}
  ],
  "offers": []
}`)
}

// e2ePublisher serves the catalog index and its baseline over HTTP. The index
// is built after the server starts, because the signed tuple covers the file's
// absolute URL and so cannot be known until the listener has a port.
type e2ePublisher struct {
	srv       *httptest.Server
	indexURL  string
	baseline  []byte
	indexBody []byte
}

func newE2EPublisher(t *testing.T, catalogID string, signWith ed25519.PrivateKey) *e2ePublisher {
	t.Helper()
	p := &e2ePublisher{baseline: baselineCatalog(catalogID)}
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog-index.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(p.indexBody)
	})
	mux.HandleFunc("/catalogs/baseline.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(p.baseline)
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)

	baseURL := p.srv.URL + "/catalogs/baseline.json"
	validUntil := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	dg := digestOf(p.baseline)

	index := map[string]any{
		"participantId": e2eSubscriberID,
		"version":       1,
		"catalogs": []any{map[string]any{
			"catalogId":   catalogID,
			"catalogType": "regular",
			"status":      "ACTIVE",
			"networkIds":  []string{},
			"baseline": map[string]any{
				"version":  1,
				"url":      baseURL,
				"size":     len(p.baseline),
				"digest":   dg,
				"encoding": "json",
				"signature": map[string]any{
					"keyId":      e2eKeyID,
					"value":      signTuple(t, signWith, catalogID, 1, baseURL, dg, validUntil),
					"validUntil": validUntil.Format(time.RFC3339),
				},
			},
			"changes": []any{},
		}},
	}
	body, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	p.indexBody = body
	p.indexURL = p.srv.URL + "/catalog-index.json"
	return p
}

// e2eDiscovery captures /catalog/push bodies.
type e2eDiscovery struct {
	srv    *httptest.Server
	bodies chan []byte
}

func newE2EDiscovery(t *testing.T) *e2eDiscovery {
	t.Helper()
	d := &e2eDiscovery{bodies: make(chan []byte, 16)}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		select {
		case d.bodies <- buf:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":{"ack":{"status":"ACK"}}}`))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

// e2eQueryRegistry is the DeDi /query index-discovery registry: it answers
// GET /query/{networkId} with one live provider record whose catalog_index_url
// points at the publisher's index. This is the source under test — the crawler
// DISCOVERS the index URL here (the same path the scheduled pass uses) rather
// than being handed it.
type e2eQueryRegistry struct {
	srv *httptest.Server
}

func newE2EQueryRegistry(t *testing.T, networkID, subscriberID, indexURL string) *e2eQueryRegistry {
	t.Helper()
	body := `{"message":"ok","data":{"registry_name":"testnet","total_records":1,"records":[` +
		`{"details":{"subscriber_id":"` + subscriberID + `","type":"BPP"},` +
		`"meta":{"catalog_index_url":"` + indexURL + `"},"state":"live"}]}}`
	q := &e2eQueryRegistry{}
	q.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/query/"+networkID {
			t.Errorf("unexpected /query path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(q.srv.Close)
	return q
}

func e2eSettings(t *testing.T, indexURL, pushURL string) config.Settings {
	t.Helper()
	dsn := os.Getenv("CRAWLER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("set CRAWLER_TEST_DB_DSN to run the end-to-end test")
	}
	return config.Settings{
		Enabled:              true,
		StoreProvider:        "postgres",
		DBDSN:                dsn,
		PushEndpoint:         pushURL,
		IndexURLs:            []string{indexURL},
		IndexInterval:        time.Hour,
		CatalogInterval:      time.Hour,
		FetchTimeout:         10 * time.Second,
		MaxArtifactBytes:     10 << 20,
		MaxDecompressedBytes: 32 << 20,
		MaxAttempts:          3,
		MaxPushBytes:         10 << 20,
		MergeOnly:            true,
		BppURI:               "https://crawler.e2e.test",
	}
}

// TestE2E_SignedCatalogReachesDiscovery is the happy path: a correctly signed
// catalog, a registry that holds the publisher's key, and a push that lands.
func TestE2E_SignedCatalogReachesDiscovery(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	catalogID := uniqueID("cat-e2e-ok")
	publisher := newE2EPublisher(t, catalogID, priv)
	discovery := newE2EDiscovery(t)
	reg := &e2eRegistry{pub: pub}
	queryReg := newE2EQueryRegistry(t, e2eNetworkID, e2eSubscriberID, publisher.indexURL)

	s := e2eSettings(t, "", discovery.srv.URL+"/catalog/push")
	// Drive discovery via the registry, not a static list; drain promptly — the
	// sync job only runs on its tick, and CrawlRegistry enqueues after Start's
	// immediate tick has already passed.
	s.IndexURLs = nil
	s.CatalogInterval = 250 * time.Millisecond

	ctx := context.Background()
	c, closer, err := crawler.New(ctx, s, crawler.Options{
		Registry:          reg,
		AllowPrivateFetch: true, // httptest listens on loopback; the SSRF guard is unit-tested separately
		NewID:             func() string { return "e2e-id" },
		Logger:            testLogger{t},
	})
	if err != nil {
		t.Fatalf("crawler.New: %v", err)
	}
	defer func() { _ = closer() }()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	// The crawler discovers publisher.indexURL from the /query registry, then
	// fetches + verifies + pushes it — the whole registry-driven chain.
	if _, err := c.CrawlRegistry(ctx, queryReg.srv.URL, []string{e2eNetworkID}); err != nil {
		t.Fatalf("CrawlRegistry: %v", err)
	}

	select {
	case body := <-discovery.bodies:
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("push body is not JSON: %v (%s)", err, string(body))
		}
		if len(body) == 0 {
			t.Fatal("push body was empty")
		}
		t.Logf("PUSH RECEIVED (%d bytes)", len(body))
		if reg.calls == 0 {
			t.Error("registry was never consulted, so the key did not come from it")
		}
		t.Logf("registry lookups: %d", reg.calls)
		// Let the sync settle (cursor advance + queue removal) before the deferred
		// Stop cancels the engine context. A push that lands but cannot settle is
		// safe (MERGE is idempotent, so the item just re-syncs) but it is not what
		// this test is asserting.
		time.Sleep(2 * time.Second)
	case <-time.After(45 * time.Second):
		t.Fatal("no push reached Discovery within 45s")
	}
}

// TestE2E_WrongKeySignatureNeverReachesDiscovery is the control. Everything is
// identical except the catalog is signed by a key the registry does not vouch
// for. Nothing may reach Discovery.
func TestE2E_WrongKeySignatureNeverReachesDiscovery(t *testing.T) {
	registeredPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	_, attackerPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	// Signed by the attacker, but the registry holds the real participant's key.
	publisher := newE2EPublisher(t, uniqueID("cat-e2e-bad"), attackerPriv)
	discovery := newE2EDiscovery(t)
	reg := &e2eRegistry{pub: registeredPub}
	queryReg := newE2EQueryRegistry(t, e2eNetworkID, e2eSubscriberID, publisher.indexURL)

	s := e2eSettings(t, "", discovery.srv.URL+"/catalog/push")
	s.IndexURLs = nil
	s.CatalogInterval = 250 * time.Millisecond

	ctx := context.Background()
	c, closer, err := crawler.New(ctx, s, crawler.Options{
		Registry:          reg,
		AllowPrivateFetch: true,
		NewID:             func() string { return "e2e-bad" },
		Logger:            testLogger{t},
	})
	if err != nil {
		t.Fatalf("crawler.New: %v", err)
	}
	defer func() { _ = closer() }()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	if _, err := c.CrawlRegistry(ctx, queryReg.srv.URL, []string{e2eNetworkID}); err != nil {
		t.Fatalf("CrawlRegistry: %v", err)
	}

	select {
	case body := <-discovery.bodies:
		t.Fatalf("SECURITY FAILURE: content signed by an unregistered key reached Discovery: %s", string(body))
	case <-time.After(20 * time.Second):
		t.Log("nothing reached Discovery, as required")
	}
}

// testLogger surfaces the crawler's own structured events in test output, so a
// failure says which stage stopped rather than only that nothing arrived.
type testLogger struct{ t *testing.T }

func (l testLogger) log(level, event string, kv ...any) {
	l.t.Logf("[%s] %s %v", level, event, kv)
}
func (l testLogger) Debug(event string, kv ...any) { l.log("DEBUG", event, kv...) }
func (l testLogger) Info(event string, kv ...any)  { l.log("INFO", event, kv...) }
func (l testLogger) Warn(event string, kv ...any)  { l.log("WARN", event, kv...) }
func (l testLogger) Error(event string, kv ...any) { l.log("ERROR", event, kv...) }

var _ = fmt.Sprintf
