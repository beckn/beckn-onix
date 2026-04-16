package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"gopkg.in/yaml.v3"
)

// ── YAML config structs ──────────────────────────────────────────────────────

type patternDef struct {
	MaskType string `yaml:"maskType"` // "replace" (default) or "last4"
	Mask     string `yaml:"mask"`     // literal replacement for maskType "replace"
}

type maskRule struct {
	Keys    []string `yaml:"keys"`
	Pattern string   `yaml:"pattern"`
}

type pathOverrideDef struct {
	Path    string `yaml:"path"`
	Pattern string `yaml:"pattern"`
}

type auditConfig struct {
	Mode           string                `yaml:"mode"`           // "full" | "selective"
	Patterns       map[string]patternDef `yaml:"patterns"`
	MaskRules      []maskRule            `yaml:"maskRules"`
	PathOverrides  []pathOverrideDef     `yaml:"pathOverrides"`
	SelectedFields map[string][]string   `yaml:"selectedFields"` // action → field paths; "default" is the fallback
}

// ── Compiled config ──────────────────────────────────────────────────────────

// CompiledPattern is the ready-to-apply masking rule for a named pattern.
type CompiledPattern struct {
	MaskType string // "replace" or "last4"
	Mask     string // literal mask for MaskType "replace"
}

// CompiledConfig is the compiled, query-ready form of auditConfig.
// All lookups are O(1) map operations so the per-request cost is minimal.
type CompiledConfig struct {
	mode          string                         // "full" or "selective"
	keyToPattern  map[string]*CompiledPattern    // field name → pattern (from maskRules)
	pathOverrides map[string]*CompiledPattern    // full dot-path → pattern
	keepPaths     map[string]map[string]struct{} // action → set of paths to keep/traverse (selective only)
}

var (
	compiledCfg   *CompiledConfig
	compiledCfgMu sync.RWMutex
)

// GetCompiledConfig returns the current compiled audit configuration.
func GetCompiledConfig() *CompiledConfig {
	compiledCfgMu.RLock()
	defer compiledCfgMu.RUnlock()
	return compiledCfg
}

// ── Config loading ───────────────────────────────────────────────────────────

func loadAuditConfig(ctx context.Context, source string) error {
	src := strings.TrimSpace(source)
	if src == "" {
		err := fmt.Errorf("auditFieldsConfig source is empty")
		log.Error(ctx, err, "")
		return err
	}

	data, err := fetchSource(ctx, src)
	if err != nil {
		return err
	}

	var cfg auditConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Error(ctx, err, "failed to parse audit config YAML")
		return err
	}

	compiled, err := compile(ctx, &cfg)
	if err != nil {
		return err
	}

	compiledCfgMu.Lock()
	compiledCfg = compiled
	compiledCfgMu.Unlock()

	log.Info(ctx, "audit config loaded")
	return nil
}

// fetchSource reads audit config bytes from either an HTTP(S) URL or a local
// file path. HTTP fetches are bounded by a 10-second context timeout.
func fetchSource(ctx context.Context, src string) ([]byte, error) {
	u, err := url.Parse(src)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, src, nil)
		if err != nil {
			log.Error(ctx, err, "failed to build audit config HTTP request")
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Error(ctx, err, "failed to fetch audit config from URL")
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("unexpected HTTP status %d fetching audit config from %s", resp.StatusCode, src)
			log.Error(ctx, err, "")
			return nil, err
		}
		return io.ReadAll(resp.Body)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		log.Error(ctx, err, "failed to read audit config file")
	}
	return data, err
}

// compile validates the raw auditConfig and produces a CompiledConfig with
// all lookups pre-built for O(1) access during request processing.
func compile(ctx context.Context, cfg *auditConfig) (*CompiledConfig, error) {
	// Resolve named patterns.
	patterns := make(map[string]*CompiledPattern, len(cfg.Patterns))
	for name, def := range cfg.Patterns {
		mt := def.MaskType
		if mt == "" {
			mt = "replace"
		}
		patterns[name] = &CompiledPattern{MaskType: mt, Mask: def.Mask}
	}

	// Build field-name → pattern map from maskRules.
	keyToPattern := make(map[string]*CompiledPattern)
	for _, rule := range cfg.MaskRules {
		p, ok := patterns[rule.Pattern]
		if !ok {
			err := fmt.Errorf("maskRule references unknown pattern %q", rule.Pattern)
			log.Error(ctx, err, "")
			return nil, err
		}
		for _, key := range rule.Keys {
			keyToPattern[key] = p
		}
	}

	// Build full-path → pattern map from pathOverrides.
	pathOverrides := make(map[string]*CompiledPattern, len(cfg.PathOverrides))
	for _, po := range cfg.PathOverrides {
		p, ok := patterns[po.Pattern]
		if !ok {
			err := fmt.Errorf("pathOverride %q references unknown pattern %q", po.Path, po.Pattern)
			log.Error(ctx, err, "")
			return nil, err
		}
		pathOverrides[po.Path] = p
	}

	// Resolve and validate mode.
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "selective" {
		err := fmt.Errorf("unknown audit mode %q — must be 'full' or 'selective'", mode)
		log.Error(ctx, err, "")
		return nil, err
	}

	// Pre-compute keepPaths for selective mode.
	// Each entry expands the listed dot-paths into an exact+ancestor path set
	// so the walker can decide keep/drop in O(1) per node without any scanning.
	var keepPaths map[string]map[string]struct{}
	if mode == "selective" {
		keepPaths = make(map[string]map[string]struct{}, len(cfg.SelectedFields))
		for action, fields := range cfg.SelectedFields {
			keepPaths[action] = buildKeepSet(fields)
		}
	}

	return &CompiledConfig{
		mode:          mode,
		keyToPattern:  keyToPattern,
		pathOverrides: pathOverrides,
		keepPaths:     keepPaths,
	}, nil
}

