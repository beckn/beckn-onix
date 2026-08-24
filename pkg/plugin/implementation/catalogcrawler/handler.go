package catalogcrawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/beckn-one/beckn-onix/core/module"
	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// RegisterHandler wires this package's crawl-trigger endpoint to the given
// already-running Crawler singleton, registering it as the Provider for
// HandlerTypeCatalogCrawl. Call this once, from main.go, right after the
// Crawler has been constructed and Start()-ed -- CrawlRegistry requires that
// exact instance to already be running, so unlike catalogpublisher's static
// handlerProviders entry (which builds everything it needs from
// PluginManager+config per module), this can't be wired ahead of time; it
// has to close over a concrete object that only exists after startup.
func RegisterHandler(crawler definition.Crawler) {
	module.RegisterProvider(handler.HandlerTypeCatalogCrawl, func(ctx context.Context, mgr handler.PluginManager, cfg *handler.Config, moduleName string) (http.Handler, error) {
		return NewHandler(ctx, crawler, cfg, moduleName)
	})
}

// crawlRequest is this endpoint's own request shape -- not a beckn.yaml
// action, since this is a DS-internal trigger, not a network-facing call.
// NetworkID and NetworkIDs are both accepted and merged, matching the
// catalog-crawler branch's original handler's flexibility.
type crawlRequest struct {
	NetworkID  string   `json:"networkId,omitempty"`
	NetworkIDs []string `json:"networkIds,omitempty"`
}

type crawlResponse struct {
	RunID string `json:"runId"`
}

// NewHandler builds the crawl endpoint: an http.Handler that decodes a
// networkId/networkIds request and triggers an immediate registry-backed
// crawl via crawler.CrawlRegistry, returning a run ID. crawler must already
// be running (Start called) -- CrawlRegistry itself enforces this -- so this
// is always the single Crawler instance main.go starts as a background job,
// wired in via module.RegisterProvider rather than constructed here.
func NewHandler(ctx context.Context, crawler definition.Crawler, cfg *handler.Config, moduleName string) (http.Handler, error) {
	if crawler == nil {
		return nil, fmt.Errorf("crawl handler %s: no Crawler plugin configured/running", moduleName)
	}

	decode := func(ctx context.Context, r *http.Request) (crawlRequest, error) {
		var req crawlRequest
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
				return crawlRequest{}, fmt.Errorf("decoding request body: %w", err)
			}
		}
		ids := req.NetworkIDs
		if req.NetworkID != "" {
			ids = append(ids, req.NetworkID)
		}
		if len(ids) == 0 {
			return crawlRequest{}, fmt.Errorf("at least one of networkId or networkIds is required")
		}
		req.NetworkIDs = ids
		return req, nil
	}

	execute := func(ctx context.Context, req crawlRequest) (crawlResponse, error) {
		runID, err := crawler.CrawlRegistry(ctx, req.NetworkIDs)
		if err != nil {
			return crawlResponse{}, err
		}
		return crawlResponse{RunID: runID}, nil
	}

	encode := func(w http.ResponseWriter, r *http.Request, req crawlRequest, resp crawlResponse, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			log.Errorf(r.Context(), err, "crawl: trigger failed")
			w.WriteHeader(http.StatusInternalServerError)
			if encErr := json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); encErr != nil {
				log.Errorf(r.Context(), encErr, "crawl handler: failed to encode error response")
			}
			return
		}
		log.Debugf(r.Context(), "crawl: triggered runId=%s for networkIds=%v", resp.RunID, req.NetworkIDs)
		w.WriteHeader(http.StatusAccepted)
		if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
			log.Errorf(r.Context(), encErr, "crawl handler: failed to encode response")
		}
	}

	log.Debugf(ctx, "crawl handler %s initialized", moduleName)
	return &handler.EndpointHandler[crawlRequest, crawlResponse]{
		Decode:  decode,
		Execute: execute,
		Encode:  encode,
	}, nil
}
