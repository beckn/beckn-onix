// Package schemaversionmediator implements the SchemaVersionMediator plugin.
// It walks inbound Beckn payloads, checks schema object compatibility against
// the local node manifest, and dispatches translation for incompatible objects.
package schemaversionmediator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/jsonata-go/jsonata"
)

// PolicyAction defines what the mediator does when schema incompatibility is
// detected or when a translation attempt fails.
type PolicyAction string

const (
	// PolicyActionReject rejects the request immediately with a NACK.
	PolicyActionReject PolicyAction = "reject"
	// PolicyActionTranslate attempts translation for each incompatible schema
	// object. On failure the OnFailure policy applies.
	PolicyActionTranslate PolicyAction = "translate"
	// PolicyActionPassThrough forwards the request as-is with a structured log
	// signal. Valid only as onFailure — used when no artifact is published yet
	// and the operator accepts the risk of forwarding an untranslated payload.
	PolicyActionPassThrough PolicyAction = "passThrough"
)

// TranslationPolicy governs mediator behaviour when schema incompatibilities
// are found. It is loaded from the plugin config map and applied by Mediate.
//
// Action is evaluated immediately after CheckCompatibility returns incompatible
// objects — before any translation is attempted. OnFailure is only consulted
// when Action is PolicyActionTranslate and the translation attempt fails (no
// artifact found, or execution error).
type TranslationPolicy struct {
	Action    PolicyAction
	OnFailure PolicyAction
}

// defaultPolicy is the sentinel default when the operator has not configured a policy.
// translate/reject is the safest default: attempt translation, hard-fail if
// it cannot be completed, never silently forward an untranslated payload.
// Declared as a value (not a pointer) to prevent accidental mutation.
var defaultPolicy = TranslationPolicy{
	Action:    PolicyActionTranslate,
	OnFailure: PolicyActionReject,
}

// loadTranslationPolicy reads the mediator policy from the plugin config map.
// Config keys: "action" and "onFailure". Both are optional — absent keys fall
// back to the default policy (translate/reject).
//
// Valid values for action:    reject | translate
// Valid values for onFailure: reject | passThrough (only validated when action=translate;
// ignored otherwise since no translation is ever attempted)
// Setting onFailure to "translate" is not permitted — it would cause a loop.
// "passThrough" is not a valid action — it may only appear as onFailure.
func loadTranslationPolicy(config map[string]string) (*TranslationPolicy, error) {
	p := &TranslationPolicy{
		Action:    defaultPolicy.Action,
		OnFailure: defaultPolicy.OnFailure,
	}

	if raw, ok := config["action"]; ok {
		switch PolicyAction(raw) {
		case PolicyActionReject, PolicyActionTranslate:
			p.Action = PolicyAction(raw)
		case PolicyActionPassThrough:
			return nil, fmt.Errorf("schemaversionmediator: action cannot be %q — passThrough is only valid as onFailure", raw)
		default:
			return nil, fmt.Errorf("schemaversionmediator: invalid action %q: must be reject or translate", raw)
		}
	}

	// onFailure is only meaningful when action=translate. Validate it only in
	// that case — silently ignoring it for other actions avoids surprising errors
	// when operators carry over a stale onFailure key alongside action=reject.
	if p.Action == PolicyActionTranslate {
		if raw, ok := config["onFailure"]; ok {
			switch PolicyAction(raw) {
			case PolicyActionReject, PolicyActionPassThrough:
				p.OnFailure = PolicyAction(raw)
			case PolicyActionTranslate:
				return nil, fmt.Errorf("schemaversionmediator: onFailure cannot be %q — would cause a translation loop", raw)
			default:
				return nil, fmt.Errorf("schemaversionmediator: invalid onFailure %q: must be reject or passThrough", raw)
			}
		}
	}

	return p, nil
}

// ErrNoManifest is returned by CheckCompatibility when the node manifest is nil.
// The caller should log a warning and skip mediation — translation targets cannot
// be determined without a manifest, but the absence of one is not a hard failure.
var ErrNoManifest = errors.New("schemaversionmediator: node manifest unavailable, skipping mediation")

// SchemaObjectRef is a schema object extracted from a payload, extended with
// the JSONata path to the node that declared it. The path flows through to
// TranslationNeeded and MappingEntry for the caller's logging and debugging use;
// it is not interpreted by ComposeExpression.
type SchemaObjectRef struct {
	model.SchemaObject
	// JSONataPath is the JSONata dot-notation path from the payload root to this
	// node, e.g. "$.message.order" or "$.message.order.fulfillments[0]".
	// The root node itself has path "$" (JSONata root reference).
	JSONataPath string
}

