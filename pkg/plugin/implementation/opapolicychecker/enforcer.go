package opapolicychecker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"gopkg.in/yaml.v3"
)

// Config holds the configuration for the OPA Policy Checker plugin.
type Config struct {
	NetworkPolicyConfig string
	RefreshInterval     time.Duration // 0 = disabled
	Enabled             bool
	DebugLogging        bool
	RuntimeConfig       map[string]string
}

type PolicyConfig struct {
	Type          string
	Location      string
	PolicyPaths   []string
	Query         string
	Actions       []string
	Enabled       bool
	FetchTimeout  time.Duration
	IsBundle      bool
	Verification  *ArtifactVerificationConfig
	RuntimeConfig map[string]string
}

var knownKeys = map[string]bool{
	"networkPolicyConfig": true,
	"enabled":             true,
	"debugLogging":        true,
	"refreshInterval":     true,
}

// policyEntryKnownKeys is matched against the post-flattening key shape produced
// by loadNetworkPolicies, so nested YAML like verification.publicKeyLookupUrl is
// validated here using dot-notation.
var policyEntryKnownKeys = map[string]bool{
	"type":                            true,
	"location":                        true,
	"query":                           true,
	"actions":                         true,
	"enabled":                         true,
	"fetchTimeoutSeconds":             true,
	"verification.enabled":            true,
	"verification.publicKeyLookupUrl": true,
	"verification.signatureLocation":  true,
	"verification.algorithm":          true,
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		RuntimeConfig: make(map[string]string),
	}
}

func defaultPolicyConfig() *PolicyConfig {
	return &PolicyConfig{
		Enabled:       true,
		FetchTimeout:  defaultPolicyFetchTimeout,
		RuntimeConfig: make(map[string]string),
	}
}

// ParseConfig parses the top-level plugin configuration map into a Config struct.
func ParseConfig(cfg map[string]string) (*Config, error) {
	config := DefaultConfig()

	npc, ok := cfg["networkPolicyConfig"]
	if !ok || strings.TrimSpace(npc) == "" {
		return nil, fmt.Errorf("'networkPolicyConfig' is required")
	}
	config.NetworkPolicyConfig = npc

	if enabled, ok := cfg["enabled"]; ok {
		config.Enabled = enabled == "true" || enabled == "1"
	}

	if debug, ok := cfg["debugLogging"]; ok {
		config.DebugLogging = debug == "true" || debug == "1"
	}

	if interval, ok := cfg["refreshInterval"]; ok && interval != "" {
		dur, err := time.ParseDuration(interval)
		if err != nil || dur < 0 {
			return nil, fmt.Errorf("'refreshInterval' must be a valid non-negative duration, got %q", interval)
		}
		config.RefreshInterval = dur
	}

	for k, v := range cfg {
		if !knownKeys[k] {
			config.RuntimeConfig[k] = v
		}
	}

	return config, nil
}

