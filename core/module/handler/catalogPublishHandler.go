package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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
// A catalog's NetworkIds come from a matching publishRequest.Message.
// PublishDirectives[].VisibleTo entry (matched by catalogId); AuthMethods
// isn't wired to any request field yet -- restricted catalogs with custom
// auth are a later phase, tracked in this package's README.
type catalogPublishHandler struct {
	publisher       definition.CatalogPublisher
	outputRoot      string
	schemaValidator definition.SchemaValidator
	policyChecker   definition.PolicyChecker
	manifestLoader  definition.ManifestLoader
	// subscriberID is the plain Beckn subscriberId (keyManager.config.
	// subscriberId) -- the KeyManager.Keyset lookup key, used for signing.
	subscriberID string
	// keyManager resolves subscriberID's current signing keyset -- used by
	// loadOwnNodeManifest to build the manifest self-lookup's synthetic
	// namespace/registry/recordName path (subscriberID/wildcard/keyID)
	// fresh on every check, the same pair RegistryLookup.Lookup already
	// resolves signing keys with during ordinary transactions (verified
	// directly against the DeDi registry to resolve to the identical
	// record a three-part path would). Resolved per-check rather than
	// cached once at construction so a key rotation is picked up
	// immediately, matching catalogpublisher.Publish's own per-request
	// Keyset resolution. Only ever used when manifestLoader is configured.
	keyManager definition.KeyManager
}

// dediSubscriberWildcardRegistry is the DeDi registry service's special
// "search across all registries" value -- the same one
// dediregistry.dediAllRegistriesWildcard uses for RegistryLookup.Lookup's
// signing-key resolution during ordinary transactions. Duplicated here
// (rather than exporting dediregistry's unexported constant, or adding a
// dedicated subscriberID+keyID-shaped method to ManifestLoader/
// RegistryMetadataLookup) to keep this change's footprint small; giving
// ManifestLoader a proper subscriberID+keyID-shaped lookup method instead
// of this synthetic-path workaround is a separate, better-scoped follow-up.
const dediSubscriberWildcardRegistry = "subscribers.beckn.one"

// publishDirective is one entry in publishRequest.Message.PublishDirectives,
// matched to a submitted catalog by CatalogID -- beckn.yaml's own
// CatalogPublishAction.publishDirectives shape (only the fields this
// handler currently acts on are modeled; catalogType/updateMode/
// resourceDirectives are part of the real schema too but not wired here
// yet). VisibleTo is the spec's name for what the catalog-index file
// itself (and CatalogSubmission) call NetworkIds -- restricts delivery of
// this catalog to the listed network participant ids; omitted means
// visible to all. CatalogType is required by the spec (unlike
// CatalogSubmission.CatalogType, which defaults to "REGULAR" when empty);
// callers must set it explicitly once schemaValidator is configured.
type publishDirective struct {
	CatalogID   string   `json:"catalogId"`
	VisibleTo   []string `json:"visibleTo,omitempty"`
	CatalogType string   `json:"catalogType,omitempty"`
}

