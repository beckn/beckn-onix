package catalogcrawler_test

// Round-trip tests: catalogpublisher produces a signed manifest + catalog
// index (with per-file signed entries), this test serves them over real
// HTTP, and catalogcrawler fetches, verifies, and composes them -- proving
// the two plugins actually agree on the wire format (JSON shapes, digest
// convention, per-file signature tuple scheme), not just on paper.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogcrawler"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
)

// servedFiles is a tiny in-memory static file server the test fills in
// after Publish, keyed by URL path.
type servedFiles struct {
	mu    sync.RWMutex
	files map[string][]byte
}

func (s *servedFiles) set(path string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = map[string][]byte{}
	}
	s.files[path] = body
}

func (s *servedFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	body, ok := s.files[r.URL.Path]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// fakeKeyManager returns a fixed keyset for one keyID -- catalogpublisher
// uses it to sign; catalogcrawler never calls it in this phase (signed
// GETs are disabled, see its README), it's only constructor-required.
type fakeKeyManager struct {
	keyID string
	priv  ed25519.PrivateKey
	pub   ed25519.PublicKey
}

func (f *fakeKeyManager) GenerateKeyset() (*model.Keyset, error) { return nil, nil }
func (f *fakeKeyManager) InsertKeyset(ctx context.Context, keyID string, keyset *model.Keyset) error {
	return nil
}
func (f *fakeKeyManager) Keyset(ctx context.Context, keyID string) (*model.Keyset, error) {
	return &model.Keyset{
		SigningPrivate: base64.StdEncoding.EncodeToString(f.priv.Seed()),
		SigningPublic:  base64.StdEncoding.EncodeToString(f.pub),
	}, nil
}
func (f *fakeKeyManager) LookupNPKeys(ctx context.Context, subscriberID, uniqueKeyID string) (string, string, error) {
	return "", "", nil
}
func (f *fakeKeyManager) DeleteKeyset(ctx context.Context, keyID string) error { return nil }

// fakeSigner is a no-op: catalogcrawler.New requires a non-nil Signer, but
// this phase never invokes it (signed index/catalog GETs are disabled).
type fakeSigner struct{}

func (fakeSigner) Sign(ctx context.Context, body []byte, privateKeyBase64 string, createdAt, expiresAt int64) (string, error) {
	return "", nil
}
func (fakeSigner) SignAck(ctx context.Context, ackBody []byte, requestSignature, privateKeyBase64 string, createdAt, expiresAt int64) (string, error) {
	return "", nil
}

// testHarness wires one publisher + crawler pair against a shared
// in-memory HTTP server, matching the real manifest -> catalog-index ->
// catalog-file chain end to end.
type testHarness struct {
	t         *testing.T
	files     *servedFiles
	baseURL   string
	km        *fakeKeyManager
	publisher *catalogpublisher.Publisher
	crawler   *catalogcrawler.Crawler
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	ctx := context.Background()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	km := &fakeKeyManager{keyID: "publisher-key-1", priv: priv, pub: pub}

	files := &servedFiles{}
	srv := httptest.NewServer(files)
	t.Cleanup(srv.Close)
	// artifactfetcher's SSRF guard rejects literal loopback IPs but doesn't
	// resolve hostnames, so route through "localhost" instead of
	// httptest's literal 127.0.0.1 URL.
	baseURL := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	publisher, _, err := catalogpublisher.New(ctx, km, &catalogpublisher.Config{
		KeyID:          "publisher-key-1",
		Domain:         baseURL,
		IndexURL:       baseURL + "/dedi/becknCatalogs.index.json",
		CatalogBaseURL: baseURL + "/catalogs",
		FileValidityIn: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("catalogpublisher.New: %v", err)
	}

	crawler, _, err := catalogcrawler.New(ctx, fakeSigner{}, km, &catalogcrawler.Config{})
	if err != nil {
		t.Fatalf("catalogcrawler.New: %v", err)
	}

	return &testHarness{t: t, files: files, baseURL: baseURL, km: km, publisher: publisher, crawler: crawler}
}

// publish calls Publish, serves the resulting manifest/index/catalog-file
// bytes at the paths they declare, and returns the result.
func (h *testHarness) publish(req definition.PublishRequest) definition.PublishResult {
	h.t.Helper()
	result, err := h.publisher.Publish(context.Background(), req)
	if err != nil {
		h.t.Fatalf("Publish: %v", err)
	}
	if len(result.Errors) != 0 {
		h.t.Fatalf("expected no publish errors, got %+v", result.Errors)
	}

	h.files.set("/.well-known/dedi.index.json", result.Manifest)
	h.files.set("/dedi/becknCatalogs.index.json", result.Index)
	for _, outcome := range result.Catalogs {
		if outcome.Content == nil {
			continue
		}
		local := localName(outcome.CatalogID)
		suffix := "json"
		if outcome.Mode == "change" {
			suffix = "changes.json"
		}
		h.files.set(fmt.Sprintf("/catalogs/%s.v%d.%s", local, outcome.Version, suffix), outcome.Content)
	}
	return result
}

func (h *testHarness) crawl() definition.CrawlResult {
	h.t.Helper()
	result, err := h.crawler.CrawlSubscriber(context.Background(), definition.CrawlRequest{
		SubscriberID: h.baseURL,
		Mode:         definition.CrawlModeFull,
	})
	if err != nil {
		h.t.Fatalf("CrawlSubscriber: %v", err)
	}
	return result
}

func localName(catalogID string) string {
	if i := strings.LastIndex(catalogID, "/"); i != -1 {
		return catalogID[i+1:]
	}
	return catalogID
}

// extractBaselineRef parses a published index (definition.PublishResult.Index)
// to find catalogID's real baseline file reference -- URL, digest, and
// signature, exactly as catalogpublisher wrote them. Used to build a
// PriorCatalogState for a follow-up Publish call without corrupting the
// carried-forward baseline reference with synthetic values.
func extractBaselineRef(index json.RawMessage, catalogID string) (*definition.FileRef, error) {
	var doc struct {
		Catalogs []json.RawMessage `json:"catalogs"`
	}
	if err := json.Unmarshal(index, &doc); err != nil {
		return nil, err
	}
	for _, raw := range doc.Catalogs {
		var entry struct {
			CatalogID string `json:"catalogId"`
			Baseline  struct {
				Version   int    `json:"version"`
				URL       string `json:"url"`
				Size      int64  `json:"size"`
				Digest    string `json:"digest"`
				Signature struct {
					KeyID      string    `json:"keyId"`
					Value      string    `json:"value"`
					ValidUntil time.Time `json:"validUntil"`
				} `json:"signature"`
			} `json:"baseline"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.CatalogID != catalogID {
			continue
		}
		return &definition.FileRef{
			Version:             entry.Baseline.Version,
			URL:                 entry.Baseline.URL,
			Size:                entry.Baseline.Size,
			Digest:              entry.Baseline.Digest,
			SignatureKeyID:      entry.Baseline.Signature.KeyID,
			SignatureValue:      entry.Baseline.Signature.Value,
			SignatureValidUntil: entry.Baseline.Signature.ValidUntil,
		}, nil
	}
	return nil, fmt.Errorf("catalog %q not found in index", catalogID)
}

func TestPublisherToCrawler_RoundTrip_Baseline(t *testing.T) {
	h := newTestHarness(t)

	catalogBody := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test Provider"},"provider":{},"resources":[]}`)
	h.publish(definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{CatalogID: "example.test/CAT-1", SchemaTypes: []string{"retail"}, Catalog: catalogBody},
		},
	})

	crawlResult := h.crawl()
	if len(crawlResult.Errors) != 0 {
		t.Fatalf("expected no crawl errors, got %+v", crawlResult.Errors)
	}
	if !crawlResult.Manifest.Verified {
		t.Fatal("expected manifest signature to verify")
	}
	if len(crawlResult.Catalogs) != 1 {
		t.Fatalf("expected 1 catalog, got %d: %+v", len(crawlResult.Catalogs), crawlResult.Catalogs)
	}
	got := crawlResult.Catalogs[0]
	if got.CatalogID != "example.test/CAT-1" {
		t.Errorf("CatalogID = %q, want example.test/CAT-1", got.CatalogID)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if !got.Verification.DigestMatch {
		t.Error("expected digest to match")
	}
	if !got.Verification.SignatureValid {
		t.Error("expected the baseline's per-file signature to verify")
	}
	if !got.Verification.SchemaValid {
		t.Error("expected catalog to look like a valid Beckn Catalog")
	}
	if string(got.Catalog) != string(catalogBody) {
		t.Errorf("Catalog body mismatch:\ngot:  %s\nwant: %s", got.Catalog, catalogBody)
	}
}

func TestPublisherToCrawler_RoundTrip_IncrementalComposesChangeFile(t *testing.T) {
	h := newTestHarness(t)

	v1 := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}},{"id":"ITEM-2","descriptor":{"name":"two"}}]}`)
	firstResult := h.publish(definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: v1}},
	})
	baselineRef, err := extractBaselineRef(firstResult.Index, "example.test/CAT-1")
	if err != nil {
		t.Fatalf("extractBaselineRef: %v", err)
	}

	// ITEM-1 updated, ITEM-2 removed, ITEM-3 added.
	v2 := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one-updated"}},{"id":"ITEM-3","descriptor":{"name":"three"}}]}`)
	h.publish(definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: v2}},
		PriorState: map[string]definition.PriorCatalogState{
			"example.test/CAT-1": {Catalog: v1, BaselineFile: baselineRef},
		},
	})

	crawlResult := h.crawl()
	if len(crawlResult.Errors) != 0 {
		t.Fatalf("expected no crawl errors, got %+v", crawlResult.Errors)
	}
	if len(crawlResult.Catalogs) != 1 {
		t.Fatalf("expected 1 catalog, got %d: %+v", len(crawlResult.Catalogs), crawlResult.Catalogs)
	}
	got := crawlResult.Catalogs[0]
	if got.Version != 2 {
		t.Errorf("Version = %d, want 2", got.Version)
	}
	if !got.Verification.SignatureValid {
		t.Error("expected both the baseline's and the change file's signatures to verify")
	}

	var doc struct {
		Resources []json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(got.Catalog, &doc); err != nil {
		t.Fatalf("parsing composed catalog: %v", err)
	}
	if len(doc.Resources) != 2 {
		t.Fatalf("expected 2 resources in composed catalog (ITEM-1 updated + ITEM-3 added), got %d: %s", len(doc.Resources), got.Catalog)
	}
	ids := map[string]bool{}
	for _, r := range doc.Resources {
		var withID struct {
			ID string `json:"id"`
		}
		json.Unmarshal(r, &withID)
		ids[withID.ID] = true
	}
	if !ids["ITEM-1"] || !ids["ITEM-3"] || ids["ITEM-2"] {
		t.Errorf("unexpected composed resource ids: %+v", ids)
	}
}