func parsePolicyConfig(cfg map[string]string) (*PolicyConfig, error) {
	config := defaultPolicyConfig()

	typ, hasType := cfg["type"]
	if !hasType || typ == "" {
		return nil, fmt.Errorf("'type' is required (file, dir, or bundle)")
	}
	config.Type = typ

	location, hasLoc := cfg["location"]
	if !hasLoc || location == "" {
		return nil, fmt.Errorf("'location' is required")
	}
	config.Location = location

	switch typ {
	case "file":
		config.PolicyPaths = append(config.PolicyPaths, location)
	case "dir":
		config.PolicyPaths = append(config.PolicyPaths, location)
	case "bundle":
		config.IsBundle = true
		config.PolicyPaths = append(config.PolicyPaths, location)
	default:
		return nil, fmt.Errorf("unsupported type %q (expected: file, dir, or bundle)", typ)
	}

	query, hasQuery := cfg["query"]
	if !hasQuery || query == "" {
		return nil, fmt.Errorf("'query' is required (e.g., data.policy.violations)")
	}
	config.Query = query

	if actions, ok := cfg["actions"]; ok && actions != "" {
		actionList := strings.Split(actions, ",")
		config.Actions = make([]string, 0, len(actionList))
		for _, action := range actionList {
			action = strings.TrimSpace(action)
			if action != "" {
				config.Actions = append(config.Actions, action)
			}
		}
	}

	if enabled, ok := cfg["enabled"]; ok {
		config.Enabled = enabled == "true" || enabled == "1"
	}

	if fts, ok := cfg["fetchTimeoutSeconds"]; ok && fts != "" {
		secs, err := strconv.Atoi(fts)
		if err != nil || secs <= 0 {
			return nil, fmt.Errorf("'fetchTimeoutSeconds' must be a positive integer, got %q", fts)
		}
		config.FetchTimeout = time.Duration(secs) * time.Second
	}

	if verificationEnabled, ok := cfg["verification.enabled"]; ok && verificationEnabled != "" {
		enabled, err := strconv.ParseBool(verificationEnabled)
		if err != nil {
			return nil, fmt.Errorf("'verification.enabled' must be a boolean, got %q", verificationEnabled)
		}
		if enabled {
			config.Verification = &ArtifactVerificationConfig{
				Enabled:            true,
				PublicKeyLookupURL: strings.TrimSpace(cfg["verification.publicKeyLookupUrl"]),
				SignatureLocation:  strings.TrimSpace(cfg["verification.signatureLocation"]),
				Algorithm:          strings.TrimSpace(cfg["verification.algorithm"]),
			}
			if config.Verification.Algorithm == "" {
				config.Verification.Algorithm = defaultBundleVerificationAlgorithm
			}

			if config.Verification.PublicKeyLookupURL == "" {
				return nil, fmt.Errorf("'verification.publicKeyLookupUrl' is required when verification.enabled=true")
			}

			switch config.Type {
			case "bundle":
				if config.Verification.SignatureLocation != "" {
					return nil, fmt.Errorf("'verification.signatureLocation' must not be set for type=bundle; bundle signatures are read from inside the bundle")
				}
			case "dir":
				return nil, fmt.Errorf("verification is not supported for type=dir; package the directory as a signed bundle instead")
			case "file":
				if config.Verification.SignatureLocation == "" {
					return nil, fmt.Errorf("'verification.signatureLocation' is required when verification.enabled=true for type=%s", config.Type)
				}
			}
		}
	}

	for k, v := range cfg {
		if !policyEntryKnownKeys[k] {
			config.RuntimeConfig[k] = v
		}
	}

	return config, nil
}

func (c *PolicyConfig) IsActionEnabled(action string) bool {
	if len(c.Actions) == 0 {
		return true
	}
	for _, a := range c.Actions {
		if a == action {
			return true
		}
	}
	return false
}

type loadedPolicy struct {
	name      string
	config    *PolicyConfig
	evaluator *Evaluator
}

type networkPolicyFile struct {
	NetworkPolicies map[string]map[string]interface{} `yaml:"networkPolicies"`
}

// PolicyEnforcer evaluates beckn messages against OPA policies and NACKs non-compliant messages.
type PolicyEnforcer struct {
	config        *Config
	policies      map[string]*loadedPolicy
	defaultPolicy *loadedPolicy
	evaluatorMu   sync.RWMutex
	closeOnce     sync.Once
	done          chan struct{}
}

