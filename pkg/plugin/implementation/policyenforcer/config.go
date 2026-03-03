package policyenforcer

import (
	"fmt"
	"os"
	"strings"
)

// Config holds the configuration for the Policy Enforcer plugin.
type Config struct {
	// PolicyDir is a local directory containing .rego policy files (all loaded).
	// At least one policy source (PolicyDir, PolicyFile, or PolicyUrls) is required.
	PolicyDir string

	// PolicyFile is a single local .rego file path.
	PolicyFile string

	// PolicyUrls is a list of URLs (or local file paths) pointing to .rego files,
	// fetched at startup or read from disk.
	// Parsed from the comma-separated "policyUrls" config key.
	PolicyUrls []string

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
	"policyDir":    true,
	"policyFile":   true,
	"policyUrls":   true,
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

	if dir, ok := cfg["policyDir"]; ok && dir != "" {
		config.PolicyDir = dir
	}
	if file, ok := cfg["policyFile"]; ok && file != "" {
		config.PolicyFile = file
	}

	// Comma-separated policyUrls (supports URLs, local files, and directory paths)
	if urls, ok := cfg["policyUrls"]; ok && urls != "" {
		for _, u := range strings.Split(urls, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				config.PolicyUrls = append(config.PolicyUrls, u)
			}
		}
	}

	if config.PolicyDir == "" && config.PolicyFile == "" && len(config.PolicyUrls) == 0 {
		// Fall back to the default ./policies directory if it exists on disk.
		if info, err := os.Stat("./policies"); err == nil && info.IsDir() {
			config.PolicyDir = "./policies"
		} else {
			return nil, fmt.Errorf("at least one policy source is required (policyDir, policyFile, or policyUrls)")
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