// TranslationNeeded describes a single schema object from the payload that
// the local node cannot handle as-is and requires translation.
//
// From is the schema object as declared in the inbound payload.
// To is the schema object the local node supports for the same Type.
// To is nil when the Type is entirely absent from the local node manifest —
// an unknown schema whose handling is governed by the data-loss policy.
// JSONataPath is the path to the node in the payload — forwarded from SchemaObjectRef.
type TranslationNeeded struct {
	From        model.SchemaObject
	To          *model.SchemaObject
	JSONataPath string
}

// WalkPayload recursively traverses a JSON payload and returns all schema
// objects declared via JSON-LD "@context" and "@type" fields, each annotated
// with the JSONata path to its location in the tree. The walk is depth-first
// and collects every qualifying node regardless of nesting level, including
// both a parent node and its nested children when both carry "@context"/"@type"
// declarations — each is an independent schema contract. The payload is not modified.
func WalkPayload(payload []byte) ([]SchemaObjectRef, error) {
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("schemaversionmediator: walk payload: %w", err)
	}
	var results []SchemaObjectRef
	walkNode(root, "$", &results)
	return results, nil
}

// walkNode is the recursive descent worker for WalkPayload.
// path is the JSONata path of the current node from the payload root.
// When a map node carries both "@context" and "@type" it is collected, then
// the walk continues into its children.
func walkNode(node any, nodePath string, results *[]SchemaObjectRef) {
	switch v := node.(type) {
	case map[string]any:
		if contextURL, ok := stringField(v, "@context"); ok {
			if typ, ok := stringField(v, "@type"); ok {
				*results = append(*results, SchemaObjectRef{
					SchemaObject: model.SchemaObject{
						ContextURL: contextURL,
						Type:       typ,
					},
					JSONataPath: nodePath,
				})
			}
		}
		for key, child := range v {
			if key == "@context" || key == "@type" {
				continue
			}
			childPath := nodePath + "." + key
			walkNode(child, childPath, results)
		}
	case []any:
		for i, item := range v {
			childPath := fmt.Sprintf("%s[%d]", nodePath, i)
			walkNode(item, childPath, results)
		}
	}
}

// stringField returns the string value of key in m, reporting whether it was
// present and non-empty.
func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

// --- Translation map manager ---

// ErrArtifactNotFound is returned by fetchArtifact when no translation artifact
// exists at the derived URL (HTTP 404). Distinct from transient network errors
// so the mediation loop can apply OnFailure policy for "map doesn't exist yet"
// vs "registry unreachable".
var ErrArtifactNotFound = errors.New("schemaversionmediator: translation artifact not found")

const (
	defaultFetchTimeout    = 30 * time.Second
	defaultPositiveTTL     = 24 * time.Hour
	defaultNegativeTTL     = 5 * time.Minute
	defaultMaxCacheEntries = 500
	maxArtifactBodySize    = 1 << 20 // 1 MiB
)

// httpClientFunc is a package-level variable so tests can inject a custom client
// without modifying the production code path. Matches manifestloader's pattern.
var httpClientFunc = func(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// TranslationArtifact holds a fetched translation artifact and the Content-Type
// returned by the server. ContentType determines which Translator implementation
// the mediation loop dispatches to (e.g. "application/jsonata").
type TranslationArtifact struct {
	Content     []byte
	ContentType string
}

// artifactCacheEntry is a single cache slot.
// artifact == nil marks a negative cache entry (404 remembered).
type artifactCacheEntry struct {
	artifact  *TranslationArtifact
	fetchedAt time.Time
}

// artifactCache is an in-memory store for translation artifacts with positive
// and negative TTLs and a bounded size. Entries are evicted FIFO when full.
type artifactCache struct {
	mu          sync.RWMutex
	entries     map[string]*artifactCacheEntry
	keys        []string // insertion order for FIFO eviction
	positiveTTL time.Duration
	negativeTTL time.Duration
	maxEntries  int
}

func newArtifactCache(positiveTTL, negativeTTL time.Duration, maxEntries int) *artifactCache {
	return &artifactCache{
		entries:     make(map[string]*artifactCacheEntry, maxEntries),
		positiveTTL: positiveTTL,
		negativeTTL: negativeTTL,
		maxEntries:  maxEntries,
	}
}

// get returns the cached artifact and whether a valid cache entry was found.
// artifact == nil with found == true means a valid negative cache entry (404).
// Expired entries are evicted from the map on read to prevent stale accumulation.
func (c *artifactCache) get(key string) (artifact *TranslationArtifact, found bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	ttl := c.positiveTTL
	if entry.artifact == nil {
		ttl = c.negativeTTL
	}
	if time.Since(entry.fetchedAt) > ttl {
		c.mu.Lock()
		// Re-check under write lock: another goroutine may have refreshed the entry.
		if e, ok := c.entries[key]; ok && time.Since(e.fetchedAt) > ttl {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, false
	}
	return entry.artifact, true
}

// set stores an artifact (or nil for a negative cache entry) under key.
// Evicts the oldest entry when the cache is at capacity.
func (c *artifactCache) set(key string, artifact *TranslationArtifact) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		if len(c.keys) >= c.maxEntries {
			oldest := c.keys[0]
			c.keys = c.keys[1:]
			delete(c.entries, oldest)
		}
		c.keys = append(c.keys, key)
	}
	c.entries[key] = &artifactCacheEntry{artifact: artifact, fetchedAt: time.Now()}
}

