package catalogpublisher

import (
	"bytes"
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

// NewHandler builds the catalogPublish endpoint: an http.Handler that
// serves a DS-internal, unsigned catalog/publish trigger by decoding the
// request via the configured CatalogPublisher plugin, invoking its Publish
// synchronously, and rendering a per-catalog ACCEPTED/REJECTED body. It
// borrows that vocabulary from beckn.yaml's
// CatalogPublishAction/OnCatalogPublishAction, but as one synchronous
// response rather than an Ack-now/on_publish-later pair: this is a
// DS-internal trigger, not a network-facing Beckn action, so there is no
// validateSign/addRoute/signAck pipeline and no async callback.
//
// This constructor lives in the catalogpublisher package itself -- not in
// core/module/handler -- precisely because it is catalog/publish-specific:
// all wire-shape decoding, validation, storage, and the registry
// catalog-index link check live in this plugin package too (decode.go,
// registrylink.go, storeadapter.go). The generic, domain-free part is
// handler.EndpointHandler[Req,Resp] (core/module/handler/endpointhandler.go),
// which this function merely instantiates by wiring
// Decode/Execute/Encode closures and layering schemaValidator/
// policyChecker -- generic plugins unrelated to CatalogPublisher's own
// contract -- onto the raw request body around the plugin's own
// DecodeRequest.
//
// A catalog's NetworkIds come from a matching publishRequest.Message.
// PublishDirectives[].VisibleTo entry (matched by catalogId, decoded inside
// this plugin); AuthMethods isn't wired to any request field yet --
// restricted catalogs with custom auth are a later phase, tracked in this
// package's README.
func NewHandler(ctx context.Context, mgr handler.PluginManager, cfg *handler.Config, moduleName string) (http.Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("catalogPublish handler %s: config is required", moduleName)
	}
	// cfg.OutputRoot is no longer read here: storage is now supplied to the
	// catalogPublisher plugin via its own CatalogBlobStore plugin config
	// (cfg.Plugins.CatalogBlobStore) instead. See Config.OutputRoot's own
	// doc comment for why the field itself stays on Config unchanged.

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

	km, err := handler.LoadKeyManager(ctx, mgr, registry, cfg.Plugins.KeyManager)
	if err != nil {
		return nil, err
	}

	if cfg.Plugins.CatalogPublisher == nil {
		return nil, fmt.Errorf("catalogPublish handler %s: catalogPublisher plugin not configured", moduleName)
	}
	publisherCfg, err := withKeyManagerSubscriberID(cfg.Plugins.CatalogPublisher, cfg.Plugins.KeyManager)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: %w", moduleName, err)
	}
	log.Debugf(ctx, "catalogPublish handler %s: resolved subscriberId=%s", moduleName, publisherCfg.Config["subscriberId"])

	if cfg.Plugins.CatalogBlobStore == nil {
		return nil, fmt.Errorf("catalogPublish handler %s: catalogBlobStore plugin not configured", moduleName)
	}
	blobStore, err := mgr.CatalogBlobStore(ctx, cfg.Plugins.CatalogBlobStore)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: failed to load catalogBlobStore plugin (%s): %w", moduleName, cfg.Plugins.CatalogBlobStore.ID, err)
	}

	// mgr.CatalogPublisher narrows registry to RegistryMetadataLookup itself
	// (as dediregistry implements it), the same way Manager.Crawler does --
	// whether it's actually required is validated inside the plugin's own
	// New, which is the only place that already knows the
	// "checkCatalogIndexLink" config key.
	publisher, err := mgr.CatalogPublisher(ctx, km, blobStore, registry, publisherCfg)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: failed to load catalogPublisher plugin (%s): %w", moduleName, cfg.Plugins.CatalogPublisher.ID, err)
	}

	// Both optional, matching this handler's other plugins: unconfigured
	// means skipped, not an error, so existing deployments keep working
	// unchanged until a schemaValidator/checkPolicy block is added.
	// policyChecker is not passed a ManifestLoader (nil): this handler
	// builds no manifest-loading infrastructure at all -- a manifest-backed
	// policy type (opapolicychecker's policyType "manifest") cannot resolve
	// here, a known, accepted limitation, not an oversight.
	schemaValidator, err := handler.LoadPlugin(ctx, "SchemaValidator", cfg.Plugins.SchemaValidator, mgr.SchemaValidator)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: %w", moduleName, err)
	}
	policyChecker, err := handler.LoadPolicyChecker(ctx, mgr, nil, cfg.Plugins.PolicyChecker)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: %w", moduleName, err)
	}

	decode := func(ctx context.Context, r *http.Request) (definition.PublishRequest, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return definition.PublishRequest{}, fmt.Errorf("reading request body: %w", err)
		}
		// publisher.DecodeRequest below does its own io.ReadAll(r.Body) --
		// buffer and replace r.Body so it can still read the same bytes
		// this closure already consumed for schemaValidator/policyChecker.
		r.Body = io.NopCloser(bytes.NewReader(body))

		req, err := publisher.DecodeRequest(ctx, r)
		if err != nil {
			return definition.PublishRequest{}, err
		}
		forceBaselineCount := 0
		for _, c := range req.Catalogs {
			if c.ForceBaseline {
				forceBaselineCount++
			}
		}
		log.Debugf(ctx, "catalogPublish: received %d catalog(s), %d retire(s), %d forceBaseline", len(req.Catalogs), len(req.Retire), forceBaselineCount)

		if len(req.Catalogs) > 0 {
			if schemaValidator != nil {
				log.Debug(ctx, "catalogPublish: running schema validation")
				if err := schemaValidator.Validate(ctx, nil, body); err != nil {
					log.Debugf(ctx, "catalogPublish: schema validation failed: %v", err)
					return definition.PublishRequest{}, fmt.Errorf("schema validation failed: %w", err)
				}
			}
			if policyChecker != nil {
				log.Debug(ctx, "catalogPublish: running policy check")
				if err := policyChecker.CheckPolicy(&model.StepContext{Context: ctx, Body: body}); err != nil {
					log.Debugf(ctx, "catalogPublish: policy check failed: %v", err)
					return definition.PublishRequest{}, fmt.Errorf("policy check failed: %w", err)
				}
			}
		}

		return req, nil
	}

	encode := func(w http.ResponseWriter, r *http.Request, req definition.PublishRequest, resp definition.PublishResult, err error) {
		if err != nil {
			log.Errorf(r.Context(), err, "catalogPublish: publish failed")
			writePublishJSON(w, r, publishResponse{
				Status:  publishOverallFailed,
				Message: &publishResponseMessage{Error: model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error())},
			})
			return
		}

		results := make([]catalogProcessingResult, 0, len(resp.Catalogs)+len(resp.Errors)+len(req.Retire))
		anyFatal := false
		// rejected is built before the Catalogs/retire loops below so
		// neither reports a catalogId as ACCEPTED/retired that also failed
		// -- e.g. verify.go's post-write check can fail a catalog that
		// Publish itself already produced a (now-unreachable/unverifiable)
		// CatalogPublishOutcome for, and a retirement whose tombstone
		// doesn't survive re-fetch. Errors is authoritative over both.
		rejected := make(map[string]bool, len(resp.Errors))
		for _, e := range resp.Errors {
			rejected[e.CatalogID] = true
			if e.Fatal {
				anyFatal = true
				log.Errorf(r.Context(), fmt.Errorf("%s", e.Reason), "catalogPublish: %s fatal publish error at stage %s", e.CatalogID, e.Stage)
			} else {
				log.Warnf(r.Context(), "catalogPublish: %s publish error at stage %s: %s", e.CatalogID, e.Stage, e.Reason)
			}
			results = append(results, catalogProcessingResult{CatalogID: e.CatalogID, Status: catalogRejected, Reason: e.Reason})
		}
		for _, c := range resp.Catalogs {
			if rejected[c.CatalogID] {
				continue
			}
			results = append(results, catalogProcessingResult{CatalogID: c.CatalogID, Status: catalogAccepted, Version: c.Version})
		}

		submittedIDs := make(map[string]bool, len(req.Catalogs))
		for _, id := range nonEmptyCatalogIDs(req.Catalogs) {
			submittedIDs[id] = true
		}
		for _, id := range req.Retire {
			if submittedIDs[id] || rejected[id] {
				// Publish's own rule: submitting and retiring the same
				// catalogId in one call means the submission wins, so no
				// tombstone was actually written -- reporting "retired"
				// here too would falsely tell the caller this catalog was
				// retired. Same reasoning for rejected: already reported
				// REJECTED above, so not also "retired".
				continue
			}
			results = append(results, catalogProcessingResult{CatalogID: id, Status: catalogAccepted, Reason: "retired"})
		}

		// A Fatal PublishError signals something worse than one catalog
		// being invalid -- e.g. a failure likely affecting every other
		// catalog in the same call too (see definition.PublishError.Fatal)
		// -- so the overall status must not read as a routine COMPLETED
		// alongside it.
		status := publishOverallCompleted
		if anyFatal {
			status = publishOverallFailed
		}

		log.Debugf(r.Context(), "catalogPublish: completed, %d result(s), %d warning(s), anyFatal=%v", len(results), len(resp.Warnings), anyFatal)
		writePublishJSON(w, r, publishResponse{Status: status, Results: results, Warnings: resp.Warnings})
	}

	log.Debugf(ctx, "catalogPublish handler %s initialized", moduleName)
	return &handler.EndpointHandler[definition.PublishRequest, definition.PublishResult]{
		Decode:  decode,
		Execute: publisher.Publish,
		Encode:  encode,
	}, nil
}

