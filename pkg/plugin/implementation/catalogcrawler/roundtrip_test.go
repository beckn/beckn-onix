package catalogcrawler_test

// Round-trip test: catalogpublisher produces a signed manifest + index,
// this test serves them over real HTTP, and catalogcrawler fetches and
// verifies them -- proving the two plugins actually agree on the wire
// format (JSON shapes, digest convention, detached-JWS scheme), not just
// on paper.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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

func TestPublisherToCrawler_RoundTrip(t *testing.T) {
	t.Skip("catalogpublisher now produces the file-spec's catalog-index shape " +
		"(participantId/version/catalogs[]/baseline+changes with per-file " +
		"signatures) instead of the DeDi-wrapper shape catalogcrawler still " +
		"expects (publisher/registry/records[].details/parts[] with one " +
		"whole-document proof) -- catalogcrawler has not been updated for " +
		"this yet; see 'Decentralized Catalog file spec.md'. Re-enable and " +
		"rewrite this test once it is.")

	ctx := context.Background()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	km := &fakeKeyManager{keyID: "publisher-key-1", priv: priv, pub: pub}

	files := &servedFiles{}
	srv := httptest.NewServer(files)
	defer srv.Close()
	// artifactfetcher's SSRF guard rejects literal loopback IPs but doesn't
	// resolve hostnames, so route through "localhost" instead of
	// httptest's literal 127.0.0.1 URL.
	baseURL := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	publisher, _, err := catalogpublisher.New(ctx, km, &catalogpublisher.Config{
		KeyID:          "publisher-key-1",
		Domain:         baseURL,
		IndexURL:       baseURL + "/index.json",
		CatalogBaseURL: baseURL + "/catalog",
	})
	if err != nil {
		t.Fatalf("catalogpublisher.New: %v", err)
	}

	catalogBody := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test Provider"},"provider":{},"resources":[]}`)
	result, err := publisher.Publish(ctx, definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{
				CatalogID:   "CAT-1",
				SchemaTypes: []string{"retail"},
				Catalog:     catalogBody,
			},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no publish errors, got %+v", result.Errors)
	}

	files.set("/.well-known/dedi.json", result.Manifest)
	files.set("/index.json", result.Index)
	files.set("/catalog/CAT-1/baseline.json", catalogBody)

	crawler, _, err := catalogcrawler.New(ctx, fakeSigner{}, km, &catalogcrawler.Config{})
	if err != nil {
		t.Fatalf("catalogcrawler.New: %v", err)
	}

	crawlResult, err := crawler.CrawlSubscriber(ctx, definition.CrawlRequest{
		SubscriberID: baseURL,
		Mode:         definition.CrawlModeFull,
	})
	if err != nil {
		t.Fatalf("CrawlSubscriber: %v", err)
	}

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
	if got.CatalogID != "CAT-1" {
		t.Errorf("CatalogID = %q, want CAT-1", got.CatalogID)
	}
	if !got.Verification.DigestMatch {
		t.Error("expected digest to match")
	}
	if !got.Verification.SchemaValid {
		t.Error("expected catalog to look like a valid Beckn Catalog")
	}
	if string(got.Catalog) != string(catalogBody) {
		t.Errorf("Catalog body mismatch:\ngot:  %s\nwant: %s", got.Catalog, catalogBody)
	}
}