// defaultMaxExprCacheEntries caps the compiled-expression cache. Expressions are
// deterministic and never expire, but the cap prevents unbounded growth on nodes
// that encounter an unusually large number of distinct schema version pairs.
// When the cap is reached, new expressions are compiled and returned but not cached.
const defaultMaxExprCacheEntries = 200

// exprCache stores compiled JSONata expressions keyed by the raw expression string.
// Entries never expire — expressions are deterministic and there are very few
// unique ones in practice (bounded by the set of schema version pairs deployed
// on a given node). See defaultMaxExprCacheEntries for the size cap.
type exprCache struct {
	mu      sync.RWMutex
	entries map[string]jsonata.Expression
	max     int
}

func newExprCache() *exprCache {
	return &exprCache{entries: make(map[string]jsonata.Expression), max: defaultMaxExprCacheEntries}
}

// mediator is the runtime state for the SchemaVersionMediator plugin.
type mediator struct {
	policy          TranslationPolicy
	loader          definition.ManifestLoader
	httpClient      *http.Client
	cache           *artifactCache
	jsonataInstance jsonata.JSONataInstance
	exprs           *exprCache
	notOnboarded    bool // set at New() when local manifest is absent or has no schemaObjects
}

// New is the package-level constructor used by the plugin entrypoint.
func New(ctx context.Context, loader definition.ManifestLoader, cfg map[string]string) (definition.SchemaVersionMediator, func() error, error) {
	return (&provider{}).New(ctx, loader, cfg)
}

// provider is the factory for mediator instances. It implements
// definition.SchemaVersionMediatorProvider.
type provider struct{}

// New constructs a mediator, validates config, and performs the cold-start
// check. If the local node manifest is absent or carries no schemaObjects the
// mediator's notOnboarded flag is set; Mediate will reject every inbound
// request until the manifest is published and the adapter is restarted.
func (p *provider) New(ctx context.Context, loader definition.ManifestLoader, cfg map[string]string) (definition.SchemaVersionMediator, func() error, error) {
	policy, err := loadTranslationPolicy(cfg)
	if err != nil {
		return nil, nil, err
	}

	fetchTimeout, positiveTTL, negativeTTL, maxEntries, err := loadMapManagerConfig(cfg)
	if err != nil {
		return nil, nil, err
	}

	instance, err := jsonata.OpenLatest()
	if err != nil {
		return nil, nil, fmt.Errorf("schemaversionmediator: open jsonata: %w", err)
	}

	m := &mediator{
		policy:          *policy,
		loader:          loader,
		httpClient:      httpClientFunc(fetchTimeout),
		cache:           newArtifactCache(positiveTTL, negativeTTL, maxEntries),
		jsonataInstance: instance,
		exprs:           newExprCache(),
	}

	// Cold-start check: attempt to load the local node manifest. If it is
	// absent or has no schemaObjects, mark as not onboarded so Mediate rejects
	// every inbound call with a clear error rather than silently pass-through.
	//
	// nodeId is not an operator-facing config field. The handler injects the
	// handler-level subscriberId here before calling New so that the cold-start
	// lookup can run at boot time, before any StepContext is available.
	nodeID := cfg["nodeId"]
	if nodeID == "" {
		m.notOnboarded = true
	} else {
		doc, err := loader.GetBySubscriberID(ctx, nodeID)
		if err != nil || doc == nil {
			m.notOnboarded = true
		} else {
			nm, err := parseNodeManifest(doc)
			if err != nil || len(nm.Schema.SchemaObjects) == 0 {
				m.notOnboarded = true
			}
		}
	}

	return m, func() error { return nil }, nil
}

