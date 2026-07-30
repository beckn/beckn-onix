package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// stubCrawler records the index URL the handler forwarded, so a test can tell
// "rejected before the fetch" apart from "accepted and dispatched".
type stubCrawler struct {
	calledWith string
	runID      string
	err        error
}

func (c *stubCrawler) Start(context.Context) error { return nil }
func (c *stubCrawler) Stop() error                 { return nil }
func (c *stubCrawler) CrawlNow(_ context.Context, indexURL string) (string, error) {
	c.calledWith = indexURL
	if c.err != nil {
		return "", c.err
	}
	return c.runID, nil
}

// TestCrawlHandlerServeHTTP covers the trigger's request validation: any
// absolute http(s) URL is queued, and malformed input and wrong methods are
// refused before the crawler is reached.
func TestCrawlHandlerServeHTTP(t *testing.T) {
	const someURL = "https://cdn.publisher.example.com/beckn/catalog-index.json"

	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
		wantCall   string
	}{
		{
			name:       "configured url is accepted",
			method:     http.MethodPost,
			body:       `{"indexUrl":"` + someURL + `"}`,
			wantStatus: http.StatusAccepted,
			wantCall:   someURL,
		},
		{
			name:       "surrounding space is trimmed",
			method:     http.MethodPost,
			body:       `{"indexUrl":"  ` + someURL + `  "}`,
			wantStatus: http.StatusAccepted,
			wantCall:   someURL,
		},
		{
			name:       "an index the crawler was not configured with is queued",
			method:     http.MethodPost,
			body:       `{"indexUrl":"https://brand-new.example.com/index.json"}`,
			wantStatus: http.StatusAccepted,
			wantCall:   "https://brand-new.example.com/index.json",
		},
		{
			name:       "plain http is accepted",
			method:     http.MethodPost,
			body:       `{"indexUrl":"http://publisher-origin/catalog-index.json"}`,
			wantStatus: http.StatusAccepted,
			wantCall:   "http://publisher-origin/catalog-index.json",
		},
		{
			name:       "file scheme is rejected",
			method:     http.MethodPost,
			body:       `{"indexUrl":"file:///etc/passwd"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non http scheme is rejected",
			method:     http.MethodPost,
			body:       `{"indexUrl":"gopher://publisher-origin:70/"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "relative url is rejected",
			method:     http.MethodPost,
			body:       `{"indexUrl":"/catalog-index.json"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty index url is rejected",
			method:     http.MethodPost,
			body:       `{"indexUrl":""}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "blank index url is rejected",
			method:     http.MethodPost,
			body:       `{"indexUrl":"   "}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed body is rejected",
			method:     http.MethodPost,
			body:       `{"indexUrl":`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "get is not allowed",
			method:     http.MethodGet,
			body:       "",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "put is not allowed",
			method:     http.MethodPut,
			body:       `{"indexUrl":"` + someURL + `"}`,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			crawler := &stubCrawler{runID: "run-1"}
			h := &crawlHandler{crawler: crawler}

			req := httptest.NewRequest(tt.method, "/crawl", strings.NewReader(tt.body))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status: got %d, want %d (body %q)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if crawler.calledWith != tt.wantCall {
				t.Errorf("CrawlNow called with %q, want %q", crawler.calledWith, tt.wantCall)
			}
			if tt.wantStatus != http.StatusAccepted {
				return
			}
			var resp map[string]string
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp["status"] != "ACCEPTED" || resp["indexUrl"] != tt.wantCall || resp["runId"] != "run-1" {
				t.Errorf("response: got %v, want status ACCEPTED, indexUrl %q, runId run-1", resp, tt.wantCall)
			}
		})
	}
}

// TestCrawlHandlerCrawlerError checks an engine failure surfaces as 503.
func TestCrawlHandlerCrawlerError(t *testing.T) {
	const someURL = "https://cdn.publisher.example.com/beckn/catalog-index.json"
	h := &crawlHandler{crawler: &stubCrawler{err: errors.New("engine stopped")}}

	req := httptest.NewRequest(http.MethodPost, "/crawl", strings.NewReader(`{"indexUrl":"`+someURL+`"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

// TestNewCrawlHandlerRequiresPlugins covers construction-time refusals.
//
// The registry case is the important one. Every catalog file the crawler
// ingests is verified against the publishing participant's public key as held
// in the network registry, so a crawl handler with no registry plugin builds a
// crawler that can verify nothing and parks every catalog it sees. That failure
// is invisible until an operator notices the parked count hours later, so it
// has to be a loud refusal at construction instead.
func TestNewCrawlHandlerRequiresPlugins(t *testing.T) {
	crawlerCfg := &plugin.Config{ID: "crawler"}
	registryCfg := &plugin.Config{ID: "registry"}

	tests := []struct {
		name   string
		cfg    *Config
		wantIn string
	}{
		{
			name:   "nil config is refused",
			cfg:    nil,
			wantIn: "config is required",
		},
		{
			name:   "no crawler plugin is refused",
			cfg:    &Config{Plugins: PluginCfg{Registry: registryCfg}},
			wantIn: "crawler plugin not configured",
		},
		{
			name:   "no registry plugin is refused",
			cfg:    &Config{Plugins: PluginCfg{Crawler: crawlerCfg}},
			wantIn: "registry plugin is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := NewCrawlHandler(context.Background(), noopPluginManager{}, tt.cfg, "catalogPull")
			if err == nil {
				t.Fatal("expected NewCrawlHandler to fail")
			}
			if h != nil {
				t.Error("a failed NewCrawlHandler must return no handler")
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantIn)
			}
		})
	}
}

// captureCrawlerMgr records what NewCrawlHandler passed to mgr.Crawler, so the
// test can assert the registry really reached the crawler rather than being
// loaded and dropped.
type captureCrawlerMgr struct {
	noopPluginManager
	gotRegistry definition.RegistryLookup
	registry    definition.RegistryLookup
}

func (m *captureCrawlerMgr) Registry(_ context.Context, _ definition.Cache, _ *plugin.Config) (definition.RegistryLookup, error) {
	return m.registry, nil
}

func (m *captureCrawlerMgr) Crawler(_ context.Context, _ definition.SchemaValidator, registry definition.RegistryLookup, _ *plugin.Config) (definition.Crawler, error) {
	m.gotRegistry = registry
	return &stubCrawler{runID: "run-1"}, nil
}

// stubRegistryLookup is an inert RegistryLookup: the test only checks identity,
// never behaviour.
type stubRegistryLookup struct{}

func (stubRegistryLookup) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return nil, nil
}

// The registry the handler loads must be the one the crawler is built with. If
// it were dropped on the floor the crawler would fail closed on every file, and
// nothing else in this handler would notice.
func TestNewCrawlHandlerPassesRegistryToCrawler(t *testing.T) {
	want := stubRegistryLookup{}
	mgr := &captureCrawlerMgr{registry: want}
	cfg := &Config{Plugins: PluginCfg{
		Crawler:  &plugin.Config{ID: "crawler", Config: map[string]string{"CRAWLER_INDEX_URLS": "https://a.example.com/i"}},
		Registry: &plugin.Config{ID: "registry"},
	}}

	if _, err := NewCrawlHandler(context.Background(), mgr, cfg, "catalogPull"); err != nil {
		t.Fatalf("NewCrawlHandler: %v", err)
	}
	if mgr.gotRegistry != definition.RegistryLookup(want) {
		t.Fatalf("crawler got registry %v, want the one the handler loaded (%v)", mgr.gotRegistry, want)
	}
}

// TestCrawlHandlerBodyCap covers the MaxBytesReader guard. /crawl is unsigned
// and DS-internal, so nothing authenticates the caller before the decode: an
// unbounded body would let one request stream arbitrary bytes into
// json.Decoder.
func TestCrawlHandlerBodyCap(t *testing.T) {
	const allowedURL = "https://cdn.publisher.example.com/beckn/catalog-index.json"

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCall   string
	}{
		{
			name:       "a normal body is accepted",
			body:       `{"indexUrl":"` + allowedURL + `"}`,
			wantStatus: http.StatusAccepted,
			wantCall:   allowedURL,
		},
		{
			name: "a body just under the cap is still accepted",
			// Pad with a field the decoder ignores, keeping the whole body under
			// maxCrawlRequestBytes.
			body:       `{"pad":"` + strings.Repeat("x", maxCrawlRequestBytes-200) + `","indexUrl":"` + allowedURL + `"}`,
			wantStatus: http.StatusAccepted,
			wantCall:   allowedURL,
		},
		{
			name:       "an oversized body is rejected before the crawler is reached",
			body:       `{"pad":"` + strings.Repeat("x", maxCrawlRequestBytes+1) + `","indexUrl":"` + allowedURL + `"}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			crawler := &stubCrawler{runID: "run-1"}
			h := &crawlHandler{crawler: crawler}

			req := httptest.NewRequest(http.MethodPost, "/crawl", strings.NewReader(tt.body))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status: got %d, want %d (body %q)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if crawler.calledWith != tt.wantCall {
				t.Errorf("CrawlNow called with %q, want %q", crawler.calledWith, tt.wantCall)
			}
		})
	}
}
