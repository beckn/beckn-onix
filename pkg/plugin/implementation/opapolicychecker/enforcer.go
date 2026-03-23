package opapolicychecker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
)

// Config holds the configuration for the OPA Policy Checker plugin.
type Config struct {
	Type            string
	Location        string
	PolicyPaths     []string
	Query           string
	Actions         []string
	Enabled         bool
	DebugLogging    bool
	IsBundle        bool
	RefreshInterval time.Duration // 0 = disabled
	RuntimeConfig   map[string]string
}

var knownKeys = map[string]bool{
	"type":                   true,
	"location":               true,
	"query":                  true,
	"actions":                true,
	"enabled":                true,
	"debugLogging":           true,
	"refreshIntervalSeconds": true,
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		RuntimeConfig: make(map[string]string),
	}
}

// ParseConfig parses the plugin configuration map into a Config struct.
// Uses type + location pattern (matches schemav2validator).
func ParseConfig(cfg map[string]string) (*Config, error) {
	config := DefaultConfig()

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

// PolicyEnforcer evaluates beckn messages against OPA policies and NACKs non-compliant messages.
type PolicyEnforcer struct {
	config        *Config
	evaluator     *Evaluator
	evaluatorMu   sync.RWMutex
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

func New(ctx context.Context, cfg map[string]string) (*PolicyEnforcer, error) {
	config, err := ParseConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("opapolicychecker: config error: %w", err)
	}

	evaluator, err := NewEvaluator(config.PolicyPaths, config.Query, config.RuntimeConfig, config.IsBundle)
	if err != nil {
		return nil, fmt.Errorf("opapolicychecker: failed to initialize OPA evaluator: %w", err)
	}

	log.Infof(ctx, "OPAPolicyChecker initialized (actions=%v, query=%s, policies=%v, isBundle=%v, debugLogging=%v, refreshInterval=%s)",
		config.Actions, config.Query, evaluator.ModuleNames(), config.IsBundle, config.DebugLogging, config.RefreshInterval)

	enforcer := &PolicyEnforcer{
		config:    config,
		evaluator: evaluator,
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
		case <-ticker.C:
			e.reloadPolicies(ctx)
		}
	}
}

// reloadPolicies reloads and recompiles all policies, atomically swapping the evaluator.
// Reload failures are non-fatal; the old evaluator stays active.
func (e *PolicyEnforcer) reloadPolicies(ctx context.Context) {
	start := time.Now()
	newEvaluator, err := NewEvaluator(
		e.config.PolicyPaths,
		e.config.Query,
		e.config.RuntimeConfig,
		e.config.IsBundle,
	)
	if err != nil {
		log.Errorf(ctx, err, "OPAPolicyChecker: policy reload failed (keeping previous policies): %v", err)
		return
	}

	e.setEvaluator(newEvaluator)
	log.Infof(ctx, "OPAPolicyChecker: policies reloaded in %s (modules=%v)", time.Since(start), newEvaluator.ModuleNames())
}

// CheckPolicy evaluates the message body against loaded OPA policies.
// Returns a BadReqErr (causing NACK) if violations are found.
// Returns an error on evaluation failure (fail closed).
func (e *PolicyEnforcer) CheckPolicy(ctx *model.StepContext) error {
	if !e.config.Enabled {
		log.Debug(ctx, "OPAPolicyChecker: plugin disabled, skipping")
		return nil
	}

	action := extractAction(ctx.Request.URL.Path, ctx.Body)

	if !e.config.IsActionEnabled(action) {
		if e.config.DebugLogging {
			log.Debugf(ctx, "OPAPolicyChecker: action %q not in configured actions %v, skipping", action, e.config.Actions)
		}
		return nil
	}

	ev := e.getEvaluator()

	if e.config.DebugLogging {
		log.Debugf(ctx, "OPAPolicyChecker: evaluating policies for action %q (modules=%v)", action, ev.ModuleNames())
	}

	violations, err := ev.Evaluate(ctx, ctx.Body)
	if err != nil {
		log.Errorf(ctx, err, "OPAPolicyChecker: policy evaluation failed: %v", err)
		return model.NewBadReqErr(fmt.Errorf("policy evaluation error: %w", err))
	}

	if len(violations) == 0 {
		if e.config.DebugLogging {
			log.Debugf(ctx, "OPAPolicyChecker: message compliant for action %q", action)
		}
		return nil
	}

	msg := fmt.Sprintf("policy violation(s): %s", strings.Join(violations, "; "))
	log.Warnf(ctx, "OPAPolicyChecker: %s", msg)
	return model.NewBadReqErr(fmt.Errorf("%s", msg))
}

func (e *PolicyEnforcer) Close() {}

func extractAction(urlPath string, body []byte) string {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) >= 3 {
		return parts[len(parts)-1]
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
