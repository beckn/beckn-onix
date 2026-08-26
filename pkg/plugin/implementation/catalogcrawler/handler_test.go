package catalogcrawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// fakeCrawler is shared by trigger_test.go, status_test.go, and this
// file's own dispatch tests.
type fakeCrawler struct {
	runID      string
	err        error
	gotNetwork []string

	// statusRows/statusErr back Status; gotStatusSubscriber/gotStatusCatalog
	// record its last call's arguments for assertions.
	statusRows          []definition.CrawlStatus
	statusErr           error
	gotStatusSubscriber string
	gotStatusCatalog    string
}

func (f *fakeCrawler) Start(ctx context.Context) error { return nil }
func (f *fakeCrawler) Stop() error                     { return nil }
func (f *fakeCrawler) CrawlRegistry(ctx context.Context, networkIDs []string) (string, error) {
	f.gotNetwork = networkIDs
	if f.err != nil {
		return "", f.err
	}
	return f.runID, nil
}
func (f *fakeCrawler) Status(ctx context.Context, subscriberID, catalogID string) ([]definition.CrawlStatus, error) {
	f.gotStatusSubscriber, f.gotStatusCatalog = subscriberID, catalogID
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	return f.statusRows, nil
}

func TestNewHandler_NoCrawlerErrors(t *testing.T) {
	if _, err := NewHandler(context.Background(), nil, &handler.Config{}, "crawl"); err == nil {
		t.Fatal("expected an error when no Crawler is configured")
	}
}

// TestNewHandler_DispatchesOnSubPath exercises NewHandler's own dispatch
// logic (path stripped of BasePath -> sub-endpoint) -- the sub-endpoints'
// own request/response behavior is covered in trigger_test.go/status_test.go
// against newTriggerHandler/newStatusHandler directly.
func TestNewHandler_DispatchesOnSubPath(t *testing.T) {
	fc := &fakeCrawler{runID: "run-123", statusRows: []definition.CrawlStatus{{CatalogID: "pub.example.com/CAT-1"}}}
	cfg := &handler.Config{BasePath: "/crawl/", AuthDisabled: true}
	h, err := NewHandler(context.Background(), fc, cfg, "crawl")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/crawl/trigger", nil))
	if rec.Code != http.StatusBadRequest { // no networkId(s) in this bare request
		t.Fatalf("trigger: status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/crawl/status?subscriberId=pub.example.com", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/crawl/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown sub-path: status = %d, want 404", rec.Code)
	}
}
