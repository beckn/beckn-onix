package catalogcrawler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// statusRequest is this endpoint's own request shape. Both fields come from
// query params right now -- see NewStatusHandler's doc comment on
// AuthDisabled for why SubscriberID isn't (yet) a verified identity.
type statusRequest struct {
	SubscriberID string
	CatalogID    string
}

type statusResponse struct {
	Catalogs []definition.CrawlStatus
}

// NewStatusHandler builds the catalogCrawlStatus endpoint: a GET returning
// the crawl/sync status of the catalogs owned by ?subscriberId= (or just
// one, via ?catalogId=).
//
// Signed-request verification is NOT implemented yet. Eventually this
// answers a specific publisher about their own data and needs to
// authenticate the caller the same way every other subscriber-facing call
// in this codebase does (signValidator + keyManager.LookupNPKeys, the way
// validateSign verifies inbound requests) -- but that's deliberately left
// out of this first cut so the endpoint's actual query/response behavior
// can be exercised end to end first. cfg.AuthDisabled is required to be
// explicitly true to construct this handler at all, so a deployment can't
// end up running this unauthenticated by omission -- leaving it
// false/unset (the default) fails fast at startup instead of silently
// serving every subscriber's status to anyone. Once signed verification
// lands, AuthDisabled=false will wire it in and SubscriberID will come
// from the verified Authorization header instead of this query param.
func NewStatusHandler(ctx context.Context, mgr handler.PluginManager, cfg *handler.Config, moduleName string) (http.Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("catalogCrawlStatus handler %s: config is required", moduleName)
	}
	if !cfg.AuthDisabled {
		return nil, fmt.Errorf("catalogCrawlStatus handler %s: signed-request verification is not implemented yet; set authDisabled: true to run this endpoint unauthenticated until then", moduleName)
	}

	// registry here is unrelated to authenticating the caller (that's the
	// AuthDisabled bit above) -- it's catalogcrawler.Provider.New's own
	// hard dependency, used to verify fetched catalogs' self-signatures,
	// required unconditionally regardless of how this endpoint verifies
	// its own callers.
	cache, err := handler.LoadPlugin(ctx, "Cache", cfg.Plugins.Cache, mgr.Cache)
	if err != nil {
		return nil, err
	}
	registry, err := handler.LoadPlugin(ctx, "Registry", cfg.Plugins.Registry, func(ctx context.Context, c *plugin.Config) (definition.RegistryLookup, error) {
		return mgr.Registry(ctx, cache, c)
	})
	if err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, fmt.Errorf("catalogCrawlStatus handler %s: registry plugin not configured", moduleName)
	}

	if cfg.Plugins.Crawler == nil {
		return nil, fmt.Errorf("catalogCrawlStatus handler %s: crawler plugin not configured", moduleName)
	}
	crawler, err := mgr.Crawler(ctx, registry, cfg.Plugins.Crawler)
	if err != nil {
		return nil, fmt.Errorf("catalogCrawlStatus handler %s: failed to load crawler plugin (%s): %w", moduleName, cfg.Plugins.Crawler.ID, err)
	}

	decode := func(ctx context.Context, r *http.Request) (statusRequest, error) {
		if r.Method != http.MethodGet {
			return statusRequest{}, &handler.StatusError{
				Status: http.StatusMethodNotAllowed,
				Err:    fmt.Errorf("method not allowed: %s", r.Method),
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
				log.Errorf(r.Context(), encErr, "catalogCrawlStatus handler: failed to encode error response")
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
				log.Errorf(r.Context(), encErr, "catalogCrawlStatus handler: failed to encode error response")
			}
			return
		}
		if encErr := json.NewEncoder(w).Encode(resp.Catalogs); encErr != nil {
			log.Errorf(r.Context(), encErr, "catalogCrawlStatus handler: failed to encode response")
		}
	}

	log.Debugf(ctx, "catalogCrawlStatus handler %s initialized (authDisabled=true)", moduleName)
	return &handler.EndpointHandler[statusRequest, statusResponse]{
		Decode:  decode,
		Execute: execute,
		Encode:  encode,
	}, nil
}
