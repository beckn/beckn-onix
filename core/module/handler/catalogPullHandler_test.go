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

// stubCrawler records the registry URL + networks the handler forwarded, so a
// test can tell "rejected before dispatch" apart from "accepted and dispatched".
type stubCrawler struct {
	calledURL      string
	calledNetworks []string
	runID          string
	err            error
}

func (c *stubCrawler) Start(context.Context) error { return nil }
func (c *stubCrawler) Stop() error                 { return nil }
func (c *stubCrawler) CrawlRegistry(_ context.Context, registryURL string, networkIDs []string) (string, error) {
	c.calledURL = registryURL
	c.calledNetworks = networkIDs
	if c.err != nil {
		return "", c.err
	}
	return c.runID, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// jsonStrings converts a decoded JSON array (of strings) into []string.
func jsonStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

// TestCrawlHandlerServeHTTP covers the trigger's request validation: a registry
// URL plus at least one network is discovered + crawled; malformed input, a
// non-http(s) registry, a missing network, and wrong methods are refused before
// the crawler is reached.
func TestCrawlHandlerServeHTTP(t *testing.T) {
	const registryURL = "https://fabric.example.com/registry/dedi"
	const net1 = "beckn.one/testnet"
	const net2 = "beckn.one/mainnet"

	tests := []struct {
		name         string
		method       string
		body         string
		wantStatus   int
		wantCall     string
		wantNetworks []string
	}{
		{
			name:         "registry url + network is accepted",
			method:       http.MethodPost,
			body:         `{"registryUrl":"` + registryURL + `","networkId":"` + net1 + `"}`,
			wantStatus:   http.StatusAccepted,
			wantCall:     registryURL,
			wantNetworks: []string{net1},
		},
		{
			name:         "surrounding space is trimmed",
			method:       http.MethodPost,
			body:         `{"registryUrl":"  ` + registryURL + `  ","networkId":"  ` + net1 + `  "}`,
			wantStatus:   http.StatusAccepted,
			wantCall:     registryURL,
			wantNetworks: []string{net1},
		},
		{
			name:         "networkIds list form is accepted",
			method:       http.MethodPost,
			body:         `{"registryUrl":"` + registryURL + `","networkIds":["` + net1 + `","` + net2 + `"]}`,
			wantStatus:   http.StatusAccepted,
			wantCall:     registryURL,
			wantNetworks: []string{net1, net2},
		},
		{
			name:         "networkId and networkIds merge and dedup, order preserved",
			method:       http.MethodPost,
			body:         `{"registryUrl":"` + registryURL + `","networkId":"` + net1 + `","networkIds":["` + net2 + `","` + net1 + `"]}`,
			wantStatus:   http.StatusAccepted,
			wantCall:     registryURL,
			wantNetworks: []string{net2, net1}, // list first, then networkId; net1 already seen
		},
		{
			name:         "plain http registry is accepted",
			method:       http.MethodPost,
			body:         `{"registryUrl":"http://registry-origin/dedi","networkId":"` + net1 + `"}`,
			wantStatus:   http.StatusAccepted,
			wantCall:     "http://registry-origin/dedi",
			wantNetworks: []string{net1},
		},
		{
			name:       "file scheme registry is rejected",
			method:     http.MethodPost,
			body:       `{"registryUrl":"file:///etc/passwd","networkId":"` + net1 + `"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non http scheme registry is rejected",
			method:     http.MethodPost,
			body:       `{"registryUrl":"gopher://registry:70/","networkId":"` + net1 + `"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "relative registry url is rejected",
			method:     http.MethodPost,
			body:       `{"registryUrl":"/dedi","networkId":"` + net1 + `"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty registry url is rejected",
			method:     http.MethodPost,
			body:       `{"registryUrl":"","networkId":"` + net1 + `"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "blank registry url is rejected",
			method:     http.MethodPost,
			body:       `{"registryUrl":"   ","networkId":"` + net1 + `"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing network is rejected",
			method:     http.MethodPost,
			body:       `{"registryUrl":"` + registryURL + `"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "blank network is rejected",
			method:     http.MethodPost,
			body:       `{"registryUrl":"` + registryURL + `","networkId":"   "}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed body is rejected",
			method:     http.MethodPost,
			body:       `{"registryUrl":`,
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
			body:       `{"registryUrl":"` + registryURL + `","networkId":"` + net1 + `"}`,
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
			if crawler.calledURL != tt.wantCall {
				t.Errorf("CrawlRegistry called with registry %q, want %q", crawler.calledURL, tt.wantCall)
			}
			if !equalStrings(crawler.calledNetworks, tt.wantNetworks) {
				t.Errorf("CrawlRegistry networks = %v, want %v", crawler.calledNetworks, tt.wantNetworks)
			}
			if tt.wantStatus != http.StatusAccepted {
				return
			}
			var resp map[string]any
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp["status"] != "ACCEPTED" || resp["registryUrl"] != tt.wantCall || resp["runId"] != "run-1" {
				t.Errorf("response: got %v, want status ACCEPTED, registryUrl %q, runId run-1", resp, tt.wantCall)
			}
			if got := jsonStrings(resp["networkIds"]); !equalStrings(got, tt.wantNetworks) {
				t.Errorf("response networkIds = %v, want %v", got, tt.wantNetworks)
			}
		})
	}
}

// TestCrawlHandlerCrawlerError checks an engine failure surfaces as 503.
func TestCrawlHandlerCrawlerError(t *testing.T) {
	const registryURL = "https://fabric.example.com/registry/dedi"
	h := &crawlHandler{crawler: &stubCrawler{err: errors.New("engine stopped")}}

	req := httptest.NewRequest(http.MethodPost, "/crawl",
		strings.NewReader(`{"registryUrl":"`+registryURL+`","networkId":"beckn.one/testnet"}`))
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
	const registryURL = "https://fabric.example.com/registry/dedi"
	const net1 = "beckn.one/testnet"

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCall   string
	}{
		{
			name:       "a normal body is accepted",
			body:       `{"registryUrl":"` + registryURL + `","networkId":"` + net1 + `"}`,
			wantStatus: http.StatusAccepted,
			wantCall:   registryURL,
		},
		{
			name: "a body just under the cap is still accepted",
			// Pad with a field the decoder ignores, keeping the whole body under
			// maxCrawlRequestBytes.
			body:       `{"pad":"` + strings.Repeat("x", maxCrawlRequestBytes-200) + `","registryUrl":"` + registryURL + `","networkId":"` + net1 + `"}`,
			wantStatus: http.StatusAccepted,
			wantCall:   registryURL,
		},
		{
			name:       "an oversized body is rejected before the crawler is reached",
			body:       `{"pad":"` + strings.Repeat("x", maxCrawlRequestBytes+1) + `","registryUrl":"` + registryURL + `","networkId":"` + net1 + `"}`,
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
			if crawler.calledURL != tt.wantCall {
				t.Errorf("CrawlRegistry called with registry %q, want %q", crawler.calledURL, tt.wantCall)
			}
		})
	}
}
