package policyenforcer

import (
	"fmt"
	"os"
	"strings"
)

// Config holds the configuration for the Policy Enforcer plugin.
type Config struct {
	// PolicyPaths is a list of policy sources. Each entry is auto-detected as:
	//   - Remote URL (http:// or https://) → fetched via HTTP
	//   - Local directory → all .rego files loaded (excluding _test.rego)
	//   - Local file → loaded directly
	// Parsed from the comma-separated "policyPaths" config key.
	PolicyPaths []string

	// Query is the Rego query that returns a set of violation strings.
	// Default: "data.policy.violations".
	Query string

	// Actions is the list of beckn actions to enforce policies on.
	// When empty or nil, all actions are considered and the Rego policy
	// is responsible for deciding which actions to gate.
	Actions []string

	// Enabled controls whether the plugin is active.
	Enabled bool

	// DebugLogging enables verbose logging.
	DebugLogging bool

	// RuntimeConfig holds arbitrary key-value pairs passed to Rego as data.config.
	// Keys like minDeliveryLeadHours are forwarded here.
	RuntimeConfig map[string]string
}

// Known config keys that are handled directly (not forwarded to RuntimeConfig).
var knownKeys = map[string]bool{
	"policyPaths":  true,
	"query":        true,
	"actions":      true,
	"enabled":      true,
	"debugLogging": true,
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Query:         "data.policy.violations",
		Enabled:       true,
		DebugLogging:  false,
		RuntimeConfig: make(map[string]string),
	}
}

// ParseConfig parses the plugin configuration map into a Config struct.
func ParseConfig(cfg map[string]string) (*Config, error) {
	config := DefaultConfig()

	// Comma-separated policyPaths (each entry auto-detected as URL, directory, or file)
	if paths, ok := cfg["policyPaths"]; ok && paths != "" {
		for _, p := range strings.Split(paths, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				config.PolicyPaths = append(config.PolicyPaths, p)
			}
		}
	}

	if len(config.PolicyPaths) == 0 {
		// Fall back to the default ./policies directory if it exists on disk.
		if info, err := os.Stat("./policies"); err == nil && info.IsDir() {
			config.PolicyPaths = append(config.PolicyPaths, "./policies")
		} else {
			return nil, fmt.Errorf("at least one policy source is required (policyPaths)")
		}
	}

	if query, ok := cfg["query"]; ok && query != "" {
		config.Query = query
	}

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

	// Forward unknown keys to RuntimeConfig (e.g., minDeliveryLeadHours)
	for k, v := range cfg {
		if !knownKeys[k] {
			config.RuntimeConfig[k] = v
		}
	}

	return config, nil
}

// IsActionEnabled checks if the given action is in the configured actions list.
// When the actions list is empty/nil, all actions are enabled and action-gating
// is delegated entirely to the Rego policy.
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
