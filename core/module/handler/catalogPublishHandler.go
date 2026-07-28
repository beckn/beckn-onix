package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher/localstore"
)

// catalogPublishHandler serves a DS-internal, unsigned catalog/publish
// trigger: it invokes a CatalogPublisher synchronously with the catalogs in
// the request body, persists the result under a common local output root
// (localstore), and returns a CatalogProcessingResult-shaped body per
// catalog -- borrowing that vocabulary (ACCEPTED/REJECTED) from beckn.yaml's
// CatalogPublishAction/OnCatalogPublishAction, but as one synchronous
// response rather than an Ack-now/on_publish-later pair: this is a
// DS-internal trigger, not a network-facing Beckn action, so there is no
// validateSign/addRoute/signAck pipeline and no async callback.
//
// Only public catalogs are supported in this phase: every submission is
// published with no networkIds/authMethods, matching
// catalogpublisher.CatalogSubmission's zero value. Restricted-catalog
// support is a later phase, tracked in this package's README.
type catalogPublishHandler struct {
	publisher  definition.CatalogPublisher
	outputRoot string
}

// publishRequest is the DS-facing catalog/publish request body. Catalogs
// carries plain Beckn Catalog objects (no context/message envelope, unlike
// beckn.yaml's CatalogPublishAction) -- each entry's own top-level "id"
// field is used verbatim as its catalogId; this handler does not prefix or
// derive it from a domain, unlike catalogpublisherctl's convenience
// defaulting. Retire/ForceBaseline map 1:1 onto
// definition.PublishRequest's fields of the same purpose.
type publishRequest struct {
	Catalogs      []json.RawMessage `json:"catalogs"`
	Retire        []string          `json:"retire,omitempty"`
	ForceBaseline bool              `json:"forceBaseline,omitempty"`
}

// NewCatalogPublishHandler builds the catalogPublish handler type: it loads
// a KeyManager and CatalogPublisher from cfg.Plugins and returns an
// http.Handler that serves POST requests by invoking
// CatalogPublisher.Publish and persisting the result under
// cfg.OutputRoot.
func NewCatalogPublishHandler(ctx context.Context, mgr PluginManager, cfg *Config, moduleName string) (http.Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("catalogPublish handler %s: config is required", moduleName)
	}
	if cfg.OutputRoot == "" {
		return nil, fmt.Errorf("catalogPublish handler %s: outputRoot is required", moduleName)
	}

	cache, err := loadPlugin(ctx, "Cache", cfg.Plugins.Cache, mgr.Cache)
	if err != nil {
		return nil, err
	}

	registry, err := loadPlugin(ctx, "Registry", cfg.Plugins.Registry, func(ctx context.Context, c *plugin.Config) (definition.RegistryLookup, error) {
		return mgr.Registry(ctx, cache, c)
	})
	if err != nil {
		return nil, err
	}

	km, err := loadKeyManager(ctx, mgr, registry, cfg.Plugins.KeyManager)
	if err != nil {
		return nil, err
	}

	if cfg.Plugins.CatalogPublisher == nil {
		return nil, fmt.Errorf("catalogPublish handler %s: catalogPublisher plugin not configured", moduleName)
	}
	publisher, err := mgr.CatalogPublisher(ctx, km, cfg.Plugins.CatalogPublisher)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: failed to load catalogPublisher plugin (%s): %w", moduleName, cfg.Plugins.CatalogPublisher.ID, err)
	}

	log.Debugf(ctx, "catalogPublish handler %s initialized, outputRoot=%s", moduleName, cfg.OutputRoot)
	return &catalogPublishHandler{publisher: publisher, outputRoot: cfg.OutputRoot}, nil
}

// publishOverallStatus is a bespoke top-level envelope (like pullStatus,
// there is no beckn.yaml definition for an internal trigger's own status)
// distinguishing "the publish call itself ran" from "it didn't" --
// individual catalog outcomes are reported in Results regardless.
type publishOverallStatus string

const (
	publishOverallCompleted publishOverallStatus = "COMPLETED"
	publishOverallFailed    publishOverallStatus = "FAILED"
)