// publishRequest is the DS-facing catalog/publish request body. It matches
// beckn.yaml's real CatalogPublishAction envelope shape (context/message,
// message.catalogs[]/publishDirectives[]) so schemaValidator/policyChecker
// can validate the raw request body directly -- no synthesized envelope
// needed (see ServeHTTP). Context carries only "action": this is still a
// DS-internal, unsigned, same-operator trigger, not a signed/routed
// network-facing Beckn action, so none of Context's other fields
// (bapId/bapUri/messageId/...) are meaningful here and the real spec
// leaves all of them optional. Retire/ForceBaseline are this handler's own
// additions, with no beckn.yaml equivalent -- kept as siblings of context/
// message rather than invented fields inside either.
type publishRequest struct {
	Context struct {
		Action string `json:"action"`
	} `json:"context"`
	Message struct {
		Catalogs          []json.RawMessage  `json:"catalogs"`
		PublishDirectives []publishDirective `json:"publishDirectives,omitempty"`
	} `json:"message"`
	Retire        []string `json:"retire,omitempty"`
	ForceBaseline bool     `json:"forceBaseline,omitempty"`
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
	publisherCfg, err := withKeyManagerSubscriberID(cfg.Plugins.CatalogPublisher, cfg.Plugins.KeyManager)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: %w", moduleName, err)
	}
	log.Debugf(ctx, "catalogPublish handler %s: resolved subscriberId=%s", moduleName, publisherCfg.Config["subscriberId"])
	publisher, err := mgr.CatalogPublisher(ctx, km, publisherCfg)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: failed to load catalogPublisher plugin (%s): %w", moduleName, cfg.Plugins.CatalogPublisher.ID, err)
	}

	// All optional, matching this handler's other plugins: unconfigured
	// means skipped, not an error, so existing deployments keep working
	// unchanged until a schemaValidator/checkPolicy/manifestLoader block is
	// added. manifestLoader is loaded before policyChecker (mirroring
	// stdHandler.go's own load order) and threaded into it, so a
	// manifest-backed policy (opapolicychecker's policyType "manifest") can
	// actually resolve here -- it also doubles as the manifestLoader used by
	// checkNodeManifestLinksIndex below.
	schemaValidator, err := loadPlugin(ctx, "SchemaValidator", cfg.Plugins.SchemaValidator, mgr.SchemaValidator)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: %w", moduleName, err)
	}
	manifestLoader, err := loadManifestLoader(ctx, mgr, cache, registry, cfg.Plugins.ManifestLoader)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: %w", moduleName, err)
	}
	policyChecker, err := loadPolicyChecker(ctx, mgr, manifestLoader, cfg.Plugins.PolicyChecker)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: %w", moduleName, err)
	}

	subscriberID := publisherCfg.Config["subscriberId"]
	if manifestLoader != nil {
		// subscriberID is fixed for the handler's lifetime, so its shape can
		// be validated once here: the manifest self-lookup's synthetic path
		// (subscriberID/wildcard/keyID, see loadOwnNodeManifest) requires
		// exactly 3 non-empty slash-separated parts downstream
		// (manifestloader.GetBySubscriberID/dediregistry.LookupNode) -- a
		// "/" inside subscriberID would silently produce a malformed path
		// on every single check instead of failing loudly here at startup.
		if strings.Contains(subscriberID, "/") {
			return nil, fmt.Errorf("catalogPublish handler %s: subscriberId %q cannot contain \"/\" (needed to build the manifest self-lookup's synthetic path)", moduleName, subscriberID)
		}
		// Resolve once here too, purely to fail fast at startup on a
		// missing/broken keyset -- the actual keyID used per-check is
		// re-resolved fresh in loadOwnNodeManifest (see keyManager's doc
		// comment), so this result itself is intentionally discarded.
		if _, err := km.Keyset(ctx, subscriberID); err != nil {
			return nil, fmt.Errorf("catalogPublish handler %s: resolving keyset for manifest self-lookup (subscriberId=%s): %w", moduleName, subscriberID, err)
		}
	}

	log.Debugf(ctx, "catalogPublish handler %s initialized, outputRoot=%s", moduleName, cfg.OutputRoot)
	return &catalogPublishHandler{
		publisher:       publisher,
		outputRoot:      cfg.OutputRoot,
		schemaValidator: schemaValidator,
		policyChecker:   policyChecker,
		manifestLoader:  manifestLoader,
		subscriberID:    subscriberID,
		keyManager:      km,
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading request body: %v", err), http.StatusBadRequest)
		return
	}
	var req publishRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if len(req.Message.Catalogs) == 0 && len(req.Retire) == 0 {
		http.Error(w, "message.catalogs or retire is required", http.StatusBadRequest)
		return
	}
	log.Debugf(r.Context(), "catalogPublish: received %d catalog(s), %d retire(s), forceBaseline=%v", len(req.Message.Catalogs), len(req.Retire), req.ForceBaseline)

	if len(req.Message.Catalogs) > 0 {
		if h.schemaValidator != nil {
			log.Debug(r.Context(), "catalogPublish: running schema validation")
			if err := h.schemaValidator.Validate(r.Context(), nil, body); err != nil {
				log.Debugf(r.Context(), "catalogPublish: schema validation failed: %v", err)
				http.Error(w, fmt.Sprintf("schema validation failed: %v", err), http.StatusBadRequest)
				return
			}
		}
		if h.policyChecker != nil {
			log.Debug(r.Context(), "catalogPublish: running policy check")
			if err := h.policyChecker.CheckPolicy(&model.StepContext{Context: r.Context(), Body: body}); err != nil {
				log.Debugf(r.Context(), "catalogPublish: policy check failed: %v", err)
				http.Error(w, fmt.Sprintf("policy check failed: %v", err), http.StatusBadRequest)
				return
			}
		}
	}

	directives := make(map[string]publishDirective, len(req.Message.PublishDirectives))
	for _, d := range req.Message.PublishDirectives {
		directives[d.CatalogID] = d
	}

	submissions := make([]definition.CatalogSubmission, 0, len(req.Message.Catalogs))
	catalogIDs := make([]string, 0, len(req.Message.Catalogs))
	for _, raw := range req.Message.Catalogs {
		var probe struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &probe) // empty ID surfaces as a non-fatal PublishError below
		d := directives[probe.ID]
		submissions = append(submissions, definition.CatalogSubmission{
			CatalogID:   probe.ID,
			Catalog:     raw,
			NetworkIds:  d.VisibleTo,
			CatalogType: d.CatalogType,
		})
		if probe.ID != "" {
			catalogIDs = append(catalogIDs, probe.ID)
		}
	}

	log.Debugf(r.Context(), "catalogPublish: loading prior state for %v from %s", catalogIDs, h.outputRoot)
	state, err := localstore.Load(h.outputRoot, catalogIDs)
	if err != nil {
		log.Errorf(r.Context(), err, "catalogPublish: loading prior state from %s", h.outputRoot)
		writePublishJSON(w, r, publishResponse{
			Status:  publishOverallFailed,
			Message: &publishResponseMessage{Error: model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error())},
		})
		return
	}

	log.Debugf(r.Context(), "catalogPublish: calling Publish, priorIndexVersion=%d, carryForward=%d", state.PriorIndexVersion, len(state.CarryForward))
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
			Status:  publishOverallFailed,
			Message: &publishResponseMessage{Error: model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error())},
		})
		return
	}

	log.Debugf(r.Context(), "catalogPublish: writing result to %s, indexVersion=%d, %d catalog outcome(s), %d error(s)", h.outputRoot, result.IndexVersion, len(result.Catalogs), len(result.Errors))
	if err := localstore.Write(h.outputRoot, result); err != nil {
		log.Errorf(r.Context(), err, "catalogPublish: writing result to %s", h.outputRoot)
		writePublishJSON(w, r, publishResponse{
			Status:  publishOverallFailed,
			Message: &publishResponseMessage{Error: model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error())},
		})
		return
	}

	var warnings []string
	if h.manifestLoader != nil {
		if warning, err := h.checkNodeManifestLinksIndex(r.Context()); err != nil {
			log.Warnf(r.Context(), "catalogPublish: node manifest link check failed: %v", err)
		} else if warning != "" {
			warnings = append(warnings, warning)
		}
	} else {
		log.Debug(r.Context(), "catalogPublish: manifestLoader not configured, skipping node manifest link check")
	}

	results := make([]catalogProcessingResult, 0, len(result.Catalogs)+len(result.Errors)+len(req.Retire))
	for _, c := range result.Catalogs {
		results = append(results, catalogProcessingResult{CatalogID: c.CatalogID, Status: catalogAccepted, Version: c.Version})
	}
	anyFatal := false
	for _, e := range result.Errors {
		if e.Fatal {
			anyFatal = true
			log.Errorf(r.Context(), fmt.Errorf("%s", e.Reason), "catalogPublish: %s fatal publish error at stage %s", e.CatalogID, e.Stage)
		} else {
			log.Warnf(r.Context(), "catalogPublish: %s publish error at stage %s: %s", e.CatalogID, e.Stage, e.Reason)
		}
		results = append(results, catalogProcessingResult{CatalogID: e.CatalogID, Status: catalogRejected, Reason: e.Reason})
	}
	submittedIDs := make(map[string]bool, len(catalogIDs))
	for _, id := range catalogIDs {
		submittedIDs[id] = true
	}
	for _, id := range req.Retire {
		if submittedIDs[id] {
			// Publish's own rule: submitting and retiring the same
			// catalogId in one call means the submission wins, so no
			// tombstone was actually written -- reporting "retired" here
			// too would falsely tell the caller this catalog was retired.
			continue
		}
		results = append(results, catalogProcessingResult{CatalogID: id, Status: catalogAccepted, Reason: "retired"})
	}

	// A Fatal PublishError signals something worse than one catalog being
	// invalid -- e.g. a failure likely affecting every other catalog in the
	// same call too (see definition.PublishError.Fatal) -- so the overall
	// status must not read as a routine COMPLETED alongside it.
	status := publishOverallCompleted
	if anyFatal {
		status = publishOverallFailed
	}

	log.Debugf(r.Context(), "catalogPublish: completed, %d result(s), %d warning(s), anyFatal=%v", len(results), len(warnings), anyFatal)
	writePublishJSON(w, r, publishResponse{Status: status, Results: results, Warnings: warnings})
}

