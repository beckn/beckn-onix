package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	// registryMetadata is the DeDi-native RegistryMetadataLookup used to
	// read this node's own registry record (read-only) and check whether
	// its meta.catalog_index_urls already links this publisher's catalog
	// index -- see checkRegistryLinksCatalogIndex. Replaces the earlier
	// node-manifest-based link check: the file spec's three-level
	// indirection (DeDi record -> node manifest -> catalog index) is
	// collapsed to two levels (DeDi record -> catalog index directly), so
	// this handler no longer needs ManifestLoader at all -- a raw registry
	// record read is enough, no manifest fetch/signature-verify/cache round
	// trip. nil when catalogPublisher.config.checkCatalogIndexLink isn't
	// "true" or when the configured Registry plugin doesn't implement
	// RegistryMetadataLookup.
	registryMetadata definition.RegistryMetadataLookup
	// subscriberID is the plain Beckn subscriberId (keyManager.config.
	// subscriberId) -- the KeyManager.Keyset lookup key, used for signing.
	subscriberID string
	// keyManager resolves subscriberID's current signing keyset -- used by
	// checkRegistryLinksCatalogIndex to build the registry self-lookup's
	// synthetic namespace/registry/recordName path
	// (subscriberID/wildcard/keyID) fresh on every check, the same pair
	// RegistryLookup.Lookup already resolves signing keys with during
	// ordinary transactions (verified directly against the DeDi registry
	// to resolve to the identical record a three-part path would).
	// Resolved per-check rather than cached once at construction so a key
	// rotation is picked up immediately, matching
	// catalogpublisher.Publish's own per-request Keyset resolution. Only
	// ever used when registryMetadata is configured.
	keyManager definition.KeyManager
}

// dediSubscriberWildcardRegistry is the DeDi registry service's special
// "search across all registries" value -- the same one
// dediregistry.dediAllRegistriesWildcard uses for RegistryLookup.Lookup's
// signing-key resolution during ordinary transactions. Duplicated here
// (rather than exporting dediregistry's unexported constant, or adding a
// dedicated subscriberID+keyID-shaped method to RegistryMetadataLookup) to
// keep this change's footprint small; giving RegistryMetadataLookup a
// proper subscriberID+keyID-shaped lookup method instead of this
// synthetic-path workaround is a separate, better-scoped follow-up.
const dediSubscriberWildcardRegistry = "subscribers.beckn.one"

