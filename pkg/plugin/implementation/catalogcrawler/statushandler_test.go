package catalogcrawler

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/signvalidator"
	"golang.org/x/crypto/blake2b"
)

// --- signing helpers, mirroring signvalidator's own test helpers (that
// package's hash()/hashAck() are unexported, so this is a small,
// intentional duplicate rather than an import) ---

func testHash(body []byte, created, expires int64) string {
	h, _ := blake2b.New512(nil)
	h.Write(body)
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s", created, expires, digest)
}

func testSignedHeader(subscriberID string, priv ed25519.PrivateKey, body []byte, created, expires int64) string {
	sig := ed25519.Sign(priv, []byte(testHash(body, created, expires)))
	return fmt.Sprintf(`Signature keyId="%s|key-1|ed25519",algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		subscriberID, created, expires, base64.StdEncoding.EncodeToString(sig))
}

// --- fakes ---

type fakeStatusRegistry struct{}

func (fakeStatusRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	panic("unused")
}

type fakeStatusKeyManager struct{ pub ed25519.PublicKey }

func (f fakeStatusKeyManager) GenerateKeyset() (*model.Keyset, error) { panic("unused") }
func (f fakeStatusKeyManager) InsertKeyset(context.Context, string, *model.Keyset) error {
	panic("unused")
}
func (f fakeStatusKeyManager) Keyset(context.Context, string) (*model.Keyset, error) {
	panic("unused")
}
func (f fakeStatusKeyManager) LookupNPKeys(_ context.Context, subscriberID, uniqueKeyID string) (string, string, error) {
	return base64.StdEncoding.EncodeToString(f.pub), "", nil
}
func (f fakeStatusKeyManager) DeleteKeyset(context.Context, string) error { panic("unused") }

// statusTestManager implements handler.PluginManager, backing only what
// NewStatusHandler actually calls (Cache/Registry/KeyManager/SignValidator/
// Crawler); everything else panics if reached.
type statusTestManager struct {
	km      definition.KeyManager
	sv      definition.SignValidator
	crawler definition.Crawler
}

func (m *statusTestManager) Cache(context.Context, *plugin.Config) (definition.Cache, error) {
	return nil, nil
}
func (m *statusTestManager) Registry(context.Context, definition.Cache, *plugin.Config) (definition.RegistryLookup, error) {
	return fakeStatusRegistry{}, nil
}
func (m *statusTestManager) KeyManager(context.Context, definition.RegistryLookup, *plugin.Config) (definition.KeyManager, error) {
	return m.km, nil
}
func (m *statusTestManager) SignValidator(context.Context, *plugin.Config) (definition.SignValidator, error) {
	return m.sv, nil
}
func (m *statusTestManager) Crawler(context.Context, definition.RegistryLookup, *plugin.Config) (definition.Crawler, error) {
	return m.crawler, nil
}
func (m *statusTestManager) Middleware(context.Context, *plugin.Config) (func(http.Handler) http.Handler, error) {
	panic("unused")
}
func (m *statusTestManager) Validator(context.Context, *plugin.Config) (definition.SchemaValidator, error) {
	panic("unused")
}
func (m *statusTestManager) Router(context.Context, *plugin.Config) (definition.Router, error) {
	panic("unused")
}
func (m *statusTestManager) Publisher(context.Context, *plugin.Config) (definition.Publisher, error) {
	panic("unused")
}
func (m *statusTestManager) Signer(context.Context, *plugin.Config) (definition.Signer, error) {
	panic("unused")
}
func (m *statusTestManager) Step(context.Context, *plugin.Config) (definition.Step, error) {
	panic("unused")
}
func (m *statusTestManager) PolicyChecker(context.Context, definition.ManifestLoader, *plugin.Config) (definition.PolicyChecker, error) {
	panic("unused")
}
func (m *statusTestManager) SchemaVersionMediator(context.Context, definition.ManifestLoader, *plugin.Config) (definition.SchemaVersionMediator, error) {
	panic("unused")
}
func (m *statusTestManager) ManifestLoader(context.Context, definition.Cache, definition.RegistryMetadataLookup, *plugin.Config) (definition.ManifestLoader, error) {
	panic("unused")
}
func (m *statusTestManager) TransportWrapper(context.Context, *plugin.Config) (definition.TransportWrapper, error) {
	panic("unused")
}
func (m *statusTestManager) SchemaValidator(context.Context, *plugin.Config) (definition.SchemaValidator, error) {
	panic("unused")
}
func (m *statusTestManager) PayloadStore(context.Context, definition.Cache, string, *plugin.Config) (definition.PayloadStore, error) {
	panic("unused")
}
func (m *statusTestManager) CatalogPublisher(context.Context, definition.KeyManager, definition.CatalogBlobStore, definition.RegistryMetadataLookup, *plugin.Config) (definition.CatalogPublisher, error) {
	panic("unused")
}
func (m *statusTestManager) CatalogBlobStore(context.Context, *plugin.Config) (definition.CatalogBlobStore, error) {
	panic("unused")
}

func newStatusTestManager(t *testing.T, crawler definition.Crawler) (*statusTestManager, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sv, _, err := signvalidator.New(context.Background(), &signvalidator.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return &statusTestManager{km: fakeStatusKeyManager{pub: pub}, sv: sv, crawler: crawler}, priv
}

func testStatusConfig() *handler.Config {
	return &handler.Config{
		Plugins: handler.PluginCfg{
			Registry:      &plugin.Config{ID: "dediregistry"},
			KeyManager:    &plugin.Config{ID: "simplekeymanager"},
			SignValidator: &plugin.Config{ID: "signvalidator"},
			Crawler:       &plugin.Config{ID: "catalogcrawler"},
		},
	}
}

func TestNewStatusHandler_MissingCrawlerConfigErrors(t *testing.T) {
	mgr, _ := newStatusTestManager(t, &fakeCrawler{})
	cfg := testStatusConfig()
	cfg.Plugins.Crawler = nil
	if _, err := NewStatusHandler(context.Background(), mgr, cfg, "crawlStatus"); err == nil {
		t.Fatal("expected an error when plugins.crawler is not configured")
	}
}

func TestStatusHandler_RejectsMissingAuthHeader(t *testing.T) {
	mgr, _ := newStatusTestManager(t, &fakeCrawler{})
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/crawl/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestStatusHandler_RejectsBadSignature(t *testing.T) {
	fc := &fakeCrawler{}
	mgr, priv := newStatusTestManager(t, fc)
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	authHeader := testSignedHeader("publisher.example.com", priv, nil, now, now+300)
	// Tamper with the signature so verification fails.
	authHeader = authHeader[:len(authHeader)-5] + `AAAA"`

	req := httptest.NewRequest(http.MethodGet, "/crawl/status", nil)
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestStatusHandler_ReturnsCatalogsScopedToVerifiedSubscriber(t *testing.T) {
	fc := &fakeCrawler{statusRows: []definition.CrawlStatus{{CatalogID: "publisher.example.com/CAT-1", Version: 3, EntryVersion: 3}}}
	mgr, priv := newStatusTestManager(t, fc)
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	authHeader := testSignedHeader("publisher.example.com", priv, nil, now, now+300)

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?catalogId=publisher.example.com/CAT-1", nil)
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fc.gotStatusSubscriber != "publisher.example.com" {
		t.Errorf("Status called with subscriberID=%q, want it derived from the verified signature", fc.gotStatusSubscriber)
	}
	if fc.gotStatusCatalog != "publisher.example.com/CAT-1" {
		t.Errorf("Status called with catalogID=%q, want the query param", fc.gotStatusCatalog)
	}
	var got []definition.CrawlStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CatalogID != "publisher.example.com/CAT-1" {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}
}

func TestStatusHandler_UnknownCatalogIdReturns404(t *testing.T) {
	fc := &fakeCrawler{statusRows: nil}
	mgr, priv := newStatusTestManager(t, fc)
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	authHeader := testSignedHeader("publisher.example.com", priv, nil, now, now+300)

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?catalogId=someone-elses.example.com/CAT-1", nil)
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestStatusHandler_EmptyListWithNoCatalogIdIsOK(t *testing.T) {
	fc := &fakeCrawler{statusRows: nil}
	mgr, priv := newStatusTestManager(t, fc)
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	authHeader := testSignedHeader("publisher.example.com", priv, nil, now, now+300)

	req := httptest.NewRequest(http.MethodGet, "/crawl/status", nil)
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}
