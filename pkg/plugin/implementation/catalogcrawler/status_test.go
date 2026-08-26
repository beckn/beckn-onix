package catalogcrawler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

func TestStatusHandler_RejectsWhenAuthNotDisabled(t *testing.T) {
	fc := &fakeCrawler{}
	h := newStatusHandler(fc, &handler.Config{AuthDisabled: false})

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?subscriberId=publisher.example.com", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestStatusHandler_RequiresSubscriberIdParam(t *testing.T) {
	fc := &fakeCrawler{}
	h := newStatusHandler(fc, &handler.Config{AuthDisabled: true})

	req := httptest.NewRequest(http.MethodGet, "/crawl/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestStatusHandler_ReturnsCatalogsForSubscriberIdParam(t *testing.T) {
	fc := &fakeCrawler{statusRows: []definition.CrawlStatus{{CatalogID: "publisher.example.com/CAT-1", EntryVersion: 3}}}
	h := newStatusHandler(fc, &handler.Config{AuthDisabled: true})

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
	h := newStatusHandler(fc, &handler.Config{AuthDisabled: true})

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
	h := newStatusHandler(fc, &handler.Config{AuthDisabled: true})

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
	h := newStatusHandler(fc, &handler.Config{AuthDisabled: true})

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?subscriberId=publisher.example.com&catalogId=someone-elses.example.com/CAT-1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestStatusHandler_EmptyListWithNoCatalogIdIsOK(t *testing.T) {
	fc := &fakeCrawler{statusRows: nil}
	h := newStatusHandler(fc, &handler.Config{AuthDisabled: true})

	req := httptest.NewRequest(http.MethodGet, "/crawl/status?subscriberId=publisher.example.com", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}
