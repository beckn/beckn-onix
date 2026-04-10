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
	Type                string
	Location            string
	PolicyPaths         []string
	Query               string
	Actions             []string
	Enabled             bool
	DebugLogging        bool
	FetchTimeout        time.Duration
	IsBundle            bool
	RefreshInterval     time.Duration // 0 = disabled
	RuntimeConfig       map[string]string
}

var knownKeys = map[string]bool{
	"networkPolicyConfig":    true,
	"type":                   true,
	"location":               true,
	"query":                  true,
	"actions":                true,
	"enabled":                true,
	"debugLogging":           true,
	"fetchTimeoutSeconds":    true,
	"refreshIntervalSeconds": true,
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		FetchTimeout:  defaultPolicyFetchTimeout,
		RuntimeConfig: make(map[string]string),
	}
}

// ParseConfig parses the plugin configuration map into a Config struct.
// Uses type + location pattern (matches schemav2validator).
func ParseConfig(cfg map[string]string) (*Config, error) {
	config := DefaultConfig()

	if npc, ok := cfg["networkPolicyConfig"]; ok && npc != "" {
		config.NetworkPolicyConfig = npc

		if enabled, ok := cfg["enabled"]; ok {
			config.Enabled = enabled == "true" || enabled == "1"
		}

		if debug, ok := cfg["debugLogging"]; ok {
			config.DebugLogging = debug == "true" || debug == "1"
		}

		if ris, ok := cfg["refreshIntervalSeconds"]; ok && ris != "" {
			secs, err := strconv.Atoi(ris)
			if err != nil || secs < 0 {
				return nil, fmt.Errorf("'refreshIntervalSeconds' must be a non-negative integer, got %q", ris)
			}
			config.RefreshInterval = time.Duration(secs) * time.Second
		}

		for k, v := range cfg {
			if !knownKeys[k] {
				config.RuntimeConfig[k] = v
			}
		}

		return config, nil
	}

	typ, hasType := cfg["type"]
	if !hasType || typ == "" {
		return nil, fmt.Errorf("'type' is required (url, file, dir, or bundle)")
	}
	config.Type = typ

	location, hasLoc := cfg["location"]
	if !hasLoc || location == "" {
		return nil, fmt.Errorf("'location' is required")
	}
	config.Location = location

	switch typ {
	case "url":
		for _, u := range strings.Split(location, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				config.PolicyPaths = append(config.PolicyPaths, u)
			}
		}
	case "file":
		config.PolicyPaths = append(config.PolicyPaths, location)
	case "dir":
		config.PolicyPaths = append(config.PolicyPaths, location)
	case "bundle":
		config.IsBundle = true
		config.PolicyPaths = append(config.PolicyPaths, location)
	default:
		return nil, fmt.Errorf("unsupported type %q (expected: url, file, dir, or bundle)", typ)
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

	if debug, ok := cfg["debugLogging"]; ok {
		config.DebugLogging = debug == "true" || debug == "1"
	}

	if fts, ok := cfg["fetchTimeoutSeconds"]; ok && fts != "" {
		secs, err := strconv.Atoi(fts)
		if err != nil || secs <= 0 {
			return nil, fmt.Errorf("'fetchTimeoutSeconds' must be a positive integer, got %q", fts)
		}
		config.FetchTimeout = time.Duration(secs) * time.Second
	}

	if ris, ok := cfg["refreshIntervalSeconds"]; ok && ris != "" {
		secs, err := strconv.Atoi(ris)
		if err != nil || secs < 0 {
			return nil, fmt.Errorf("'refreshIntervalSeconds' must be a non-negative integer, got %q", ris)
		}
		config.RefreshInterval = time.Duration(secs) * time.Second
	}

	for k, v := range cfg {
		if !knownKeys[k] {
			config.RuntimeConfig[k] = v
		}
	}

	return config, nil
}

func (c *Config) IsActionEnabled(action string) bool {
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
	config    *Config
	evaluator *Evaluator
}

type networkPolicyFile struct {
	NetworkPolicies map[string]map[string]interface{} `yaml:"networkPolicies"`
}

// PolicyEnforcer evaluates beckn messages against OPA policies and NACKs non-compliant messages.
type PolicyEnforcer struct {
	config        *Config
	evaluator     *Evaluator
	policies      map[string]*loadedPolicy
	defaultPolicy *loadedPolicy
	evaluatorMu   sync.RWMutex
	closeOnce     sync.Once
	done          chan struct{}
}

// getEvaluator safely returns the current evaluator under a read lock.
func (e *PolicyEnforcer) getEvaluator() *Evaluator {
	e.evaluatorMu.RLock()
	ev := e.evaluator
	e.evaluatorMu.RUnlock()
	return ev
}

