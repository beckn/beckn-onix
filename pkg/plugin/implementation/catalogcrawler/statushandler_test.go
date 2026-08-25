package catalogcrawler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// statusTestManager implements handler.PluginManager, backing only what
// NewStatusHandler actually calls (Cache/Registry/Crawler) while
// AuthDisabled is the only supported mode; everything else panics if
// reached.
type statusTestManager struct {
	crawler definition.Crawler
}

func (m *statusTestManager) Cache(context.Context, *plugin.Config) (definition.Cache, error) {
	return nil, nil
}
func (m *statusTestManager) Registry(context.Context, definition.Cache, *plugin.Config) (definition.RegistryLookup, error) {
	return fakeStatusRegistry{}, nil
}
func (m *statusTestManager) Crawler(context.Context, definition.RegistryLookup, *plugin.Config) (definition.Crawler, error) {
	return m.crawler, nil
}
func (m *statusTestManager) Middleware(context.Context, *plugin.Config) (func(http.Handler) http.Handler, error) {
	panic("unused")
}
func (m *statusTestManager) SignValidator(context.Context, *plugin.Config) (definition.SignValidator, error) {
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
func (m *statusTestManager) KeyManager(context.Context, definition.RegistryLookup, *plugin.Config) (definition.KeyManager, error) {
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

type fakeStatusRegistry struct{}

func (fakeStatusRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	panic("unused")
}

func testStatusConfig(authDisabled bool) *handler.Config {
	return &handler.Config{
		AuthDisabled: authDisabled,
		Plugins: handler.PluginCfg{
			Registry: &plugin.Config{ID: "dediregistry"},
			Crawler:  &plugin.Config{ID: "catalogcrawler"},
		},
	}
}

func TestNewStatusHandler_AuthEnabledIsNotImplementedYet(t *testing.T) {
	mgr := &statusTestManager{crawler: &fakeCrawler{}}
	if _, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(false), "crawlStatus"); err == nil {
		t.Fatal("expected an error when authDisabled is not set -- signed auth isn't implemented yet")
	}
}

func TestNewStatusHandler_MissingCrawlerConfigErrors(t *testing.T) {
	mgr := &statusTestManager{crawler: &fakeCrawler{}}
	cfg := testStatusConfig(true)
	cfg.Plugins.Crawler = nil
	if _, err := NewStatusHandler(context.Background(), mgr, cfg, "crawlStatus"); err == nil {
		t.Fatal("expected an error when plugins.crawler is not configured")
	}
}

func TestStatusHandler_RequiresSubscriberIdParam(t *testing.T) {
	mgr := &statusTestManager{crawler: &fakeCrawler{}}
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(true), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/crawl/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestStatusHandler_ReturnsCatalogsForSubscriberIdParam(t *testing.T) {
	fc := &fakeCrawler{statusRows: []definition.CrawlStatus{{CatalogID: "publisher.example.com/CAT-1", Version: 3, EntryVersion: 3}}}
	mgr := &statusTestManager{crawler: fc}
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(true), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?subscriberId=publisher.example.com&catalogId=publisher.example.com/CAT-1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fc.gotStatusSubscriber != "publisher.example.com" {
		t.Errorf("Status called with subscriberID=%q, want the query param", fc.gotStatusSubscriber)
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

// TestStatusHandler_QueuedButNeverSyncedCatalogIsVisible guards against a
// real bug found in the underlying store query: a catalog queued for its
// very first sync has no crawler_catalog row yet (that table is only
// written on settle), so a naive query starting FROM crawler_catalog
// silently omits it entirely instead of reporting queued=true. This test
// exercises the handler/JSON boundary for that shape; internal/store's own
// query fix was verified against a real Postgres instance separately.
func TestStatusHandler_QueuedButNeverSyncedCatalogIsVisible(t *testing.T) {
	fc := &fakeCrawler{statusRows: []definition.CrawlStatus{
		{CatalogID: "publisher.example.com/CAT-NEW", EverSynced: false, Queued: true},
	}}
	mgr := &statusTestManager{crawler: fc}
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(true), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?subscriberId=publisher.example.com&catalogId=publisher.example.com/CAT-NEW", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []definition.CrawlStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EverSynced || !got[0].Queued {
		t.Fatalf("expected 1 catalog with everSynced=false, queued=true, got: %s", rec.Body.String())
	}
}

// TestStatusHandler_SurfacesParkedAndAbandonedState exercises the
// handler/JSON boundary for the revive-or-abandon fields (catalog-core's
// Store.RequeueOrAbandonParked/ListAbandoned) -- the underlying store
// query was verified against a real Postgres instance separately.
func TestStatusHandler_SurfacesParkedAndAbandonedState(t *testing.T) {
	fc := &fakeCrawler{statusRows: []definition.CrawlStatus{
		{CatalogID: "publisher.example.com/CAT-PARKED", EverSynced: true, Parked: true, ParkCount: 1},
		{CatalogID: "publisher.example.com/CAT-ABANDONED", EverSynced: true, Abandoned: true, ParkCount: 48, AbandonedAt: time.Unix(1700000000, 0)},
	}}
	mgr := &statusTestManager{crawler: fc}
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(true), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?subscriberId=publisher.example.com", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []definition.CrawlStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 catalogs, got: %s", rec.Body.String())
	}
	byID := map[string]definition.CrawlStatus{}
	for _, c := range got {
		byID[c.CatalogID] = c
	}
	parked := byID["publisher.example.com/CAT-PARKED"]
	if !parked.Parked || parked.Abandoned || parked.ParkCount != 1 {
		t.Fatalf("unexpected parked catalog: %+v", parked)
	}
	abandoned := byID["publisher.example.com/CAT-ABANDONED"]
	if !abandoned.Abandoned || abandoned.Parked || abandoned.ParkCount != 48 || abandoned.AbandonedAt.IsZero() {
		t.Fatalf("unexpected abandoned catalog: %+v", abandoned)
	}
}

func TestStatusHandler_UnknownCatalogIdReturns404(t *testing.T) {
	fc := &fakeCrawler{statusRows: nil}
	mgr := &statusTestManager{crawler: fc}
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(true), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?subscriberId=publisher.example.com&catalogId=someone-elses.example.com/CAT-1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestStatusHandler_EmptyListWithNoCatalogIdIsOK(t *testing.T) {
	fc := &fakeCrawler{statusRows: nil}
	mgr := &statusTestManager{crawler: fc}
	h, err := NewStatusHandler(context.Background(), mgr, testStatusConfig(true), "crawlStatus")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?subscriberId=publisher.example.com", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}