func loadNetworkPolicies(configPath string) (map[string]map[string]string, error) {
	if configPath == "" {
		return nil, fmt.Errorf("networkPolicyConfig path is empty")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("error reading network policy config file at %s: %w", configPath, err)
	}

	var file networkPolicyFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("error parsing network policy config YAML: %w", err)
	}
	if len(file.NetworkPolicies) == 0 {
		return nil, fmt.Errorf("networkPolicies must contain at least one entry")
	}

	policies := make(map[string]map[string]string, len(file.NetworkPolicies))
	for policyName, rawCfg := range file.NetworkPolicies {
		if strings.TrimSpace(policyName) == "" {
			return nil, fmt.Errorf("network policy key cannot be empty")
		}
		if rawCfg == nil {
			return nil, fmt.Errorf("network policy %q config cannot be empty", policyName)
		}

		cfg := make(map[string]string, len(rawCfg))
		for k, v := range rawCfg {
			switch typed := v.(type) {
			case string:
				cfg[k] = typed
			case bool:
				cfg[k] = strconv.FormatBool(typed)
			case int:
				cfg[k] = strconv.Itoa(typed)
			case int64:
				cfg[k] = strconv.FormatInt(typed, 10)
			case float64:
				cfg[k] = strconv.FormatFloat(typed, 'f', -1, 64)
			case map[string]interface{}:
				if k != "verification" {
					return nil, fmt.Errorf("network policy %q field %q must be a scalar value", policyName, k)
				}
				for subKey, subValue := range typed {
					flatKey := "verification." + subKey
					switch subTyped := subValue.(type) {
					case string:
						cfg[flatKey] = subTyped
					case bool:
						cfg[flatKey] = strconv.FormatBool(subTyped)
					case int:
						cfg[flatKey] = strconv.Itoa(subTyped)
					case int64:
						cfg[flatKey] = strconv.FormatInt(subTyped, 10)
					case float64:
						cfg[flatKey] = strconv.FormatFloat(subTyped, 'f', -1, 64)
					case nil:
						return nil, fmt.Errorf("network policy %q field %q cannot be null", policyName, flatKey)
					default:
						return nil, fmt.Errorf("network policy %q field %q must be a scalar value", policyName, flatKey)
					}
				}
			case nil:
				return nil, fmt.Errorf("network policy %q field %q cannot be null", policyName, k)
			default:
				return nil, fmt.Errorf("network policy %q field %q must be a scalar value", policyName, k)
			}
		}
		policies[policyName] = cfg
	}

	return policies, nil
}

func mergeRuntimeConfig(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}

	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

func loadPolicy(policyName string, config *PolicyConfig, sharedRuntimeConfig map[string]string) (*loadedPolicy, error) {
	policyConfig := *config
	policyConfig.RuntimeConfig = mergeRuntimeConfig(sharedRuntimeConfig, config.RuntimeConfig)

	loaded := &loadedPolicy{
		name:   policyName,
		config: &policyConfig,
	}
	if !policyConfig.Enabled {
		return loaded, nil
	}

	evaluator, err := NewEvaluator(
		policyConfig.PolicyPaths,
		policyConfig.Query,
		policyConfig.RuntimeConfig,
		policyConfig.IsBundle,
		policyConfig.FetchTimeout,
		policyConfig.Verification,
	)
	if err != nil {
		return nil, err
	}
	loaded.evaluator = evaluator
	return loaded, nil
}

func loadNetworkPoliciesForEnforcer(config *Config) (map[string]*loadedPolicy, *loadedPolicy, error) {
	rawPolicies, err := loadNetworkPolicies(config.NetworkPolicyConfig)
	if err != nil {
		return nil, nil, err
	}

	policies := make(map[string]*loadedPolicy, len(rawPolicies))
	var defaultPolicy *loadedPolicy

	for policyName, rawCfg := range rawPolicies {
		policyCfg, err := parsePolicyConfig(rawCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid network policy %q: %w", policyName, err)
		}

		loaded, err := loadPolicy(policyName, policyCfg, config.RuntimeConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialize network policy %q: %w", policyName, err)
		}

		if policyName == "default" {
			defaultPolicy = loaded
		} else {
			policies[policyName] = loaded
		}
	}

	return policies, defaultPolicy, nil
}

func logLoadedPolicy(ctx context.Context, networkScoped bool, policy *loadedPolicy) {
	if policy == nil || policy.config == nil {
		return
	}

	moduleNames := []string{}
	if policy.evaluator != nil {
		moduleNames = policy.evaluator.ModuleNames()
	}

	if networkScoped {
		log.Infof(ctx, "OPAPolicyChecker: loaded network policy networkID=%q type=%s location=%s query=%s actions=%v enabled=%t modules=%v",
			policy.name,
			policy.config.Type,
			policy.config.Location,
			policy.config.Query,
			policy.config.Actions,
			policy.config.Enabled,
			moduleNames,
		)
		return
	}

	log.Infof(ctx, "OPAPolicyChecker: loaded default policy type=%s location=%s query=%s actions=%v enabled=%t modules=%v",
		policy.config.Type,
		policy.config.Location,
		policy.config.Query,
		policy.config.Actions,
		policy.config.Enabled,
		moduleNames,
	)
}

