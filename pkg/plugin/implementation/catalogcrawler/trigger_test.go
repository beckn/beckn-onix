package catalogcrawler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTriggerHandler_TriggersCrawlAndReturnsRunID(t *testing.T) {
	fc := &fakeCrawler{runID: "run-123"}
	h := newTriggerHandler(fc)

	req := httptest.NewRequest(http.MethodPost, "/crawl/trigger", strings.NewReader(`{"networkIds":["net.a","net.b"]}`))
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

func TestTriggerHandler_MergesSingularNetworkID(t *testing.T) {
	fc := &fakeCrawler{runID: "run-1"}
	h := newTriggerHandler(fc)

	req := httptest.NewRequest(http.MethodPost, "/crawl/trigger", strings.NewReader(`{"networkId":"net.a","networkIds":["net.b"]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := fc.gotNetwork; len(got) != 2 || got[0] != "net.b" || got[1] != "net.a" {
		t.Fatalf("CrawlRegistry called with %v", got)
	}
}

func TestTriggerHandler_EmptyRequestIsRejected(t *testing.T) {
	fc := &fakeCrawler{runID: "run-1"}
	h := newTriggerHandler(fc)

	req := httptest.NewRequest(http.MethodPost, "/crawl/trigger", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestTriggerHandler_CrawlRegistryErrorSurfaces(t *testing.T) {
	fc := &fakeCrawler{err: context.DeadlineExceeded}
	h := newTriggerHandler(fc)

	req := httptest.NewRequest(http.MethodPost, "/crawl/trigger", strings.NewReader(`{"networkIds":["net.a"]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}