// catalogIndexMetaKey is the DeDi registry record's meta field this handler
// checks: the direct link from a subscriber's own DeDi record to the
// catalog index(es) it publishes. Replaces the earlier three-level
// indirection (DeDi record -> node manifest -> catalog index) with a
// two-level one (DeDi record -> catalog index directly). Plural, an array
// of {url} objects, per NFH-014 §Schema Changes ("Beckn_subscriber
// (unmodified) + meta.catalog_index_urls") -- a node MAY host more than
// one catalog index, so this is never a single-value field.
const catalogIndexMetaKey = "catalog_index_urls"

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

	// Both optional, matching this handler's other plugins: unconfigured
	// means skipped, not an error, so existing deployments keep working
	// unchanged until a schemaValidator/checkPolicy block is added.
	// policyChecker is not passed a ManifestLoader (nil): this handler no
	// longer builds one at all (see registryMetadata below), so a
	// manifest-backed policy type (opapolicychecker's policyType
	// "manifest") cannot resolve here -- a known, accepted limitation of
	// this simplification, not an oversight.
	schemaValidator, err := loadPlugin(ctx, "SchemaValidator", cfg.Plugins.SchemaValidator, mgr.SchemaValidator)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: %w", moduleName, err)
	}
	policyChecker, err := loadPolicyChecker(ctx, mgr, nil, cfg.Plugins.PolicyChecker)
	if err != nil {
		return nil, fmt.Errorf("catalogPublish handler %s: %w", moduleName, err)
	}

	// checkCatalogIndexLink is this handler's own on/off toggle for the
	// catalog-index link check -- deliberately not cfg.Plugins.ManifestLoader:
	// no ManifestLoader plugin instance is ever built for this check.
	// registryMetadata instead reuses the already-loaded `registry` typed as
	// RegistryMetadataLookup (the same underlying dediregistry client, see
	// loadManifestLoader's identical type-assertion in stdHandler.go), since
	// a raw registry-record read is all checkRegistryLinksCatalogIndex needs
	// -- no manifest document fetch/verify/cache required.
	checkCatalogIndexLink, _ := strconv.ParseBool(publisherCfg.Config["checkCatalogIndexLink"])
	var registryMetadata definition.RegistryMetadataLookup
	subscriberID := publisherCfg.Config["subscriberId"]
	if checkCatalogIndexLink {
		var ok bool
		registryMetadata, ok = registry.(definition.RegistryMetadataLookup)
		if !ok {
			return nil, fmt.Errorf("catalogPublish handler %s: Registry plugin does not implement RegistryMetadataLookup (needed for the catalog-index link check)", moduleName)
		}
		// subscriberID is fixed for the handler's lifetime, so its shape can
		// be validated once here: the registry self-lookup's synthetic path
		// (subscriberID/wildcard/keyID, see checkRegistryLinksCatalogIndex)
		// requires exactly 3 non-empty slash-separated parts downstream
		// (dediregistry.LookupNode) -- a "/" inside subscriberID would
		// silently produce a malformed path on every single check instead
		// of failing loudly here at startup.
		if strings.Contains(subscriberID, "/") {
			return nil, fmt.Errorf("catalogPublish handler %s: subscriberId %q cannot contain \"/\" (needed to build the registry self-lookup's synthetic path)", moduleName, subscriberID)
		}
		// Resolve once here too, purely to fail fast at startup on a
		// missing/broken keyset -- the actual keyID used per-check is
		// re-resolved fresh in checkRegistryLinksCatalogIndex (see
		// keyManager's doc comment), so this result itself is
		// intentionally discarded.
		if _, err := km.Keyset(ctx, subscriberID); err != nil {
			return nil, fmt.Errorf("catalogPublish handler %s: resolving keyset for registry self-lookup (subscriberId=%s): %w", moduleName, subscriberID, err)
		}
	}

	log.Debugf(ctx, "catalogPublish handler %s initialized, outputRoot=%s", moduleName, cfg.OutputRoot)
	return &catalogPublishHandler{
		publisher:        publisher,
		outputRoot:       cfg.OutputRoot,
		schemaValidator:  schemaValidator,
		policyChecker:    policyChecker,
		registryMetadata: registryMetadata,
		subscriberID:     subscriberID,
		keyManager:       km,
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
	// Retired catalogIds need their prior state loaded too -- the
	// tombstone Publish builds for them carries forward their prior
	// CatalogType/NetworkIds/SchemaTypes and bumps their EntryVersion
	// (NFH-014 Appendix A, Example 4's retired entry still carries those
	// fields), not just retiredAt.
	loadIDs := append(append([]string{}, catalogIDs...), req.Retire...)

	log.Debugf(r.Context(), "catalogPublish: loading prior state for %v from %s", loadIDs, h.outputRoot)
	state, err := localstore.Load(h.outputRoot, loadIDs)
	if err != nil {
		log.Errorf(r.Context(), err, "catalogPublish: loading prior state from %s", h.outputRoot)
		writePublishJSON(w, r, publishResponse{
			Status:  publishOverallFailed,
			Message: &publishResponseMessage{Error: model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error())},
		})
		return
	}

	log.Debugf(r.Context(), "catalogPublish: calling Publish, carryForward=%d", len(state.CarryForward))
	result, err := h.publisher.Publish(r.Context(), definition.PublishRequest{
		Catalogs:      submissions,
		PriorState:    state.PriorState,
		CarryForward:  state.CarryForward,
		Retire:        req.Retire,
		ForceBaseline: req.ForceBaseline,
	})
	if err != nil {
		log.Errorf(r.Context(), err, "catalogPublish: publish failed")
		writePublishJSON(w, r, publishResponse{
			Status:  publishOverallFailed,
			Message: &publishResponseMessage{Error: model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error())},
		})
		return
	}

	log.Debugf(r.Context(), "catalogPublish: writing result to %s, %d catalog outcome(s), %d error(s)", h.outputRoot, len(result.Catalogs), len(result.Errors))
	if err := localstore.Write(h.outputRoot, result); err != nil {
		log.Errorf(r.Context(), err, "catalogPublish: writing result to %s", h.outputRoot)
		writePublishJSON(w, r, publishResponse{
			Status:  publishOverallFailed,
			Message: &publishResponseMessage{Error: model.NewCodedError("BIZ_PUBLISH_FAILED", err.Error())},
		})
		return
	}

	var warnings []string
	if h.registryMetadata != nil {
		if warning, err := h.checkRegistryLinksCatalogIndex(r.Context()); err != nil {
			log.Warnf(r.Context(), "catalogPublish: registry catalog-index link check failed: %v", err)
		} else if warning != "" {
			warnings = append(warnings, warning)
		}
	} else {
		log.Debug(r.Context(), "catalogPublish: catalog-index link check not configured, skipping")
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

// checkRegistryLinksCatalogIndex reads this node's own DeDi registry record
// (read-only, via RegistryMetadataLookup.LookupNode -- dediregistry has no
// write path, so there is nothing this handler could push even if it
// wanted to) and checks whether its meta.catalog_index_urls already
// includes this publisher's index URL (h.publisher.IndexURL()) -- plural,
// since a node MAY host more than one catalog index, so the match is
// membership in the array, not equality against a single value. Unlike the
// earlier node-manifest-based check, there is no local artifact to stage:
// getting a value into a DeDi record's meta is, and remains, an external,
// manual operator action (e.g. via DeDi's own registration tooling) -- a
// missing link is reported as a warning naming the meta key and the URL it
// should be added to. Returns ("", nil) when the link already matches.
func (h *catalogPublishHandler) checkRegistryLinksCatalogIndex(ctx context.Context) (string, error) {
	indexURL := h.publisher.IndexURL()
	log.Debugf(ctx, "catalogPublish: checking DeDi record for %s links catalog index %s", h.subscriberID, indexURL)

	keyset, err := h.keyManager.Keyset(ctx, h.subscriberID)
	if err != nil {
		return "", fmt.Errorf("resolving keyset for registry self-lookup (subscriberId=%s): %w", h.subscriberID, err)
	}
	keyID := keyset.UniqueKeyID
	if keyID == "" {
		return "", fmt.Errorf("keyset for subscriberId=%s has no keyId; cannot build registry self-lookup path", h.subscriberID)
	}
	if strings.Contains(keyID, "/") {
		return "", fmt.Errorf("keyId %q for subscriberId=%s cannot contain \"/\" (needed to build the registry self-lookup's synthetic path)", keyID, h.subscriberID)
	}

	syntheticNodeID := h.subscriberID + "/" + dediSubscriberWildcardRegistry + "/" + keyID
	record, err := h.registryMetadata.LookupNode(ctx, syntheticNodeID)
	if err != nil {
		return "", fmt.Errorf("looking up DeDi record for %s: %w", h.subscriberID, err)
	}

	for _, url := range record.MetaArrays[catalogIndexMetaKey] {
		if url == indexURL {
			log.Debugf(ctx, "catalogPublish: DeDi record for %s already links catalog index %s", h.subscriberID, indexURL)
			return "", nil
		}
	}

	return fmt.Sprintf(
		"DeDi record for %s does not link catalog index %s; add this URL to meta.%s on your DeDi record",
		h.subscriberID, indexURL, catalogIndexMetaKey,
	), nil
}

func writePublishJSON(w http.ResponseWriter, r *http.Request, body publishResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Errorf(r.Context(), err, "catalogPublish handler: failed to encode response")
	}
}
