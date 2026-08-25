package catalogcrawler

// trigger.go — the /crawl/trigger sub-endpoint: a DS-internal, unsigned
// on-demand crawl trigger. Not a beckn.yaml action; not network-facing.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// crawlRequest is this sub-endpoint's own request shape. NetworkID and
// NetworkIDs are both accepted and merged, matching the catalog-crawler
// branch's original handler's flexibility.
type crawlRequest struct {
	NetworkID  string   `json:"networkId,omitempty"`
	NetworkIDs []string `json:"networkIds,omitempty"`
}

type crawlResponse struct {
	RunID string `json:"runId"`
}

// newTriggerHandler builds the /crawl/trigger sub-endpoint: it decodes a
// networkId/networkIds request and triggers an immediate registry-backed
// crawl via crawler.CrawlRegistry, returning a run ID. crawler must
// already be running (Start called) -- CrawlRegistry itself enforces this.
func newTriggerHandler(crawler definition.Crawler) http.Handler {
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
				log.Errorf(r.Context(), encErr, "crawl trigger: failed to encode error response")
			}
			return
		}
		log.Debugf(r.Context(), "crawl: triggered runId=%s for networkIds=%v", resp.RunID, req.NetworkIDs)
		w.WriteHeader(http.StatusAccepted)
		if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
			log.Errorf(r.Context(), encErr, "crawl trigger: failed to encode response")
		}
	}

	return &handler.EndpointHandler[crawlRequest, crawlResponse]{
		Decode:  decode,
		Execute: execute,
		Encode:  encode,
	}
}