func New(ctx context.Context, cfg map[string]string) (*PolicyEnforcer, error) {
	config, err := ParseConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("opapolicychecker: config error: %w", err)
	}

	enforcer := &PolicyEnforcer{
		config: config,
		done:   make(chan struct{}),
	}

	if !config.Enabled {
		log.Warnf(ctx, "OPAPolicyChecker is disabled via config; policy enforcement will be skipped")
		return enforcer, nil
	}

	enforcer.policies, enforcer.defaultPolicy, err = loadNetworkPoliciesForEnforcer(config)
	if err != nil {
		return nil, fmt.Errorf("opapolicychecker: failed to initialize network policies: %w", err)
	}

	log.Infof(ctx, "OPAPolicyChecker initialized in network policy mode (policyConfig=%s, policies=%d, hasDefault=%t, refreshInterval=%s)",
		config.NetworkPolicyConfig, len(enforcer.policies), enforcer.defaultPolicy != nil, config.RefreshInterval)
	for _, policy := range enforcer.policies {
		logLoadedPolicy(ctx, true, policy)
	}
	if enforcer.defaultPolicy != nil {
		logLoadedPolicy(ctx, false, enforcer.defaultPolicy)
	}

	if config.RefreshInterval > 0 {
		go enforcer.refreshLoop(ctx)
	}

	return enforcer, nil
}

// refreshLoop periodically reloads and recompiles OPA policies.
// Follows the schemav2validator pattern: driven by context cancellation.
func (e *PolicyEnforcer) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(e.config.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Infof(ctx, "OPAPolicyChecker: refresh loop stopped")
			return
		case <-e.done:
			log.Infof(ctx, "OPAPolicyChecker: refresh loop stopped by Close()")
			return
		case <-ticker.C:
			e.reloadPolicies(ctx)
		}
	}
}

// reloadPolicies reloads and recompiles all policies, atomically swapping the evaluator.
// Reload failures are non-fatal; the old evaluator stays active.
func (e *PolicyEnforcer) reloadPolicies(ctx context.Context) {
	e.reloadNetworkPolicies(ctx)
}

func (e *PolicyEnforcer) reloadNetworkPolicies(ctx context.Context) {
	start := time.Now()

	policies, defaultPolicy, err := loadNetworkPoliciesForEnforcer(e.config)
	if err != nil {
		log.Errorf(ctx, err, "OPAPolicyChecker: network policy reload failed (keeping previous policies): %v", err)
		return
	}

	e.evaluatorMu.Lock()
	e.policies = policies
	e.defaultPolicy = defaultPolicy
	e.evaluatorMu.Unlock()

	log.Infof(ctx, "OPAPolicyChecker: network policies reloaded in %s (policies=%d, hasDefault=%t)", time.Since(start), len(policies), defaultPolicy != nil)
}

func (e *PolicyEnforcer) selectedPolicy(body []byte) (*loadedPolicy, string) {
	e.evaluatorMu.RLock()
	defer e.evaluatorMu.RUnlock()

	networkID := extractNetworkID(body)
	if networkID != "" {
		if policy, ok := e.policies[networkID]; ok {
			return policy, networkID
		}
	}
	if e.defaultPolicy != nil {
		return e.defaultPolicy, networkID
	}
	return nil, networkID
}

