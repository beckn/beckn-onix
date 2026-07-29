package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// crawlHandler serves the DS-internal, unsigned /crawl trigger: it re-crawls
// one provider's index on demand (basic supportability for a stuck
// publisher). The crawler's scheduled jobs run in the background from plugin
// init; this handler only pokes an immediate pass. Same-operator call, so no
// validateSign/signAck pipeline (see the crawler design).
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
// SchemaValidator, constructs + starts the crawler engine via the plugin
// manager, and returns an http.Handler for the on-demand trigger.
func NewCrawlHandler(ctx context.Context, mgr PluginManager, cfg *Config, moduleName string) (http.Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("crawl handler %s: config is required", moduleName)
	}
	if cfg.Plugins.Crawler == nil {
		return nil, fmt.Errorf("crawl handler %s: crawler plugin not configured", moduleName)
	}

	var validator definition.SchemaValidator
	if cfg.Plugins.SchemaValidator != nil {
		v, err := mgr.SchemaValidator(ctx, cfg.Plugins.SchemaValidator)
		if err != nil {
			return nil, fmt.Errorf("crawl handler %s: failed to load schema validator (%s): %w", moduleName, cfg.Plugins.SchemaValidator.ID, err)
		}
		validator = v
	}

	crawler, err := mgr.Crawler(ctx, validator, cfg.Plugins.Crawler)
	if err != nil {
		return nil, fmt.Errorf("crawl handler %s: failed to load crawler plugin (%s): %w", moduleName, cfg.Plugins.Crawler.ID, err)
	}

	log.Debugf(ctx, "crawl handler %s initialized", moduleName)
	return &crawlHandler{crawler: crawler}, nil
}

// ServeHTTP triggers an immediate crawl of the given index URL and returns
// 202 Accepted -- the crawl runs asynchronously; results surface through the
// crawler's own telemetry, not this response.
func (h *crawlHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req crawlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.IndexURL == "" {
		http.Error(w, "indexUrl is required", http.StatusBadRequest)
		return
	}

	// CrawlNow returns immediately, launching a tracked goroutine on the
	// engine's own context (drained by Stop) — so we don't run a detached crawl
	// on a context the shutdown path can't reach (avoids DB use-after-close).
	runID, err := h.crawler.CrawlNow(r.Context(), req.IndexURL)
	if err != nil {
		log.Errorf(r.Context(), err, "crawl: trigger failed for %s", req.IndexURL)
		http.Error(w, "crawl trigger unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ACCEPTED", "indexUrl": req.IndexURL, "runId": runID})
}