// withKeyManagerSubscriberID returns catalogPublisherCfg with its
// "subscriberId" config entry set from keyManagerCfg's own "subscriberId" --
// the single source of truth for which keyset CatalogPublisher.Publish
// loads via KeyManager.Keyset. This removes the need to declare
// subscriberId a second time under catalogPublisher's own config; an
// explicit value there (for the rare case it legitimately differs) is
// still honored and left untouched.
func withKeyManagerSubscriberID(catalogPublisherCfg, keyManagerCfg *plugin.Config) (*plugin.Config, error) {
	if catalogPublisherCfg.Config != nil && catalogPublisherCfg.Config["subscriberId"] != "" {
		return catalogPublisherCfg, nil
	}
	if keyManagerCfg == nil {
		return nil, fmt.Errorf("keyManager plugin not configured (needed to derive catalogPublisher's subscriberId)")
	}
	subscriberID := keyManagerCfg.Config["subscriberId"]
	if subscriberID == "" {
		return nil, fmt.Errorf("keyManager plugin config missing subscriberId (needed to derive catalogPublisher's subscriberId)")
	}

	merged := make(map[string]string, len(catalogPublisherCfg.Config)+1)
	for k, v := range catalogPublisherCfg.Config {
		merged[k] = v
	}
	merged["subscriberId"] = subscriberID
	return &plugin.Config{ID: catalogPublisherCfg.ID, Config: merged}, nil
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

// publishResponseMessage nests Error under "message" the same way
// beckn.yaml's own NACK envelope does (model.Message{Status, Error} in
// pkg/model/model.go, written by core/module/handler/responsestep.go for
// every std/network-facing action) -- {code, message, details} on model.
// Error itself already matches beckn.yaml's Error schema field-for-field;
// this only fixes where it's nested, not its own shape.
type publishResponseMessage struct {
	Error *model.Error `json:"error,omitempty"`
}

type publishResponse struct {
	Status   publishOverallStatus      `json:"status"`
	Results  []catalogProcessingResult `json:"results,omitempty"`
	Warnings []string                  `json:"warnings,omitempty"`
	Message  *publishResponseMessage   `json:"message,omitempty"`
}

func writePublishJSON(w http.ResponseWriter, r *http.Request, body publishResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Errorf(r.Context(), err, "catalogPublish handler: failed to encode response")
	}
}