// Mediate runs the full inbound schema version mediation sequence on ctx.Body.
// It is direction-agnostic: the caller (BAPCaller / BPPCaller handler) invokes
// it for inbound payloads; the response path uses RunOnResponse (not yet implemented).
func (m *mediator) Mediate(ctx *model.StepContext) error {
	if m.notOnboarded {
		return &MediationError{
			Code:    "subscriberNotOnboarded",
			Message: "Local node manifest is missing or has no schemaObjects. Publish your manifest to DeDi before going live.",
		}
	}

	networkID := extractNetworkID(ctx.Body)
	counterpartyID, _ := ctx.Value(model.ContextKeyRemoteID).(string)

	// No policy match → pass through unchanged.
	if networkID == "" || counterpartyID == "" {
		return nil
	}

	// Fetch counterparty manifest. Absent manifest drives onFailure branch.
	counterpartyManifest, err := m.fetchCounterpartyManifest(ctx, counterpartyID)
	if err != nil {
		return m.applyOnFailure(fmt.Errorf("schemaversionmediator: counterparty manifest unavailable for %q: %w", counterpartyID, err))
	}

	refs, err := WalkPayload(ctx.Body)
	if err != nil {
		return fmt.Errorf("schemaversionmediator: walk payload: %w", err)
	}

	needs, err := CheckCompatibility(refs, counterpartyManifest)
	if err != nil {
		return err
	}
	if len(needs) == 0 {
		return nil // fully compatible
	}

	if m.policy.Action == PolicyActionReject {
		return &MediationError{
			Code:    "schemaIncompatible",
			Message: fmt.Sprintf("payload contains %d incompatible schema object(s) and policy is reject", len(needs)),
		}
	}

	// Fetch all translation artifacts; any failure applies onFailure policy.
	artifacts, failures := m.fetchAllArtifacts(ctx, needs)
	if len(failures) > 0 {
		return m.applyOnFailure(fmt.Errorf("schemaversionmediator: %d artifact fetch failure(s): first: %w", len(failures), failures[0].Reason))
	}

	// Build mapping entries from artifacts (JSONata content-type only for now).
	entries := make([]MappingEntry, 0, len(artifacts))
	for jsonataPath, artifact := range artifacts {
		entries = append(entries, MappingEntry{
			JSONataPath: jsonataPath,
			Expression:  string(artifact.Content),
		})
	}

	expression, err := ComposeExpression(entries)
	if err != nil {
		return fmt.Errorf("schemaversionmediator: compose expression: %w", err)
	}

	msgBytes, err := extractMessageSubtree(ctx.Body)
	if err != nil {
		return fmt.Errorf("schemaversionmediator: extract message subtree: %w", err)
	}

	translated, err := m.Execute(ctx, expression, msgBytes)
	if err != nil {
		return fmt.Errorf("schemaversionmediator: execute translation: %w", err)
	}

	dropped, err := droppedFields(msgBytes, translated)
	if err != nil {
		return fmt.Errorf("schemaversionmediator: data-loss detection: %w", err)
	}
	if len(dropped) > 0 {
		return &MediationError{
			Code:         "schemaTranslationDataLoss",
			Message:      "translation dropped fields that were present in the source payload",
			DroppedFields: dropped,
		}
	}

	patched, err := patchMessageSubtree(ctx.Body, translated)
	if err != nil {
		return fmt.Errorf("schemaversionmediator: patch message subtree: %w", err)
	}
	ctx.Body = patched
	return nil
}

// applyOnFailure returns the appropriate error or nil depending on policy.OnFailure.
// cause is retained for the wrapped error chain but not surfaced in the
// user-facing MediationError.Message to avoid leaking internal detail.
func (m *mediator) applyOnFailure(cause error) error {
	if m.policy.OnFailure == PolicyActionPassThrough {
		return nil
	}
	return &MediationError{
		Code:    "schemaIncompatible",
		Message: "schema version mediation failed; check adapter logs for details",
		cause:   cause,
	}
}

// fetchCounterpartyManifest fetches and parses the counterparty's node manifest
// via the manifest loader.
func (m *mediator) fetchCounterpartyManifest(ctx context.Context, counterpartyID string) (*model.NodeManifest, error) {
	doc, err := m.loader.GetBySubscriberID(ctx, counterpartyID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("no manifest document returned")
	}
	return parseNodeManifest(doc)
}