// catalogProcessingStatus mirrors beckn.yaml's CatalogProcessingResult
// status enum ("ACCEPTED" | "REJECTED"); "PARTIAL" is not used here since
// this handler tracks whole-catalog outcomes, not intra-catalog ones.
type catalogProcessingStatus string

const (
	catalogAccepted catalogProcessingStatus = "ACCEPTED"
	catalogRejected catalogProcessingStatus = "REJECTED"
)

type catalogProcessingResult struct {
	CatalogID string                  `json:"catalogId"`
	Status    catalogProcessingStatus `json:"status"`
	Version   int                     `json:"version,omitempty"`
	Reason    string                  `json:"reason,omitempty"`
}

type publishResponse struct {
	Status  publishOverallStatus      `json:"status"`
	Results []catalogProcessingResult `json:"results,omitempty"`
	Error   *model.Error              `json:"error,omitempty"`
}

// ServeHTTP parses the DS-internal request body, loads prior state for the
// submitted catalogIds from cfg.outputRoot, runs the publish, persists the
// result back to cfg.outputRoot, and returns a per-catalog
// ACCEPTED/REJECTED body. A malformed request (bad body, no
// catalogs/retire at all) is a transport-level 400. Once Publish runs,
// every outcome -- including a fatal failure -- is reported as a 200 with
// status COMPLETED/FAILED in the body, matching catalogPullHandler's
// convention.
func (h *catalogPublishHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if len(req.Catalogs) == 0 && len(req.Retire) == 0 {
		http.Error(w, "catalogs or retire is required", http.StatusBadRequest)
		return
	}

	submissions := make([]definition.CatalogSubmission, 0, len(req.Catalogs))
	catalogIDs := make([]string, 0, len(req.Catalogs))
	for _, raw := range req.Catalogs {
		var probe struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &probe) // empty ID surfaces as a non-fatal PublishError below
		submissions = append(submissions, definition.CatalogSubmission{CatalogID: probe.ID, Catalog: raw})
		if probe.ID != "" {
			catalogIDs = append(catalogIDs, probe.ID)
		}
	}

	state, err := localstore.Load(h.outputRoot, catalogIDs)
	if err != nil {
		log.Errorf(r.Context(), err, "catalogPublish: loading prior state from %s", h.outputRoot)
		writePublishJSON(w, r, publishResponse{
			Status: publishOverallFailed,
			Error:  model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error()),
		})
		return
	}

	result, err := h.publisher.Publish(r.Context(), definition.PublishRequest{
		Catalogs:          submissions,
		PriorState:        state.PriorState,
		CarryForward:      state.CarryForward,
		PriorIndexVersion: state.PriorIndexVersion,
		Retire:            req.Retire,
		ForceBaseline:     req.ForceBaseline,
	})
	if err != nil {
		log.Errorf(r.Context(), err, "catalogPublish: publish failed")
		writePublishJSON(w, r, publishResponse{
			Status: publishOverallFailed,
			Error:  model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error()),
		})
		return
	}

	if err := localstore.Write(h.outputRoot, result); err != nil {
		log.Errorf(r.Context(), err, "catalogPublish: writing result to %s", h.outputRoot)
		writePublishJSON(w, r, publishResponse{
			Status: publishOverallFailed,
			Error:  model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error()),
		})
		return
	}

	results := make([]catalogProcessingResult, 0, len(result.Catalogs)+len(result.Errors)+len(req.Retire))
	for _, c := range result.Catalogs {
		results = append(results, catalogProcessingResult{CatalogID: c.CatalogID, Status: catalogAccepted, Version: c.Version})
	}
	for _, e := range result.Errors {
		log.Warnf(r.Context(), "catalogPublish: %s publish error at stage %s: %s", e.CatalogID, e.Stage, e.Reason)
		results = append(results, catalogProcessingResult{CatalogID: e.CatalogID, Status: catalogRejected, Reason: e.Reason})
	}
	for _, id := range req.Retire {
		results = append(results, catalogProcessingResult{CatalogID: id, Status: catalogAccepted, Reason: "retired"})
	}

	writePublishJSON(w, r, publishResponse{Status: publishOverallCompleted, Results: results})
}

func writePublishJSON(w http.ResponseWriter, r *http.Request, body publishResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Errorf(r.Context(), err, "catalogPublish handler: failed to encode response")
	}
}