func TestPublisherToCrawler_RoundTrip_RetiredCatalogIsTombstone(t *testing.T) {
	h := newTestHarness(t)

	h.publish(definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{CatalogID: "example.test/CAT-1", Catalog: json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}`)},
		},
	})
	h.publish(definition.PublishRequest{Retire: []string{"example.test/CAT-1"}})

	crawlResult := h.crawl()
	if len(crawlResult.Errors) != 0 {
		t.Fatalf("expected no crawl errors, got %+v", crawlResult.Errors)
	}
	if len(crawlResult.Catalogs) != 1 {
		t.Fatalf("expected 1 catalog, got %d: %+v", len(crawlResult.Catalogs), crawlResult.Catalogs)
	}
	got := crawlResult.Catalogs[0]
	if got.Status != "RETIRED" || got.RetiredAt == nil {
		t.Errorf("expected a RETIRED tombstone, got %+v", got)
	}
	if got.Catalog != nil {
		t.Errorf("expected no catalog content on a tombstone, got %s", got.Catalog)
	}
}

func TestPublisherToCrawler_RoundTrip_TamperedSignatureFailsVerification(t *testing.T) {
	h := newTestHarness(t)

	catalogBody := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}`)
	result := h.publish(definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: catalogBody}},
	})
	_ = result

	// Corrupt the served baseline file so its digest no longer matches
	// what the index declared.
	h.files.set("/catalogs/CAT-1.v1.json", json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"TAMPERED"},"provider":{},"resources":[]}`))

	crawlResult := h.crawl()
	if len(crawlResult.Catalogs) != 0 {
		t.Fatalf("expected the tampered catalog to be dropped, got %+v", crawlResult.Catalogs)
	}
	if len(crawlResult.Errors) != 1 {
		t.Fatalf("expected 1 non-fatal crawl error, got %+v", crawlResult.Errors)
	}
}