// setEvaluator safely swaps the evaluator under a write lock.
func (e *PolicyEnforcer) setEvaluator(ev *Evaluator) {
	e.evaluatorMu.Lock()
	e.evaluator = ev
	e.evaluatorMu.Unlock()
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

func loadPolicy(policyName string, config *Config, sharedRuntimeConfig map[string]string) (*loadedPolicy, error) {
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
		policyCfg, err := ParseConfig(rawCfg)
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

	if config.NetworkPolicyConfig != "" {
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
	} else {
		evaluator, err := NewEvaluator(config.PolicyPaths, config.Query, config.RuntimeConfig, config.IsBundle, config.FetchTimeout)
		if err != nil {
			return nil, fmt.Errorf("opapolicychecker: failed to initialize OPA evaluator: %w", err)
		}
		enforcer.evaluator = evaluator

		log.Infof(ctx, "OPAPolicyChecker initialized (actions=%v, query=%s, policies=%v, isBundle=%v, debugLogging=%v, fetchTimeout=%s, refreshInterval=%s)",
			config.Actions, config.Query, evaluator.ModuleNames(), config.IsBundle, config.DebugLogging, config.FetchTimeout, config.RefreshInterval)
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
	if e.config.NetworkPolicyConfig != "" {
		e.reloadNetworkPolicies(ctx)
		return
	}

	start := time.Now()
	newEvaluator, err := NewEvaluator(
		e.config.PolicyPaths,
		e.config.Query,
		e.config.RuntimeConfig,
		e.config.IsBundle,
		e.config.FetchTimeout,
	)
	if err != nil {
		log.Errorf(ctx, err, "OPAPolicyChecker: policy reload failed (keeping previous policies): %v", err)
		return
	}

	e.setEvaluator(newEvaluator)
	log.Infof(ctx, "OPAPolicyChecker: policies reloaded in %s (modules=%v)", time.Since(start), newEvaluator.ModuleNames())
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

	policyConfig := e.config
	ev := e.getEvaluator()
	selectedNetworkID := ""
	if e.config.NetworkPolicyConfig != "" {
		policy, networkID := e.selectedPolicy(ctx.Body)
		selectedNetworkID = networkID
		if policy == nil {
			log.Debugf(ctx, "OPAPolicyChecker: no matching network policy for networkID=%q and no default configured, skipping", networkID)
			return nil
		}
		policyConfig = policy.config
		ev = policy.evaluator
	}

	action := extractAction(ctx.Request.URL.Path, ctx.Body)

	if !policyConfig.IsActionEnabled(action) {
		if policyConfig.DebugLogging {
			log.Debugf(ctx, "OPAPolicyChecker: action %q not in configured actions %v, skipping", action, policyConfig.Actions)
		}
		return nil
	}

	if !policyConfig.Enabled {
		log.Debug(ctx, "OPAPolicyChecker: selected policy is disabled, skipping")
		return nil
	}

	if ev == nil {
		return model.NewBadReqErr(fmt.Errorf("policy evaluator is not initialized"))
	}

	if policyConfig.DebugLogging {
		if e.config.NetworkPolicyConfig != "" {
			log.Debugf(ctx, "OPAPolicyChecker: evaluating policy for networkID=%q action=%q (modules=%v)", selectedNetworkID, action, ev.ModuleNames())
		} else {
			log.Debugf(ctx, "OPAPolicyChecker: evaluating policies for action %q (modules=%v)", action, ev.ModuleNames())
		}
	}

	violations, err := ev.Evaluate(ctx, ctx.Body)
	if err != nil {
		if e.config.NetworkPolicyConfig != "" {
			log.Errorf(ctx, err, "OPAPolicyChecker: policy evaluation failed for networkID=%q: %v", selectedNetworkID, err)
		} else {
			log.Errorf(ctx, err, "OPAPolicyChecker: policy evaluation failed: %v", err)
		}
		return model.NewBadReqErr(fmt.Errorf("policy evaluation error: %w", err))
	}

	if len(violations) == 0 {
		if e.config.DebugLogging {
			log.Debugf(ctx, "OPAPolicyChecker: message compliant for action %q", action)
		}
		return nil
	}

	msg := fmt.Sprintf("policy violation(s): %s", strings.Join(violations, "; "))
	if e.config.NetworkPolicyConfig != "" {
		log.Warnf(ctx, "OPAPolicyChecker: networkID=%q %s", selectedNetworkID, msg)
	} else {
		log.Warnf(ctx, "OPAPolicyChecker: %s", msg)
	}
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

func isBecknDirection(part string) bool {
	switch part {
	case "caller", "receiver", "reciever":
		return true
	default:
		return false
	}
}