// CheckPolicy evaluates the message body against loaded OPA policies.
// Returns a BadReqErr (causing NACK) if violations are found.
// Returns an error on evaluation failure (fail closed).
func (e *PolicyEnforcer) CheckPolicy(ctx *model.StepContext) error {
	if !e.config.Enabled {
		log.Debug(ctx, "OPAPolicyChecker: plugin disabled, skipping")
		return nil
	}

	policy, selectedNetworkID := e.selectedPolicy(ctx.Body)
	if policy == nil {
		log.Debugf(ctx, "OPAPolicyChecker: no matching network policy for networkID=%q and no default configured, skipping", selectedNetworkID)
		return nil
	}
	policyConfig := policy.config

	action := extractAction(ctx.Request.URL.Path, ctx.Body)

	if !policyConfig.IsActionEnabled(action) {
		if e.config.DebugLogging {
			log.Debugf(ctx, "OPAPolicyChecker: action %q not in configured actions %v, skipping", action, policyConfig.Actions)
		}
		return nil
	}

	if !policyConfig.Enabled {
		log.Debug(ctx, "OPAPolicyChecker: selected policy is disabled, skipping")
		return nil
	}

	// Disabled policies intentionally do not initialize an evaluator in loadPolicy.
	ev := policy.evaluator
	if ev == nil {
		return model.NewBadReqErr(fmt.Errorf("policy evaluator is not initialized"))
	}

	if e.config.DebugLogging {
		log.Debugf(ctx, "OPAPolicyChecker: evaluating policy for networkID=%q action=%q (modules=%v)", selectedNetworkID, action, ev.ModuleNames())
	}

	requestLogCtx := formatRequestLogContext(ctx.Body)

	violations, err := ev.Evaluate(ctx, ctx.Body)
	if err != nil {
		log.Errorf(ctx, err, "OPAPolicyChecker: policy evaluation failed for networkID=%q%s: %v", selectedNetworkID, requestLogCtx, err)
		return model.NewBadReqErr(fmt.Errorf("policy evaluation error: %w", err))
	}

	if len(violations) == 0 {
		if e.config.DebugLogging {
			log.Debugf(ctx, "OPAPolicyChecker: message compliant for action %q", action)
		}
		return nil
	}

	msg := fmt.Sprintf("policy violation(s): %s", strings.Join(violations, "; "))
	log.Warnf(ctx, "OPAPolicyChecker: networkID=%q%s %s", selectedNetworkID, requestLogCtx, msg)
	return model.NewBadReqErr(fmt.Errorf("%s", msg))
}

func (e *PolicyEnforcer) Close() {
	e.closeOnce.Do(func() {
		close(e.done)
	})
}

func extractAction(urlPath string, body []byte) string {
	// /bpp/caller/confirm/extra as action "extra".
	parts := strings.FieldsFunc(strings.Trim(urlPath, "/"), func(r rune) bool { return r == '/' })
	if len(parts) == 3 && isBecknDirection(parts[1]) && parts[2] != "" {
		return parts[2]
	}

	var payload struct {
		Context struct {
			Action string `json:"action"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Context.Action != "" {
		return payload.Context.Action
	}

	return ""
}

func extractNetworkID(body []byte) string {
	var payload struct {
		Context struct {
			NetworkIDCamel string `json:"networkId"`
			NetworkIDSnake string `json:"network_id"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if payload.Context.NetworkIDCamel != "" {
		return payload.Context.NetworkIDCamel
	}
	return payload.Context.NetworkIDSnake
}

type requestLogContext struct {
	BAPID         string
	BPPID         string
	MessageID     string
	TransactionID string
	Action        string
	Timestamp     string
}

func extractRequestLogContext(body []byte) requestLogContext {
	var payload struct {
		Context map[string]interface{} `json:"context"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Context == nil {
		return requestLogContext{}
	}

	get := func(snakeKey, camelKey string) string {
		if v, ok := payload.Context[snakeKey].(string); ok && v != "" {
			return v
		}
		if v, ok := payload.Context[camelKey].(string); ok && v != "" {
			return v
		}
		return ""
	}

	return requestLogContext{
		BAPID:         get("bap_id", "bapId"),
		BPPID:         get("bpp_id", "bppId"),
		MessageID:     get("message_id", "messageId"),
		TransactionID: get("transaction_id", "transactionId"),
		Action:        get("action", "action"),
		Timestamp:     get("timestamp", "timestamp"),
	}
}

func formatRequestLogContext(body []byte) string {
	ctx := extractRequestLogContext(body)
	parts := make([]string, 0, 6)
	if ctx.BAPID != "" {
		parts = append(parts, fmt.Sprintf("bap_id=%q", ctx.BAPID))
	}
	if ctx.BPPID != "" {
		parts = append(parts, fmt.Sprintf("bpp_id=%q", ctx.BPPID))
	}
	if ctx.MessageID != "" {
		parts = append(parts, fmt.Sprintf("message_id=%q", ctx.MessageID))
	}
	if ctx.TransactionID != "" {
		parts = append(parts, fmt.Sprintf("transaction_id=%q", ctx.TransactionID))
	}
	if ctx.Action != "" {
		parts = append(parts, fmt.Sprintf("action=%q", ctx.Action))
	}
	if ctx.Timestamp != "" {
		parts = append(parts, fmt.Sprintf("timestamp=%q", ctx.Timestamp))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func isBecknDirection(part string) bool {
	switch part {
	case "caller", "receiver", "reciever":
		return true
	default:
		return false
	}
}