// checkNodeManifestLinksIndex reads this node's manifest (read-only, via
// ManifestLoader/dediregistry -- the real manifest is never written to, see
// localstore.Write) and checks whether catalog.catalogIndexes already
// declares this publisher's index URL (h.publisher.IndexURL()). If not, it
// stages a proposed updated manifest locally (localstore.
// WriteStagedNodeManifest, under outputRoot/index/) for the operator to
// review and push to DeDi themselves, and returns a warning describing
// what's missing. Returns ("", nil) when the link already exists.
func (h *catalogPublishHandler) checkNodeManifestLinksIndex(ctx context.Context) (string, error) {
	indexURL := h.publisher.IndexURL()
	log.Debugf(ctx, "catalogPublish: checking node manifest for %s links catalog index %s", h.subscriberID, indexURL)

	nm, err := h.loadOwnNodeManifest(ctx)
	if err != nil {
		// Deliberately NOT treated as "no manifest yet, start a skeleton"
		// -- that would silently mask real failures (a lookup/parse
		// failure here means the check is inconclusive, not that the
		// link is missing).
		return "", fmt.Errorf("fetching/parsing node manifest for %s: %w", h.subscriberID, err)
	}

	for _, entry := range nm.Catalog.CatalogIndexes {
		if entry.URL == indexURL {
			log.Debugf(ctx, "catalogPublish: node manifest for %s already declares catalog index %s", h.subscriberID, indexURL)
			return "", nil
		}
	}

	nm.Catalog.CatalogIndexes = append(nm.Catalog.CatalogIndexes, model.CatalogIndexEntry{URL: indexURL})
	if err := localstore.WriteStagedNodeManifest(h.outputRoot, nm); err != nil {
		return "", fmt.Errorf("staging updated node manifest: %w", err)
	}
	log.Debugf(ctx, "catalogPublish: staged updated node manifest at %s", localstore.StagedNodeManifestPath(h.outputRoot))

	return fmt.Sprintf(
		"node manifest for %s does not declare catalog index %s; a proposed update was staged at %s -- review and publish it to DeDi yourself",
		h.subscriberID, indexURL, localstore.StagedNodeManifestPath(h.outputRoot),
	), nil
}