// parseNodeManifest parses the raw YAML content of a ManifestDocument into a NodeManifest.
func parseNodeManifest(doc *model.ManifestDocument) (*model.NodeManifest, error) {
	nm, err := model.ParseNodeManifest(doc.Content)
	if err != nil {
		return nil, fmt.Errorf("schemaversionmediator: parse node manifest: %w", err)
	}
	return nm, nil
}

// extractNetworkID reads context.network_id / context.networkId from a Beckn payload.
func extractNetworkID(body []byte) string {
	var envelope struct {
		Context map[string]json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	for _, key := range []string{"network_id", "networkId"} {
		if raw, ok := envelope.Context[key]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil && s != "" {
				return s
			}
		}
	}
	return ""
}

// extractMessageSubtree returns the raw JSON bytes of the "message" field from
// a Beckn payload. Returns an error if "message" is absent.
func extractMessageSubtree(body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	msg, ok := envelope["message"]
	if !ok {
		return nil, fmt.Errorf("payload has no \"message\" field")
	}
	return msg, nil
}

// patchMessageSubtree replaces the "message" value in body with translated and
// returns the re-serialised full payload.
func patchMessageSubtree(body, translated []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	envelope["message"] = translated
	return json.Marshal(envelope)
}

// MediationError is a structured rejection returned by Mediate. It carries a
// camelCase error code and a human-readable message so the handler can build a
// Beckn NACK response with the correct fault details. cause is the underlying
// technical error; it is available via errors.Unwrap for logging but is not
// exposed in the user-facing Message.
type MediationError struct {
	Code          string
	Message       string
	DroppedFields []string // non-nil only for schemaTranslationDataLoss
	cause         error    // internal; use errors.Unwrap to access
}

func (e *MediationError) Error() string {
	if len(e.DroppedFields) > 0 {
		return fmt.Sprintf("schemaversionmediator: %s: %s (dropped: %s)", e.Code, e.Message, strings.Join(e.DroppedFields, ", "))
	}
	return fmt.Sprintf("schemaversionmediator: %s: %s", e.Code, e.Message)
}

func (e *MediationError) Unwrap() error { return e.cause }

// droppedFields returns the sorted set of dot-notation key paths that are
// present in src but absent in dst. Both must be JSON object bytes.
// Arrays are treated as opaque leaf values — element-level drops within an
// array are not detected. Only object key presence is compared.
func droppedFields(src, dst []byte) ([]string, error) {
	var srcMap, dstMap map[string]any
	if err := json.Unmarshal(src, &srcMap); err != nil {
		return nil, fmt.Errorf("unmarshal source: %w", err)
	}
	if err := json.Unmarshal(dst, &dstMap); err != nil {
		return nil, fmt.Errorf("unmarshal translated: %w", err)
	}
	srcKeys := flattenKeyPaths(srcMap, "")
	dstKeys := flattenKeyPaths(dstMap, "")
	dstSet := make(map[string]struct{}, len(dstKeys))
	for _, k := range dstKeys {
		dstSet[k] = struct{}{}
	}
	var dropped []string
	for _, k := range srcKeys {
		if _, ok := dstSet[k]; !ok {
			dropped = append(dropped, k)
		}
	}
	sort.Strings(dropped)
	return dropped, nil
}

// flattenKeyPaths recursively collects all dot-notation leaf paths from a
// JSON object tree. Array elements are not indexed — array presence is
// tracked at the array key level only.
func flattenKeyPaths(v map[string]any, prefix string) []string {
	var keys []string
	for k, child := range v {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		if nested, ok := child.(map[string]any); ok {
			keys = append(keys, flattenKeyPaths(nested, full)...)
		} else {
			keys = append(keys, full)
		}
	}
	return keys
}

// fetchArtifact returns the translation artifact for the given TranslationNeeded.
// The artifact URL is derived from the To context URL base and the From version.
// Results are cached; 404s are negative-cached to avoid repeated network calls.
// Retries once on transient failures (5xx, network errors); no retry on 404.
func (m *mediator) fetchArtifact(ctx context.Context, need TranslationNeeded) (*TranslationArtifact, error) {
	artifactURL, err := deriveArtifactURL(need)
	if err != nil {
		return nil, err
	}
	return m.fetchArtifactByURL(ctx, artifactURL)
}

