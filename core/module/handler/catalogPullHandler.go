package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// maxCrawlRequestBytes caps the /crawl request body. The endpoint is
// DS-internal and unsigned, so nothing authenticates the caller before the
// decode; without a cap a single request could stream unbounded bytes into
// json.Decoder and exhaust memory. The body is one small JSON object (two short
// string fields), so 64 KiB is generous.
const maxCrawlRequestBytes = 64 << 10

// crawlHandler serves the DS-internal, unsigned /crawl trigger: it runs an
// immediate registry-backed crawl on demand (basic supportability). The caller
// supplies a REGISTRY URL and the network(s) to query in it; the crawler asks
// the DeDi /query endpoint for that network's providers and crawls each
// discovered index — the SAME discovery the scheduled jobs run in the
// background, so every crawl input is registry-based rather than a raw index
// URL. Same-operator call, so no validateSign/signAck pipeline (see the crawler
// design).
//
// Neither the registry URL nor the discovered index URLs are a trust boundary.
// Every fetched catalog file is verified against the publishing participant's
// signing key as held in the registry, so content from an unrecognised provider
// fails verification and parks. Server-side fetch exposure is bounded by the
// fetch layer's SSRF guard (loopback, private, link-local, CGNAT and reserved
// ranges are refused at dial time), the fetch timeout, and the artifact and
// decompression size caps.
type crawlHandler struct {
	crawler definition.Crawler
}

// crawlRequest is the /crawl body: the registry to discover catalog indexes
// from and the network(s) to query in it. networkIds is the canonical list
// form; networkId is a single-network convenience that is folded into it.
type crawlRequest struct {
	RegistryURL string   `json:"registryUrl"`
	NetworkID   string   `json:"networkId"`
	NetworkIDs  []string `json:"networkIds"`
}

// NewCrawlHandler builds the crawl handler: it loads an optional
// SchemaValidator, the REQUIRED Registry (the crawler's trust anchor for
// publisher signing keys) and the Cache that registry lookups are memoised in,
// constructs + starts the crawler engine via the plugin manager, and returns an
// http.Handler for the on-demand trigger.
func NewCrawlHandler(ctx context.Context, mgr PluginManager, cfg *Config, moduleName string) (http.Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("crawl handler %s: config is required", moduleName)
	}
	if cfg.Plugins.Crawler == nil {
		return nil, fmt.Errorf("crawl handler %s: crawler plugin not configured", moduleName)
	}
	// The registry is not optional here. Every catalog file the crawler ingests
	// is verified against the publishing participant's public key as held in the
	// network registry, so without a registry plugin the crawler can verify
	// nothing and would park every catalog it saw. Refuse to construct rather
	// than start a crawler that is silently inert.
	if cfg.Plugins.Registry == nil {
		return nil, fmt.Errorf("crawl handler %s: registry plugin is required (catalog signatures are verified against the publisher's registry key)", moduleName)
	}

	var validator definition.SchemaValidator
	if cfg.Plugins.SchemaValidator != nil {
		v, err := mgr.SchemaValidator(ctx, cfg.Plugins.SchemaValidator)
		if err != nil {
			return nil, fmt.Errorf("crawl handler %s: failed to load schema validator (%s): %w", moduleName, cfg.Plugins.SchemaValidator.ID, err)
		}
		validator = v
	}

	// mgr.Registry takes a Cache (the registry client memoises lookups in it),
	// so load the cache first — the same order stdHandler.initPlugins uses. The
	// cache is optional: a nil cache means the registry client simply does not
	// memoise, which is correct but chattier.
	cache, err := loadPlugin(ctx, "Cache", cfg.Plugins.Cache, mgr.Cache)
	if err != nil {
		return nil, fmt.Errorf("crawl handler %s: %w", moduleName, err)
	}
	registry, err := loadPlugin(ctx, "Registry", cfg.Plugins.Registry, func(ctx context.Context, c *plugin.Config) (definition.RegistryLookup, error) {
		return mgr.Registry(ctx, cache, c)
	})
	if err != nil {
		return nil, fmt.Errorf("crawl handler %s: %w", moduleName, err)
	}

	crawler, err := mgr.Crawler(ctx, validator, registry, cfg.Plugins.Crawler)
	if err != nil {
		return nil, fmt.Errorf("crawl handler %s: failed to load crawler plugin (%s): %w", moduleName, cfg.Plugins.Crawler.ID, err)
	}

	log.Debugf(ctx, "crawl handler %s initialized", moduleName)
	return &crawlHandler{crawler: crawler}, nil
}

// isHTTPURL reports whether s is an absolute URL over http or https. Any other
// scheme (file, gopher, ftp, a bare host) is not something the crawler fetches.
func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

// ServeHTTP triggers an immediate registry-backed crawl and returns 202
// Accepted -- the crawl runs asynchronously; results surface through the
// crawler's own telemetry, not this response.
func (h *crawlHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Bound the body before decoding it: see maxCrawlRequestBytes. An over-cap
	// body surfaces as a decode error, which is already a 400.
	r.Body = http.MaxBytesReader(w, r.Body, maxCrawlRequestBytes)
	var req crawlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	registryURL := strings.TrimSpace(req.RegistryURL)
	if registryURL == "" {
		http.Error(w, "registryUrl is required", http.StatusBadRequest)
		return
	}
	if !isHTTPURL(registryURL) {
		http.Error(w, "registryUrl must be an absolute http or https URL", http.StatusBadRequest)
		return
	}
	networks := resolveNetworks(req)
	if len(networks) == 0 {
		http.Error(w, "networkId (or networkIds) is required", http.StatusBadRequest)
		return
	}
	// CrawlRegistry returns immediately, launching a tracked goroutine on the
	// engine's own context (drained by Stop) — so we don't run a detached crawl
	// on a context the shutdown path can't reach (avoids DB use-after-close).
	runID, err := h.crawler.CrawlRegistry(r.Context(), registryURL, networks)
	if err != nil {
		log.Errorf(r.Context(), err, "crawl: trigger failed for registry %s", registryURL)
		http.Error(w, "crawl trigger unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ACCEPTED", "registryUrl": registryURL, "networkIds": networks, "runId": runID,
	})
}

// resolveNetworks collects the network ids from the request — the canonical
// networkIds list plus the single-network networkId convenience — trimming
// blanks and de-duplicating while preserving order.
func resolveNetworks(req crawlRequest) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	for _, n := range req.NetworkIDs {
		add(n)
	}
	add(req.NetworkID)
	return out
}
