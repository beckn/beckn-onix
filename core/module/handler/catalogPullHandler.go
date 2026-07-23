package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// catalogPullHandler serves a DS-internal, unsigned catalog/pull trigger: it
// invokes a Crawler synchronously and returns a CatalogPullCallbackAction-
// shaped body ({status, catalogs, error}, matching beckn.yaml exactly), no
// other crawler-internal metadata. Unlike stdHandler, there is no
// validateSign/addRoute/signAck pipeline here -- the caller is the DS's own
// backend on the same trust domain, not another network participant (see
// onix-catalog-crawler-plugin-requirements.md and the catalog-crawler
// design discussion for why this is a distinct handler type rather than a
// std module).
type catalogPullHandler struct {
	crawler definition.Crawler
}

// pullRequest is the DS-facing catalog/pull request body. ReceiverID
// mirrors Context.receiverId from beckn.yaml (the PN's DID) -- but for now
// its value is treated as a literal domain/URI, the same way bppUri was
// before this rename, since DID resolution isn't implemented yet. This is
// a field-name alignment with the spec, not yet real DID resolution.
type pullRequest struct {
	ReceiverID string `json:"receiverId"`
	NetworkID  string `json:"networkId"`
	Mode       string `json:"mode"`
}

// NewCatalogPullHandler builds the catalogPull handler type: it loads a
// Signer, KeyManager, and Crawler from cfg.Plugins and returns an
// http.Handler that serves POST requests by invoking Crawler.CrawlSubscriber.
func NewCatalogPullHandler(ctx context.Context, mgr PluginManager, cfg *Config, moduleName string) (http.Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("catalogPull handler %s: config is required", moduleName)
	}

	signer, err := loadPlugin(ctx, "Signer", cfg.Plugins.Signer, mgr.Signer)
	if err != nil {
		return nil, err
	}

	cache, err := loadPlugin(ctx, "Cache", cfg.Plugins.Cache, mgr.Cache)
	if err != nil {
		return nil, err
	}

	registry, err := loadRegistryForCatalogPull(ctx, mgr, cache, cfg)
	if err != nil {
		return nil, err
	}

	km, err := loadKeyManager(ctx, mgr, registry, cfg.Plugins.KeyManager)
	if err != nil {
		return nil, err
	}

	if cfg.Plugins.Crawler == nil {
		return nil, fmt.Errorf("catalogPull handler %s: crawler plugin not configured", moduleName)
	}
	crawler, err := mgr.Crawler(ctx, signer, km, cfg.Plugins.Crawler)
	if err != nil {
		return nil, fmt.Errorf("catalogPull handler %s: failed to load crawler plugin (%s): %w", moduleName, cfg.Plugins.Crawler.ID, err)
	}

	log.Debugf(ctx, "catalogPull handler %s initialized", moduleName)
	return &catalogPullHandler{crawler: crawler}, nil
}

// loadRegistryForCatalogPull loads a RegistryLookup purely to satisfy
// KeyManager's constructor -- catalog-crawler itself never calls it, since
// it resolves the PN domain directly from the DS-supplied receiverId, not
// via registry lookup. This mirrors every other module's local-dev config
// (e.g. bppTxnReceiver/bppTxnCaller in config/local-beckn-one-bpp.yaml),
// which configures a registry+cache alongside keyManager even when using
// static keys, because KeyManagerProvider.New always requires one.
func loadRegistryForCatalogPull(ctx context.Context, mgr PluginManager, cache definition.Cache, cfg *Config) (definition.RegistryLookup, error) {
	if cfg.Plugins.Registry == nil {
		log.Debug(ctx, "Skipping Registry plugin: not configured")
		return nil, nil
	}
	registry, err := mgr.Registry(ctx, cache, cfg.Plugins.Registry)
	if err != nil {
		return nil, fmt.Errorf("failed to load Registry plugin (%s): %w", cfg.Plugins.Registry.ID, err)
	}
	return registry, nil
}

// pullStatus mirrors beckn.yaml CatalogPullCallbackAction's status enum.
type pullStatus string

const (
	pullStatusCompleted pullStatus = "COMPLETED"
	pullStatusFailed    pullStatus = "FAILED"
)

// pullResponse matches beckn.yaml's CatalogPullCallbackAction shape exactly:
// "status" is required (COMPLETED|FAILED); "catalogs" is present when
// COMPLETED; "error" is present when FAILED. No crawler-internal
// bookkeeping (catalogId, version, digests, verification outcomes) is
// included -- those are logged, not returned.
type pullResponse struct {
	Status   pullStatus        `json:"status"`
	Catalogs []json.RawMessage `json:"catalogs,omitempty"`
	Error    *model.Error      `json:"error,omitempty"`
}

// ServeHTTP parses the DS-internal request body, runs the crawl, and
// returns a CatalogPullCallbackAction-shaped body. There is no
// ACK/callback split -- crawl latency is bounded by the crawler's own
// fetch-timeout config, so a single synchronous response suffices.
//
// A malformed request (bad body, missing receiverId) is a transport-level 400,
// not a CatalogPullCallbackAction concept. Once the crawl itself runs,
// every outcome -- including a fatal crawl failure -- is reported as a 200
// with status COMPLETED/FAILED in the body, matching how the original
// on_pull callback always carried its own status regardless of the HTTP
// transport result.
func (h *catalogPullHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req pullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.ReceiverID == "" {
		http.Error(w, "receiverId is required", http.StatusBadRequest)
		return
	}

	result, err := h.crawler.CrawlSubscriber(r.Context(), definition.CrawlRequest{
		SubscriberID: req.ReceiverID,
		NetworkID:    req.NetworkID,
		Mode:         definition.CrawlMode(req.Mode),
	})
	if err != nil {
		log.Errorf(r.Context(), err, "catalogPull: crawl failed for %s", req.ReceiverID)
		writeJSON(w, r, pullResponse{
			Status: pullStatusFailed,
			Error:  model.NewCodedError("BIZ_CRAWL_FAILED", err.Error()),
		})
		return
	}

	for _, e := range result.Errors {
		log.Warnf(r.Context(), "catalogPull: %s crawl error for catalog %s at stage %s: %s", req.ReceiverID, e.CatalogID, e.Stage, e.Reason)
	}
	for _, c := range result.Catalogs {
		log.Debugf(r.Context(), "catalogPull: %s catalog %s v%d status=%s digestMatch=%t",
			req.ReceiverID, c.CatalogID, c.Version, c.Status, c.Verification.DigestMatch)
	}

	catalogs := make([]json.RawMessage, 0, len(result.Catalogs))
	for _, c := range result.Catalogs {
		if len(c.Catalog) > 0 {
			catalogs = append(catalogs, c.Catalog)
		}
	}

	writeJSON(w, r, pullResponse{Status: pullStatusCompleted, Catalogs: catalogs})
}

func writeJSON(w http.ResponseWriter, r *http.Request, body pullResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Errorf(r.Context(), err, "catalogPull handler: failed to encode response")
	}
}
