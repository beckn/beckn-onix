package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	definition "github.com/beckn/beckn-onix/pkg/plugin/definition"

	"gopkg.in/yaml.v3"
)

// Config holds the configuration for the Router plugin.
type Config struct {
	RoutingConfig string `json:"routing_config"`
}

// RoutingConfig represents the structure of the routing configuration file.
type routingConfig struct {
	RoutingRules []routingRule `yaml:"routing_rules"`
}

// Router implements Router interface
type Router struct {
	config *Config
	rules  []routingRule
}

// RoutingRule represents a single routing rule.
type routingRule struct {
	Domain      string   `yaml:"domain"`
	Version     string   `yaml:"version"`
	RoutingType string   `yaml:"routing_type"` // "url" or "msgq"
	Target      target   `yaml:"target"`
	Endpoints   []string `yaml:"endpoints"`
}

// Target contains destination-specific details.
type target struct {
	URL     string `yaml:"url,omitempty"`      // For "url" type
	TopicID string `yaml:"topic_id,omitempty"` // For "msgq" type
}

// New initializes a new ProxyRouter instance.
func New(ctx context.Context, config *Config) (*Router, func() error, error) {
	// Check if config is nil
	if config == nil {
		return nil, nil, fmt.Errorf("config cannot be nil")
	}
	router := &Router{
		config: config,
	}

	// Load rules at bootup
	if err := router.loadRules(); err != nil {
		return nil, nil, fmt.Errorf("failed to load routing rules: %w", err)
	}
	return router, router.Close, nil
}

// LoadRules reads and parses routing rules from the YAML configuration file.
func (r *Router) loadRules() error {
	if r.config.RoutingConfig == "" {
		return fmt.Errorf("routing_config path is empty")
	}
	data, err := os.ReadFile(r.config.RoutingConfig)
	if err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}
	var config routingConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("error parsing YAML: %w", err)
	}

	// Validate rules
	if err := validateRules(config.RoutingRules); err != nil {
		return fmt.Errorf("invalid routing rules: %w", err)
	}
	r.rules = config.RoutingRules
	return nil
}

// validateRules performs basic validation on the loaded routing rules.
func validateRules(rules []routingRule) error {
	for _, rule := range rules {
		if rule.Domain == "" || rule.Version == "" || rule.RoutingType == "" {
			return fmt.Errorf("invalid rule: domain, version, and routing_type are required")
		}

		switch rule.RoutingType {
		case "url":
			if rule.Target.URL == "" {
				return fmt.Errorf("invalid rule: url is required for routing_type 'url'")
			}
		case "msgq":
			if rule.Target.TopicID == "" {
				return fmt.Errorf("invalid rule: topic_id is required for routing_type 'msgq'")
			}
		default:
			return fmt.Errorf("invalid rule: unknown routing_type '%s'", rule.RoutingType)
		}
	}
	return nil
}

// Route determines the routing destination based on the request context.
func (r *Router) Route(ctx context.Context, url *url.URL, body []byte) (*definition.Route, error) {

	// Parse the body to extract domain and version
	var requestBody struct {
		Context struct {
			Domain  string `json:"domain"`
			Version string `json:"version"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &requestBody); err != nil {
		return nil, fmt.Errorf("error parsing request body: %w", err)
	}

	// Match the rule
	for _, rule := range r.rules {
		if rule.Domain == requestBody.Context.Domain && rule.Version == requestBody.Context.Version {
			// Check if the endpoint matches
			endpoint := path.Base(url.Path)
			for _, ep := range rule.Endpoints {
				if strings.EqualFold(ep, endpoint) {
					return &definition.Route{
						RoutingType: rule.RoutingType,
						TopicID:     rule.Target.TopicID,
						TargetURL:   rule.Target.URL,
					}, nil
				}
			}

			// If domain and version match but endpoint is not found, return an error
			return nil, fmt.Errorf("endpoint '%s' is not supported for domain %s and version %s", endpoint, requestBody.Context.Domain, requestBody.Context.Version)
		}
	}

	// return nil, fmt.Errorf("no matching routing rule found for domain %s and version %s", requestBody.Context.Domain, requestBody.Context.Version)
	return nil, fmt.Errorf("no matching routing rule found for domain %s and version %s", requestBody.Context.Domain, requestBody.Context.Version)
}

// Close releases resources (mock implementation returning nil).
func (r *Router) Close() error {
	return nil
}
