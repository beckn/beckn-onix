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

// Router implements Router interface.
type Router struct {
	rules map[string]map[string]map[string]*definition.Route // domain -> version -> endpoint -> route
}

// RoutingRule represents a single routing rule.
type routingRule struct {
	Domain     string   `yaml:"domain"`
	Version    string   `yaml:"version"`
	TargetType string   `yaml:"targetType"` // "url", "msgq", "bpp", or "bap"
	Target     target   `yaml:"target,omitempty"`
	Endpoints  []string `yaml:"endpoints"`
}

// Target contains destination-specific details.
type target struct {
	URL         string `yaml:"url,omitempty"`         // URL for "url" or gateway endpoint for "bpp"/"bap"
	PublisherID string `yaml:"publisherId,omitempty"` // For "msgq" type
}

// TargetType defines possible target destinations.
const (
	targetTypeURL  = "url"  // Route to a specific URL
	targetTypeMSGQ = "msgq" // Route to a message queue
	targetTypeBPP  = "bpp"  // Route to a BPP endpoint
	targetTypeBAP  = "bap"  // Route to a BAP endpoint
)

// New initializes a new Router instance with the provided configuration.
// It loads and validates the routing rules from the specified YAML file.
// Returns an error if the configuration is invalid or the rules cannot be loaded.
func New(ctx context.Context, config *Config) (*Router, func() error, error) {
	// Check if config is nil
	if config == nil {
		return nil, nil, fmt.Errorf("config cannot be nil")
	}
	router := &Router{
		rules: make(map[string]map[string]map[string]*definition.Route),
	}

	// Load rules at bootup
	if err := router.loadRules(config.RoutingConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to load routing rules: %w", err)
	}
	return router, nil, nil
}

// LoadRules reads and parses routing rules from the YAML configuration file.
func (r *Router) loadRules(configPath string) error {
	if configPath == "" {
		return fmt.Errorf("routingConfig path is empty")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading config file at %s: %w", configPath, err)
	}
	var config routingConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("error parsing YAML: %w", err)
	}

	// Validate rules
	if err := validateRules(config.RoutingRules); err != nil {
		return fmt.Errorf("invalid routing rules: %w", err)
	}
	// Build the optimized rule map
	for _, rule := range config.RoutingRules {
		// Initialize domain map if not exists
		if _, ok := r.rules[rule.Domain]; !ok {
			r.rules[rule.Domain] = make(map[string]map[string]*definition.Route)
		}

		// Initialize version map if not exists
		if _, ok := r.rules[rule.Domain][rule.Version]; !ok {
			r.rules[rule.Domain][rule.Version] = make(map[string]*definition.Route)
		}

		// Add all endpoints for this rule
		for _, endpoint := range rule.Endpoints {
			var route *definition.Route
			switch rule.TargetType {
			case targetTypeMSGQ:
				route = &definition.Route{
					TargetType:  rule.TargetType,
					PublisherID: rule.Target.PublisherID,
				}
			case targetTypeURL:
				route = &definition.Route{
					TargetType: rule.TargetType,
					URL:        rule.Target.URL,
				}
			case targetTypeBPP, targetTypeBAP:
				route = &definition.Route{
					TargetType: rule.TargetType,
					URL:        rule.Target.URL, // Fallback URL if URI not provided in request
				}
			}

			fmt.Print(r.rules)

			r.rules[rule.Domain][rule.Version][endpoint] = route
		}
	}

	return nil
}

// validateRules performs basic validation on the loaded routing rules.
func validateRules(rules []routingRule) error {
	for _, rule := range rules {
		// Ensure domain, version, and TargetType are present
		if rule.Domain == "" || rule.Version == "" || rule.TargetType == "" {
			return fmt.Errorf("invalid rule: domain, version, and targetType are required")
		}

		// Validate based on TargetType
		switch rule.TargetType {
		case targetTypeURL:
			if rule.Target.URL == "" {
				return fmt.Errorf("invalid rule: url is required for targetType 'url'")
			}
			if _, err := url.ParseRequestURI(rule.Target.URL); err != nil {
				return fmt.Errorf("invalid URL in rule: %w", err)
			}
		case targetTypeMSGQ:
			if rule.Target.PublisherID == "" {
				return fmt.Errorf("invalid rule: publisherID is required for targetType 'msgq'")
			}
		case targetTypeBPP, targetTypeBAP:
			// No target validation needed for bpp/bap, as they use URIs from the request body
			continue
		default:
			return fmt.Errorf("invalid rule: unknown targetType '%s'", rule.TargetType)
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
			BPPURI  string `json:"bpp_uri,omitempty"`
			BAPURI  string `json:"bap_uri,omitempty"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &requestBody); err != nil {
		return nil, fmt.Errorf("error parsing request body: %w", err)
	}

	// Extract the endpoint from the URL
	endpoint := path.Base(url.Path)

	// Lookup route in the optimized map
	domainRules, ok := r.rules[requestBody.Context.Domain]
	if !ok {
		return nil, fmt.Errorf("no routing rules found for domain %s", requestBody.Context.Domain)
	}

	versionRules, ok := domainRules[requestBody.Context.Version]
	if !ok {
		return nil, fmt.Errorf("no routing rules found for domain %s version %s", requestBody.Context.Domain, requestBody.Context.Version)
	}

	route, ok := versionRules[endpoint]
	if !ok {
		return nil, fmt.Errorf("endpoint '%s' is not supported for domain %s and version %s in routing config",
			endpoint, requestBody.Context.Domain, requestBody.Context.Version)
	}

	// Handle BPP/BAP routing with request URIs
	switch route.TargetType {
	case targetTypeBPP:
		uri := strings.TrimSpace(requestBody.Context.BPPURI)
		target := strings.TrimSpace(route.URL)
		if len(uri) != 0 {
			target = uri
		}
		if len(target) == 0 {
			return nil, fmt.Errorf("could not determine destination for endpoint '%s': neither request contained a BPP URI nor was a default URL configured in routing rules", endpoint)
		}
		route = &definition.Route{
			TargetType: route.TargetType,
			URL:        target,
		}
	case targetTypeBAP:
		uri := strings.TrimSpace(requestBody.Context.BAPURI)
		target := strings.TrimSpace(route.URL)
		if len(uri) != 0 {
			target = uri
		}
		if len(target) == 0 {
			return nil, fmt.Errorf("could not determine destination for endpoint '%s': neither request contained a BAP URI nor was a default URL configured in routing rules", endpoint)
		}
		route = &definition.Route{
			TargetType: route.TargetType,
			URL:        target,
		}
	}

	return route, nil
}