// buildKeepSet expands dot-paths into a set containing each path AND all its
// ancestor paths, so the walker knows which intermediate nodes to traverse.
//
// Example: "context.transactionId" → {"context", "context.transactionId"}
func buildKeepSet(fields []string) map[string]struct{} {
	set := make(map[string]struct{}, len(fields)*3)
	for _, f := range fields {
		set[f] = struct{}{}
		parts := strings.Split(f, ".")
		for i := 1; i < len(parts); i++ {
			set[strings.Join(parts[:i], ".")] = struct{}{}
		}
	}
	return set
}

// ── Payload processing (single pass) ────────────────────────────────────────

// ProcessAuditPayload applies field selection and PII masking in a single
// tree traversal. Both concerns are handled together so the payload is walked
// exactly once regardless of how many maskRules or selectedFields are configured.
//
// Error handling is conservative: any parse or marshal failure returns the
// original body unchanged so the audit log entry is never silently dropped.
func ProcessAuditPayload(ctx context.Context, body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	cfg := GetCompiledConfig()
	if cfg == nil {
		return body
	}

	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		log.Warn(ctx, "audit: failed to unmarshal payload, emitting as-is")
		return body
	}

	// Determine the per-action keep set for selective mode.
	// For full mode keepSet stays nil and the walker skips the drop step.
	var keepSet map[string]struct{}
	if cfg.mode == "selective" {
		action := extractAction(root)
		keepSet = cfg.keepPaths[action]
		if keepSet == nil {
			keepSet = cfg.keepPaths["default"]
		}
	}

	walkMap(root, "", cfg, keepSet)

	out, err := json.Marshal(root)
	if err != nil {
		log.Warn(ctx, "audit: failed to marshal processed payload, emitting as-is")
		return body
	}
	return out
}

// extractAction reads context.action from the unmarshalled root without
// allocating a new map — just a single type assertion per level.
func extractAction(root map[string]interface{}) string {
	ctxNode, ok := root["context"].(map[string]interface{})
	if !ok {
		return ""
	}
	action, _ := ctxNode["action"].(string)
	return strings.TrimSpace(action)
}

// walkMap processes a JSON object node in-place. Priority order per key:
//  1. Key-name masking  (O(1) map lookup — checked first for lowest latency)
//  2. Path-override masking
//  3. Selective-mode field dropping
//  4. Recurse into nested objects / arrays
func walkMap(node map[string]interface{}, prefix string, cfg *CompiledConfig, keepSet map[string]struct{}) {
	for key := range node {
		fullPath := prefix + key

		// 1. Key-name masking — field name is sufficient signal; no path needed.
		if pattern, ok := cfg.keyToPattern[key]; ok {
			node[key] = applyMask(node[key], pattern)
			continue
		}

		// 2. Path-override masking — for fields that need path-level precision.
		if pattern, ok := cfg.pathOverrides[fullPath]; ok {
			node[key] = applyMask(node[key], pattern)
			continue
		}

		// 3. Selective mode: drop any path that is not in the keep set.
		if keepSet != nil {
			if _, keep := keepSet[fullPath]; !keep {
				delete(node, key)
				continue
			}
		}

		// 4. Recurse into nested structures.
		switch child := node[key].(type) {
		case map[string]interface{}:
			walkMap(child, fullPath+".", cfg, keepSet)
		case []interface{}:
			walkSlice(child, fullPath+".", cfg, keepSet)
		}
	}
}

// walkSlice processes each element of a JSON array, forwarding the same
// path prefix so field names inside array elements are matched correctly.
func walkSlice(arr []interface{}, prefix string, cfg *CompiledConfig, keepSet map[string]struct{}) {
	for _, elem := range arr {
		switch child := elem.(type) {
		case map[string]interface{}:
			walkMap(child, prefix, cfg, keepSet)
		case []interface{}:
			walkSlice(child, prefix, cfg, keepSet)
		}
	}
}

// ── Public API ───────────────────────────────────────────────────────────────

// LoadAuditConfig loads audit config from a local file path or HTTP(S) URL.
func LoadAuditConfig(ctx context.Context, source string) error {
	return loadAuditConfig(ctx, source)
}

// StartAuditFieldsRefresh loads the config immediately, then reloads it every
// intervalSec seconds from the same source. Returns a stop function.
func StartAuditFieldsRefresh(ctx context.Context, source string, intervalSec int64) func() {
	if intervalSec <= 0 {
		intervalSec = 3600
	}
	if err := loadAuditConfig(ctx, source); err != nil {
		log.Warn(ctx, "audit: initial config load failed")
	}

	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-done:
				ticker.Stop()
				return
			case <-ticker.C:
				reloadCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := loadAuditConfig(reloadCtx, source); err != nil {
					log.Warn(reloadCtx, "audit: config reload failed")
				}
				cancel()
			}
		}
	}()

	return func() { close(done) }
}
