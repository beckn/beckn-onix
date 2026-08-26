package catalogcrawler

// status.go — the /crawl/status sub-endpoint: crawl/sync status for the
// catalogs owned by ?subscriberId= (or just ?catalogId=, if given).
//
// Signed-request verification is NOT implemented yet. Eventually this
// answers a specific publisher about their own data and needs to
// authenticate the caller the same way every other subscriber-facing call
// in this codebase does (signValidator + keyManager.LookupNPKeys, the way
// validateSign verifies inbound requests) -- but that's deliberately left
// out of this first cut so the endpoint's actual query/response behavior
// can be exercised end to end first. cfg.AuthDisabled must be explicitly
// true to serve this sub-endpoint at all -- a request is rejected
// (checked here, not at NewHandler construction time, so a missing/false
// AuthDisabled can't take down /crawl/trigger, which needs no auth of its
// own) when it's false/unset, rather than silently serving every
// subscriber's status to anyone. Once signed verification lands,
// AuthDisabled=false will wire it in and SubscriberID will come from the
// verified Authorization header instead of this query param.
//
// Reuses the same running Crawler singleton /crawl/trigger uses -- Status
// only reads persisted state, so unlike CrawlRegistry it works whether or
// not the instance has been Start()-ed, and there's no reason to build a
// second one just to serve a read.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// statusRequest is this sub-endpoint's own request shape. Both fields come
// from query params right now -- see this file's own doc comment on
// AuthDisabled for why SubscriberID isn't (yet) a verified identity.
type statusRequest struct {
	SubscriberID string
	CatalogID    string
}

type statusResponse struct {
	Catalogs []definition.CrawlStatus
}

// newStatusHandler builds the /crawl/status sub-endpoint.
func newStatusHandler(crawler definition.Crawler, cfg *handler.Config) http.Handler {
	decode := func(ctx context.Context, r *http.Request) (statusRequest, error) {
		if r.Method != http.MethodGet {
			return statusRequest{}, &handler.StatusError{
				Status: http.StatusMethodNotAllowed,
				Err:    fmt.Errorf("method not allowed: %s", r.Method),
			}
		}
		if !cfg.AuthDisabled {
			return statusRequest{}, &handler.StatusError{
				Status: http.StatusServiceUnavailable,
				Err:    fmt.Errorf("signed-request verification is not implemented yet; set authDisabled: true to use this endpoint unauthenticated until then"),
			}
		}
		subscriberID := r.URL.Query().Get("subscriberId")
		if subscriberID == "" {
			return statusRequest{}, &handler.StatusError{
				Status: http.StatusBadRequest,
				Err:    fmt.Errorf("subscriberId query param is required (authDisabled mode has no verified identity to derive it from)"),
			}
		}
		return statusRequest{
			SubscriberID: subscriberID,
			CatalogID:    r.URL.Query().Get("catalogId"),
		}, nil
	}

	execute := func(ctx context.Context, req statusRequest) (statusResponse, error) {
		rows, err := crawler.Status(ctx, req.SubscriberID, req.CatalogID)
		if err != nil {
			return statusResponse{}, err
		}
		return statusResponse{Catalogs: rows}, nil
	}

	encode := func(w http.ResponseWriter, r *http.Request, req statusRequest, resp statusResponse, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			log.Errorf(r.Context(), err, "catalogCrawlStatus: query failed")
			w.WriteHeader(http.StatusInternalServerError)
			if encErr := json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); encErr != nil {
				log.Errorf(r.Context(), encErr, "crawl status: failed to encode error response")
			}
			return
		}
		// A non-empty catalogId that matched nothing (never crawled, or
		// belongs to a different subscriber -- Crawler.Status makes the
		// two indistinguishable on purpose) is a 404, not a 200 with an
		// empty list: it was a specific, singular lookup.
		if req.CatalogID != "" && len(resp.Catalogs) == 0 {
			w.WriteHeader(http.StatusNotFound)
			if encErr := json.NewEncoder(w).Encode(map[string]string{"error": "catalog not found"}); encErr != nil {
				log.Errorf(r.Context(), encErr, "crawl status: failed to encode error response")
			}
			return
		}
		if encErr := json.NewEncoder(w).Encode(resp.Catalogs); encErr != nil {
			log.Errorf(r.Context(), encErr, "crawl status: failed to encode response")
		}
	}

	return &handler.EndpointHandler[statusRequest, statusResponse]{
		Decode:  decode,
		Execute: execute,
		Encode:  encode,
	}
}
