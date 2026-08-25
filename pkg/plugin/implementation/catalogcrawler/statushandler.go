package catalogcrawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// statusRequest is this endpoint's own request shape: SubscriberID comes
// only from the verified Authorization header (never a request parameter --
// see NewStatusHandler's doc comment), CatalogID from an optional query
// param.
type statusRequest struct {
	SubscriberID string
	CatalogID    string
}

type statusResponse struct {
	Catalogs []definition.CrawlStatus
}

// NewStatusHandler builds the catalogCrawlStatus endpoint: a GET returning
// the crawl/sync status of the authenticated caller's own catalogs (or just
// one, via ?catalogId=). Unlike catalogPublish/catalogCrawl -- DS-internal,
// unsigned, same-operator triggers -- this answers a specific publisher
// about their own data, so it is a signed, network-facing call and must
// authenticate the caller the same way every other subscriber-facing call
// in this codebase does: verify the Authorization header via
// signValidator + keyManager.LookupNPKeys, exactly as
// core/module/handler's validateSign step does. That step isn't reusable
// directly here -- it's wired into the std handler's step pipeline (a
// hardcoded switch in stdHandler.go's initSteps, not a pluggable
// mechanism), and std's pipeline drags in Beckn-action machinery
// (addRoute, signAck, response steps) this endpoint has no use for. So the
// same two calls (LookupNPKeys, Validate) are made directly in Decode
// instead, using handler.ParseAuthHeader to parse the header exactly as
// validateSign's own parseHeader would -- no new verification logic, just
// inlined rather than routed through a step.
//
// The verified subscriberId becomes the only identity Execute is ever
// scoped by; there is no subscriberId request parameter, since it would be
// redundant with (and could disagree with) the one the signature already
// authenticates.
func NewStatusHandler(ctx context.Context, mgr handler.PluginManager, cfg *handler.Config, moduleName string) (http.Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("catalogCrawlStatus handler %s: config is required", moduleName)
	}

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

	km, err := handler.LoadKeyManager(ctx, mgr, registry, cfg.Plugins.KeyManager)
	if err != nil {
		return nil, err
	}
	if km == nil {
		return nil, fmt.Errorf("catalogCrawlStatus handler %s: keyManager plugin not configured", moduleName)
	}

	signValidator, err := handler.LoadPlugin(ctx, "SignValidator", cfg.Plugins.SignValidator, mgr.SignValidator)
	if err != nil {
		return nil, err
	}
	if signValidator == nil {
		return nil, fmt.Errorf("catalogCrawlStatus handler %s: signValidator plugin not configured", moduleName)
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

		authHeaderValue := r.Header.Get(model.AuthHeaderSubscriber)
		if authHeaderValue == "" {
			return statusRequest{}, &handler.StatusError{
				Status: http.StatusUnauthorized,
				Err:    fmt.Errorf("%s header is required", model.AuthHeaderSubscriber),
			}
		}
		headerVals, err := handler.ParseAuthHeader(authHeaderValue)
		if err != nil {
			return statusRequest{}, &handler.StatusError{Status: http.StatusUnauthorized, Err: err}
		}
		if headerVals.Algorithm != "ed25519" {
			return statusRequest{}, &handler.StatusError{
				Status: http.StatusUnauthorized,
				Err:    fmt.Errorf("unsupported algorithm %q: only ed25519 is permitted", headerVals.Algorithm),
			}
		}

		signingPublicKey, _, err := km.LookupNPKeys(ctx, headerVals.SubscriberID, headerVals.UniqueID)
		if err != nil {
			return statusRequest{}, &handler.StatusError{
				Status: http.StatusUnauthorized,
				Err:    fmt.Errorf("failed to get validation key: %w", err),
			}
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			return statusRequest{}, fmt.Errorf("reading request body: %w", err)
		}
		// checkIdentity=true, same as every other subscriber-facing call --
		// a bodyless GET has no context.bap_id/bpp_id to cross-check
		// against, so checkSubscriberIdentity degrades to a no-op (already
		// relied on elsewhere for bodyless GET/DELETE requests), not an
		// error.
		stepCtx := &model.StepContext{Context: ctx, Body: body}
		if err := signValidator.Validate(stepCtx, authHeaderValue, signingPublicKey, true); err != nil {
			return statusRequest{}, &handler.StatusError{
				Status: http.StatusUnauthorized,
				Err:    fmt.Errorf("sign validation failed: %w", err),
			}
		}

		return statusRequest{
			SubscriberID: headerVals.SubscriberID,
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

	log.Debugf(ctx, "catalogCrawlStatus handler %s initialized", moduleName)
	return &handler.EndpointHandler[statusRequest, statusResponse]{
		Decode:  decode,
		Execute: execute,
		Encode:  encode,
	}, nil
}
