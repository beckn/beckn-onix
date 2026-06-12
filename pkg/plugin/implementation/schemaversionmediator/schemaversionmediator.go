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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
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
	// PolicyActionPassIncompatible forwards the request as-is with a structured
	// log signal indicating which schema objects were not translated.
	PolicyActionPassIncompatible PolicyAction = "pass_incompatible"
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
// Valid values for action:    reject | translate | pass_incompatible
// Valid values for onFailure: reject | pass_incompatible (only validated when action=translate;
// ignored otherwise since no translation is ever attempted)
// Setting onFailure to "translate" is not permitted — it would cause a loop.
func loadTranslationPolicy(config map[string]string) (*TranslationPolicy, error) {
	p := &TranslationPolicy{
		Action:    defaultPolicy.Action,
		OnFailure: defaultPolicy.OnFailure,
	}

	if raw, ok := config["action"]; ok {
		switch PolicyAction(raw) {
		case PolicyActionReject, PolicyActionTranslate, PolicyActionPassIncompatible:
			p.Action = PolicyAction(raw)
		default:
			return nil, fmt.Errorf("schemaversionmediator: invalid action %q: must be reject, translate, or pass_incompatible", raw)
		}
	}

	// onFailure is only meaningful when action=translate. Validate it only in
	// that case — silently ignoring it for other actions avoids surprising errors
	// when operators carry over a stale onFailure key alongside action=reject.
	if p.Action == PolicyActionTranslate {
		if raw, ok := config["onFailure"]; ok {
			switch PolicyAction(raw) {
			case PolicyActionReject, PolicyActionPassIncompatible:
				p.OnFailure = PolicyAction(raw)
			case PolicyActionTranslate:
				return nil, fmt.Errorf("schemaversionmediator: onFailure cannot be %q — would cause a translation loop", raw)
			default:
				return nil, fmt.Errorf("schemaversionmediator: invalid onFailure %q: must be reject or pass_incompatible", raw)
			}
		}
	}

	return p, nil
}

// ErrNoManifest is returned by CheckCompatibility when the node manifest is nil.
// The caller should log a warning and skip mediation — translation targets cannot
// be determined without a manifest, but the absence of one is not a hard failure.
var ErrNoManifest = errors.New("schemaversionmediator: node manifest unavailable, skipping mediation")

// TranslationNeeded describes a single schema object from the payload that
// the local node cannot handle as-is and requires translation.
//
// From is the schema object as declared in the inbound payload.
// To is the schema object the local node supports for the same Type.
// To is nil when the Type is entirely absent from the local node manifest —
// an unknown schema whose handling is governed by the data-loss policy.
type TranslationNeeded struct {
	From model.SchemaObject
	To   *model.SchemaObject
}

// WalkPayload recursively traverses a JSON payload and returns all schema
// objects declared via JSON-LD "@context" and "@type" fields. The walk is
// depth-first and collects every qualifying node regardless of nesting level,
// including both a parent node and its nested children when both carry
// "@context"/"@type" declarations — each is an independent schema contract.
// The payload is not modified.
func WalkPayload(payload []byte) ([]model.SchemaObject, error) {
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("schemaversionmediator: walk payload: %w", err)
	}
	var results []model.SchemaObject
	walkNode(root, &results)
	return results, nil
}

// walkNode is the recursive descent worker for WalkPayload.
// When a map node carries both "@context" and "@type" it is collected, then
// the walk continues into its children — a parent and its nested children may
// each declare independent schema objects and both are valid collection targets.
func walkNode(node any, results *[]model.SchemaObject) {
	switch v := node.(type) {
	case map[string]any:
		if contextURL, ok := stringField(v, "@context"); ok {
			if typ, ok := stringField(v, "@type"); ok {
				*results = append(*results, model.SchemaObject{
					ContextURL: contextURL,
					Type:       typ,
				})
			}
		}
		for key, child := range v {
			// Skip the JSON-LD marker fields themselves — they are strings and
			// descending into them is a no-op, but skipping makes intent explicit.
			if key == "@context" || key == "@type" {
				continue
			}
			walkNode(child, results)
		}
	case []any:
		for _, item := range v {
			walkNode(item, results)
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

// mediator is the runtime state for the SchemaVersionMediator plugin.
// The Mediate method and provider New function are added in the mediation loop branch.
type mediator struct {
	policy     TranslationPolicy
	loader     definition.ManifestLoader
	httpClient *http.Client
	cache      *artifactCache
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

// --- CheckCompatibility ---

// CheckCompatibility compares extracted schema objects against the local node
// manifest and returns those that require translation. An empty result means
// the payload is fully compatible and the mediator can short-circuit.
//
// Returns ErrNoManifest if manifest is nil — the caller should log a warning
// and skip mediation rather than treating this as a hard failure.
//
// For each extracted SchemaObject:
//   - Exact match in manifest → compatible, omitted from result.
//   - Same Type, different ContextURL → TranslationNeeded with To set to the
//     locally supported SchemaObject (version the node expects).
//   - Type absent from manifest entirely → TranslationNeeded with To nil;
//     handling is delegated to the data-loss policy enforcer.
func CheckCompatibility(extracted []model.SchemaObject, manifest *model.NodeManifest) ([]TranslationNeeded, error) {
	if manifest == nil {
		return nil, ErrNoManifest
	}

	supported := make(map[string]model.SchemaObject, len(manifest.Schema.SchemaObjects))
	for _, obj := range manifest.Schema.SchemaObjects {
		supported[obj.Type] = obj
	}

	var needs []TranslationNeeded
	for _, from := range extracted {
		local, known := supported[from.Type]
		switch {
		case !known:
			// Type entirely absent from the local manifest — unknown schema.
			needs = append(needs, TranslationNeeded{From: from})
		case local.ContextURL != from.ContextURL:
			// Same type, different version — translation required.
			to := local
			needs = append(needs, TranslationNeeded{From: from, To: &to})
		}
		// Exact match: compatible — no entry added.
	}
	return needs, nil
}
