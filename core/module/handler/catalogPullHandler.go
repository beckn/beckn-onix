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

// crawlHandler serves the DS-internal, unsigned /crawl trigger: it re-crawls
// one provider's index on demand (basic supportability for a stuck
// publisher). The crawler's scheduled jobs run in the background from plugin
// init; this handler only pokes an immediate pass. Same-operator call, so no
// validateSign/signAck pipeline (see the crawler design).
// The caller supplies the index URL to crawl: for this phase the crawler API
// ingests it directly rather than resolving it from the registry, so the URL
// is not required to be one the crawler was configured with. Verifying the URL
// against the registry before queueing is the final design and is deferred.
//
// The URL itself is not a trust boundary. Every fetched catalog file is
// verified against the publishing participant's signing key as held in the
// registry, so content from an unrecognised index fails verification and parks.
// Server-side fetch exposure is bounded by the fetch layer's SSRF guard
// (loopback, private, link-local, CGNAT and reserved ranges are refused at dial
// time), the fetch timeout, and the artifact and decompression size caps.
type crawlHandler struct {
	crawler definition.Crawler
}

// crawlRequest is the /crawl body: the index URL to re-crawl now.
// participantId is accepted for forward-compatibility with DID resolution
// but is not yet used.
type crawlRequest struct {
	IndexURL      string `json:"indexUrl"`
	ParticipantID string `json:"participantId"`
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

// ServeHTTP triggers an immediate crawl of the given index URL and returns
// 202 Accepted -- the crawl runs asynchronously; results surface through the
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
	indexURL := strings.TrimSpace(req.IndexURL)
	if indexURL == "" {
		http.Error(w, "indexUrl is required", http.StatusBadRequest)
		return
	}
	if !isHTTPURL(indexURL) {
		http.Error(w, "indexUrl must be an absolute http or https URL", http.StatusBadRequest)
		return
	}
	// CrawlNow returns immediately, launching a tracked goroutine on the
	// engine's own context (drained by Stop) — so we don't run a detached crawl
	// on a context the shutdown path can't reach (avoids DB use-after-close).
	runID, err := h.crawler.CrawlNow(r.Context(), indexURL)
	if err != nil {
		log.Errorf(r.Context(), err, "crawl: trigger failed for %s", indexURL)
		http.Error(w, "crawl trigger unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ACCEPTED", "indexUrl": indexURL, "runId": runID})
}
