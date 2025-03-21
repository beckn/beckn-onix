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
	RoutingConfig string `json:"routingConfig"`
}

// RoutingConfig represents the structure of the routing configuration file.
type routingConfig struct {
	RoutingRules []routingRule `yaml:"routingRules"`
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
	RoutingType string   `yaml:"routingType"` // "url", "msgq", "bpp", or "bap"
	Target      target   `yaml:"target,omitempty"`
	Endpoints   []string `yaml:"endpoints"`
}

// Target contains destination-specific details.
type target struct {
	URL     string `yaml:"url,omitempty"`      // URL for "url" or gateway endpoint for "bpp"/"bap"
	TopicID string `yaml:"topic_id,omitempty"` // For "msgq" type
}

// New initializes a new Router instance with the provided configuration.
// It loads and validates the routing rules from the specified YAML file.
// Returns an error if the configuration is invalid or the rules cannot be loaded.
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
	return router, nil, nil
}

// LoadRules reads and parses routing rules from the YAML configuration file.
func (r *Router) loadRules() error {
	if r.config.RoutingConfig == "" {
		return fmt.Errorf("routingConfig path is empty")
	}
	data, err := os.ReadFile(r.config.RoutingConfig)
	if err != nil {
		return fmt.Errorf("error reading config file at %s: %w", r.config.RoutingConfig, err)
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
		// Ensure domain, version, and routingType are present
		if rule.Domain == "" || rule.Version == "" || rule.RoutingType == "" {
			return fmt.Errorf("invalid rule: domain, version, and routingType are required")
		}

		// Validate based on routingType
		switch rule.RoutingType {
		case "url":
			if rule.Target.URL == "" {
				return fmt.Errorf("invalid rule: url is required for routingType 'url'")
			}
			if _, err := url.ParseRequestURI(rule.Target.URL); err != nil {
				return fmt.Errorf("invalid URL in rule: %w", err)
			}
		case "msgq":
			if rule.Target.TopicID == "" {
				return fmt.Errorf("invalid rule: topicId is required for routingType 'msgq'")
			}
		case "bpp", "bap":
			// No target validation needed for bpp/bap, as they use URIs from the request body
			continue
		default:
			return fmt.Errorf("invalid rule: unknown routingType '%s'", rule.RoutingType)
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
			BppURI  string `json:"bpp_uri,omitempty"`
			BapURI  string `json:"bap_uri,omitempty"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &requestBody); err != nil {
		return nil, fmt.Errorf("error parsing request body: %w", err)
	}

	// Extract the endpoint from the URL
	endpoint := path.Base(url.Path)

	// Collect all matching rules for the domain and version
	matchingRules := r.getMatchingRules(requestBody.Context.Domain, requestBody.Context.Version)

	// If no matching rules are found, return an error
	if len(matchingRules) == 0 {
		return nil, fmt.Errorf("no matching routing rule found for domain %s and version %s", requestBody.Context.Domain, requestBody.Context.Version)
	}

	// Match the rule
	for _, rule := range matchingRules {
		for _, ep := range rule.Endpoints {
			if strings.EqualFold(ep, endpoint) {
				switch rule.RoutingType {
				case "msgq":
					return &definition.Route{
						RoutingType: rule.RoutingType,
						TopicID:     rule.Target.TopicID,
					}, nil
				case "url":
					return &definition.Route{
						RoutingType: rule.RoutingType,
						TargetURL:   rule.Target.URL,
					}, nil
				case "bpp":
					return handleRouting(rule, requestBody.Context.BppURI, endpoint, "bpp")
				case "bap":
					return handleRouting(rule, requestBody.Context.BapURI, endpoint, "bap")
				default:
					return nil, fmt.Errorf("unsupported routingType: %s", rule.RoutingType)
				}
			}
		}
	}

	// If domain and version match but endpoint is not found, return an error
	return nil, fmt.Errorf("endpoint '%s' is not supported for domain %s and version %s", endpoint, requestBody.Context.Domain, requestBody.Context.Version)
}

// getMatchingRules returns all rules that match the given domain and version
func (r *Router) getMatchingRules(domain, version string) []routingRule {
	var matchingRules []routingRule
	for _, rule := range r.rules {
		if rule.Domain == domain && rule.Version == version {
			matchingRules = append(matchingRules, rule)
		}
	}
	return matchingRules
}

// handleRouting handles routing for bap and bpp routing type
func handleRouting(rule routingRule, uri, endpoint string, routingType string) (*definition.Route, error) {
	if uri == "" {
		if rule.Target.URL != "" {
			return &definition.Route{
				RoutingType: routingType,
				TargetURL:   rule.Target.URL,
			}, nil
		} else {
			return nil, fmt.Errorf("no target URI or URL found for %s routing type and %s endpoint", routingType, endpoint)
		}
	}
	return &definition.Route{
		RoutingType: routingType,
		TargetURL:   uri,
	}, nil
}