// noManifestPublishedErrMsg matches manifestloader.ErrNoManifestPublished's
// message. Compared by string, not errors.Is/sentinel identity: manifestLoader
// is loaded as a plugin.Config-driven definition.ManifestLoader (potentially
// a separately-compiled Go plugin .so, see PluginManager), so a sentinel
// error value from a statically-imported copy of that package is not
// guaranteed to compare equal to one returned across that boundary.
const noManifestPublishedErrMsg = "subscriber has not published a node manifest"

// loadOwnNodeManifest fetches and parses this node's own manifest via
// GetBySubscriberID, addressed by a synthetic
// subscriberID/dediSubscriberWildcardRegistry/keyID path built from
// subscriberID+keyID -- the same identifiers RegistryLookup.Lookup already
// resolves signing keys with during ordinary transactions -- rather than a
// hand-configured three-part DeDi path. Verified directly against the DeDi
// registry that this resolves to the identical record a real
// namespace/registry/recordName lookup would. Only the specific "subscriber
// has never published a manifest at all" case is treated as non-fatal -- a
// fresh skeleton (SubscriberID/ManifestVersion/ManifestType only)
// checkNodeManifestLinksIndex can add a catalogIndexes entry to and stage
// from scratch. Any other failure (network error, unparseable content) is a
// real error, surfaced to the caller instead of being mistaken for "no
// manifest" -- silently swallowing it here previously produced a false "not
// declared" warning even when the real manifest already listed this index,
// whenever the underlying fetch failed for an unrelated reason.
func (h *catalogPublishHandler) loadOwnNodeManifest(ctx context.Context) (*model.NodeManifest, error) {
	keyset, err := h.keyManager.Keyset(ctx, h.subscriberID)
	if err != nil {
		return nil, fmt.Errorf("resolving keyset for manifest self-lookup (subscriberId=%s): %w", h.subscriberID, err)
	}
	keyID := keyset.UniqueKeyID
	if keyID == "" {
		return nil, fmt.Errorf("keyset for subscriberId=%s has no keyId; cannot build manifest self-lookup path", h.subscriberID)
	}
	if strings.Contains(keyID, "/") {
		return nil, fmt.Errorf("keyId %q for subscriberId=%s cannot contain \"/\" (needed to build the manifest self-lookup's synthetic path)", keyID, h.subscriberID)
	}

	syntheticNodeID := h.subscriberID + "/" + dediSubscriberWildcardRegistry + "/" + keyID
	doc, err := h.manifestLoader.GetBySubscriberID(ctx, syntheticNodeID)
	if err != nil {
		if strings.Contains(err.Error(), noManifestPublishedErrMsg) {
			log.Debugf(ctx, "catalogPublish: no node manifest published yet for %s, starting from a fresh skeleton", h.subscriberID)
			return &model.NodeManifest{
				ManifestVersion: "1.0",
				ManifestType:    model.NodeManifestType,
				SubscriberID:    h.subscriberID,
			}, nil
		}
		return nil, err
	}
	return model.ParseNodeManifest(doc.Content)
}

func writePublishJSON(w http.ResponseWriter, r *http.Request, body publishResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Errorf(r.Context(), err, "catalogPublish handler: failed to encode response")
	}
}
