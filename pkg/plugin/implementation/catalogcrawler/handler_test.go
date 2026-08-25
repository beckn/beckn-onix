package catalogcrawler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/core/module/handler"
)

type fakeCrawler struct {
	runID      string
	err        error
	gotNetwork []string
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

func TestNewHandler_NoCrawlerErrors(t *testing.T) {
	if _, err := NewHandler(context.Background(), nil, &handler.Config{}, "crawl"); err == nil {
		t.Fatal("expected an error when no Crawler is configured")
	}
}

func TestHandler_TriggersCrawlAndReturnsRunID(t *testing.T) {
	fc := &fakeCrawler{runID: "run-123"}
	h, err := NewHandler(context.Background(), fc, &handler.Config{}, "crawl")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/crawl", strings.NewReader(`{"networkIds":["net.a","net.b"]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var resp crawlResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RunID != "run-123" {
		t.Fatalf("runId = %q, want %q", resp.RunID, "run-123")
	}
	if got := fc.gotNetwork; len(got) != 2 || got[0] != "net.a" || got[1] != "net.b" {
		t.Fatalf("CrawlRegistry called with %v", got)
	}
}

func TestHandler_MergesSingularNetworkID(t *testing.T) {
	fc := &fakeCrawler{runID: "run-1"}
	h, err := NewHandler(context.Background(), fc, &handler.Config{}, "crawl")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/crawl", strings.NewReader(`{"networkId":"net.a","networkIds":["net.b"]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := fc.gotNetwork; len(got) != 2 || got[0] != "net.b" || got[1] != "net.a" {
		t.Fatalf("CrawlRegistry called with %v", got)
	}
}

func TestHandler_EmptyRequestIsRejected(t *testing.T) {
	fc := &fakeCrawler{runID: "run-1"}
	h, err := NewHandler(context.Background(), fc, &handler.Config{}, "crawl")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/crawl", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandler_CrawlRegistryErrorSurfaces(t *testing.T) {
	fc := &fakeCrawler{err: context.DeadlineExceeded}
	h, err := NewHandler(context.Background(), fc, &handler.Config{}, "crawl")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/crawl", strings.NewReader(`{"networkIds":["net.a"]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}