// fetchArtifactByURL is the URL-scoped fetch primitive used by both fetchArtifact
// and fetchAllArtifacts. Callers that have already derived the URL use this
// directly to avoid a second derivation.
func (m *mediator) fetchArtifactByURL(ctx context.Context, artifactURL string) (*TranslationArtifact, error) {
	if artifact, found := m.cache.get(artifactURL); found {
		if artifact == nil {
			return nil, ErrArtifactNotFound
		}
		return artifact, nil
	}

	artifact, err := m.doFetch(ctx, artifactURL)
	if err != nil {
		if errors.Is(err, ErrArtifactNotFound) {
			m.cache.set(artifactURL, nil) // negative cache
		}
		return nil, err
	}

	m.cache.set(artifactURL, artifact)
	return artifact, nil
}

// doFetch attempts the HTTP fetch with one retry on transient failure.
func (m *mediator) doFetch(ctx context.Context, artifactURL string) (*TranslationArtifact, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		artifact, err := m.httpFetch(ctx, artifactURL)
		if err == nil {
			return artifact, nil
		}
		if errors.Is(err, ErrArtifactNotFound) {
			return nil, err // permanent — no retry
		}
		lastErr = err
	}
	return nil, lastErr
}

// httpFetch performs a single HTTP GET for the artifact URL.
func (m *mediator) httpFetch(ctx context.Context, artifactURL string) (*TranslationArtifact, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return nil, fmt.Errorf("schemaversionmediator: build request for %q: %w", artifactURL, err)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("schemaversionmediator: fetch artifact %q: %w", artifactURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrArtifactNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("schemaversionmediator: artifact %q: unexpected status %d", artifactURL, resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		// Artifact URL convention omits file extensions, so Content-Type is the
		// only reliable signal for which Translator to dispatch to.
		return nil, fmt.Errorf("schemaversionmediator: artifact %q: missing Content-Type header", artifactURL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArtifactBodySize))
	if err != nil {
		return nil, fmt.Errorf("schemaversionmediator: read artifact %q: %w", artifactURL, err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("schemaversionmediator: artifact %q: empty response body", artifactURL)
	}
	return &TranslationArtifact{Content: body, ContentType: contentType}, nil
}

// deriveArtifactURL constructs the translation artifact URL from a TranslationNeeded.
// Convention: {directory of To.ContextURL}/{From.Type}_from_{fromVersion}
// From.Type is used (not To.Type) because the artifact describes how to map *from*
// the old type representation; if a type is renamed across versions, the artifact
// is identified by what it transforms away from.
// Example: https://schema.beckn.io/retail/v2.0/Order_from_v1.1
func deriveArtifactURL(need TranslationNeeded) (string, error) {
	if need.To == nil {
		return "", fmt.Errorf("schemaversionmediator: cannot derive artifact URL for unknown schema type %q (To is nil)", need.From.Type)
	}
	toURL, err := url.Parse(need.To.ContextURL)
	if err != nil {
		return "", fmt.Errorf("schemaversionmediator: invalid To context URL %q: %w", need.To.ContextURL, err)
	}
	fromVersion, err := extractVersionSegment(need.From.ContextURL)
	if err != nil {
		return "", err
	}
	result := *toURL
	result.Path = path.Dir(toURL.Path) + "/" + need.From.Type + "_from_" + fromVersion
	return result.String(), nil
}

// extractVersionSegment walks the path segments of a context URL and returns
// the first version identifier (e.g. "v1.1" from ".../retail/v1.1/Order.jsonld").
func extractVersionSegment(contextURL string) (string, error) {
	u, err := url.Parse(contextURL)
	if err != nil {
		return "", fmt.Errorf("schemaversionmediator: invalid context URL %q: %w", contextURL, err)
	}
	for _, seg := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if isVersionSegment(seg) {
			return seg, nil
		}
	}
	return "", fmt.Errorf("schemaversionmediator: no version segment found in context URL %q", contextURL)
}

// isVersionSegment returns true for path segments that are version identifiers
// (e.g. "v1.1", "v2.0", "1.0"). Requires at least one dot to avoid matching
// bare numbers like "2" that could be part of a path name. Replicates
// schemav2validator's convention.
func isVersionSegment(s string) bool {
	if len(s) == 0 {
		return false
	}
	check := s
	if check[0] == 'v' || check[0] == 'V' {
		check = check[1:]
	}
	if len(check) == 0 {
		return false
	}
	if check[0] == '.' || check[len(check)-1] == '.' {
		return false
	}
	hasDot := false
	for _, c := range check {
		if c == '.' {
			hasDot = true
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return hasDot
}

// loadMapManagerConfig parses map manager config keys from the plugin config map.
func loadMapManagerConfig(config map[string]string) (fetchTimeout, positiveTTL, negativeTTL time.Duration, maxEntries int, err error) {
	fetchTimeout = defaultFetchTimeout
	positiveTTL = defaultPositiveTTL
	negativeTTL = defaultNegativeTTL
	maxEntries = defaultMaxCacheEntries

	if v, ok := config["fetchTimeout"]; ok {
		if fetchTimeout, err = time.ParseDuration(v); err != nil {
			return 0, 0, 0, 0, fmt.Errorf("schemaversionmediator: invalid fetchTimeout %q: %w", v, err)
		}
	}
	if v, ok := config["artifactCacheTTL"]; ok {
		if positiveTTL, err = time.ParseDuration(v); err != nil {
			return 0, 0, 0, 0, fmt.Errorf("schemaversionmediator: invalid artifactCacheTTL %q: %w", v, err)
		}
	}
	if v, ok := config["negativeCacheTTL"]; ok {
		if negativeTTL, err = time.ParseDuration(v); err != nil {
			return 0, 0, 0, 0, fmt.Errorf("schemaversionmediator: invalid negativeCacheTTL %q: %w", v, err)
		}
	}
	if v, ok := config["maxCacheEntries"]; ok {
		if maxEntries, err = strconv.Atoi(v); err != nil || maxEntries <= 0 {
			return 0, 0, 0, 0, fmt.Errorf("schemaversionmediator: invalid maxCacheEntries %q: must be a positive integer", v)
		}
	}
	return
}

// --- JSONata composition and execution ---

// MappingEntry pairs a translation artifact expression with the payload path
// of the schema object it targets. The artifact expression is written to operate
// on the Beckn message subtree (not the full payload) and must return an object
// whose fields are merged at the message root — i.e. a message-level patch.
//
// JSONataPath is metadata carried for the caller's logging and debugging use.
// ComposeExpression does not interpret it — the Expression itself must reference
// message-level paths directly (e.g. `fulfillment.type`, not `$.message.fulfillment.type`).
//
// Example for an Order at $.message:
//
//	Expression: `{"state": status}`  →  adds "state" from "status" at message root
//
// Example for a Fulfillment at $.message.fulfillment:
//
//	Expression: `{"fulfillment": $merge([fulfillment, {"fulfillment_type": fulfillment.type}])}`
type MappingEntry struct {
	JSONataPath string // from WalkPayload, e.g. "$.message.fulfillment"; metadata only for the caller
	Expression  string // message-level patch expression
}

// ComposeExpression combines N per-object patch expressions into a single JSONata
// expression that applies all transforms to the message subtree in one Evaluate call.
//
// Each entry's Expression must return an object that is merged at the message root.
// Artifact authors write expressions that reference message-level paths directly
// (e.g. `fulfillment.type`). See TestExecute_MultiPathComposed for a verified
// example of three schema objects transformed in a single composed expression.
//
// An empty entries list returns the identity expression "$".
// The returned string can be compiled and evaluated by Execute.
func ComposeExpression(entries []MappingEntry) (string, error) {
	if len(entries) == 0 {
		return "$", nil
	}
	patches := make([]string, len(entries))
	for i, e := range entries {
		if strings.TrimSpace(e.Expression) == "" {
			return "", fmt.Errorf("schemaversionmediator: compose expression: entry %d (path %q) has empty expression", i, e.JSONataPath)
		}
		patches[i] = e.Expression
	}
	return "$merge([$, " + strings.Join(patches, ", ") + "])", nil
}

// compiledExpr returns a cached compiled JSONata expression for the given
// expression string, compiling and caching it on the first call.
func (m *mediator) compiledExpr(expression string) (jsonata.Expression, error) {
	m.exprs.mu.RLock()
	if expr, ok := m.exprs.entries[expression]; ok {
		m.exprs.mu.RUnlock()
		return expr, nil
	}
	m.exprs.mu.RUnlock()

	expr, err := m.jsonataInstance.Compile(expression, false)
	if err != nil {
		return nil, fmt.Errorf("schemaversionmediator: compile expression: %w", err)
	}

	m.exprs.mu.Lock()
	if len(m.exprs.entries) < m.exprs.max {
		m.exprs.entries[expression] = expr
	}
	m.exprs.mu.Unlock()
	return expr, nil
}

// Execute compiles (with caching) and evaluates a JSONata expression against
// the Beckn message subtree bytes. It returns the transformed message bytes.
// The expression is typically produced by ComposeExpression.
// ctx is accepted for interface consistency; jsonata-go does not support
// context cancellation, so it is not forwarded to the evaluator.
func (m *mediator) Execute(ctx context.Context, expression string, message []byte) ([]byte, error) {
	_ = ctx
	expr, err := m.compiledExpr(expression)
	if err != nil {
		return nil, err
	}
	result, err := expr.Evaluate(message, nil)
	if err != nil {
		return nil, fmt.Errorf("schemaversionmediator: execute expression: %w", err)
	}
	return result, nil
}

// --- Batch artifact fetching with comprehensive failure reporting ---

// ArtifactFetchFailure records a single failed artifact fetch with the full
// context needed for a structured log event: which schema object was being
// translated, from/to what version, which URL was attempted, and why it failed.
type ArtifactFetchFailure struct {
	Need   TranslationNeeded
	URL    string // artifact URL that was attempted; empty when URL derivation failed
	Reason error
}

// fetchAllArtifacts attempts to fetch translation artifacts for every need in
// the slice. All fetches are attempted regardless of individual failures so the
// caller can log the complete failure picture in a single structured log event
// rather than surfacing one error at a time.
//
// Two distinct failure modes are reported through ArtifactFetchFailure:
//  1. To == nil: the schema type is entirely absent from the local manifest;
//     no artifact URL can be derived. Reason will describe the missing type.
//  2. fetchArtifact returned an error: URL was derived but the fetch failed
//     (ErrArtifactNotFound or a transient network error).
//
// Returns a nil error slice only when ALL fetches succeed. The caller must check
// for failures before calling ComposeExpression and Execute.
func (m *mediator) fetchAllArtifacts(ctx context.Context, needs []TranslationNeeded) (map[string]*TranslationArtifact, []ArtifactFetchFailure) {
	artifacts := make(map[string]*TranslationArtifact, len(needs))
	var failures []ArtifactFetchFailure

	for _, need := range needs {
		if need.To == nil {
			failures = append(failures, ArtifactFetchFailure{
				Need:   need,
				Reason: fmt.Errorf("type %q not in local manifest: no translation target known", need.From.Type),
			})
			continue
		}

		artifactURL, err := deriveArtifactURL(need)
		if err != nil {
			failures = append(failures, ArtifactFetchFailure{Need: need, Reason: err})
			continue
		}

		artifact, err := m.fetchArtifactByURL(ctx, artifactURL)
		if err != nil {
			failures = append(failures, ArtifactFetchFailure{Need: need, URL: artifactURL, Reason: err})
			continue
		}
		artifacts[need.JSONataPath] = artifact
	}
	return artifacts, failures
}

// --- CheckCompatibility ---

// CheckCompatibility compares extracted schema object refs against the local node
// manifest and returns those that require translation. An empty result means
// the payload is fully compatible and the mediator can short-circuit.
//
// Returns ErrNoManifest if manifest is nil — the caller should log a warning
// and skip mediation rather than treating this as a hard failure.
//
// For each extracted SchemaObjectRef:
//   - Exact match in manifest → compatible, omitted from result.
//   - Same Type, different ContextURL → TranslationNeeded with To set to the
//     locally supported SchemaObject (version the node expects).
//   - Type absent from manifest entirely → TranslationNeeded with To nil;
//     handling is delegated to the data-loss policy enforcer.
//
// The JSONataPath from each ref is forwarded into TranslationNeeded for the
// caller's logging and debugging use; it is not interpreted by ComposeExpression.
func CheckCompatibility(extracted []SchemaObjectRef, manifest *model.NodeManifest) ([]TranslationNeeded, error) {
	if manifest == nil {
		return nil, ErrNoManifest
	}

	supported := make(map[string]model.SchemaObject, len(manifest.Schema.SchemaObjects))
	for _, obj := range manifest.Schema.SchemaObjects {
		supported[obj.Type] = obj
	}

	var needs []TranslationNeeded
	for _, ref := range extracted {
		local, known := supported[ref.Type]
		switch {
		case !known:
			needs = append(needs, TranslationNeeded{From: ref.SchemaObject, JSONataPath: ref.JSONataPath})
		case local.ContextURL != ref.ContextURL:
			to := local
			needs = append(needs, TranslationNeeded{From: ref.SchemaObject, To: &to, JSONataPath: ref.JSONataPath})
		}
	}
	return needs, nil
}
