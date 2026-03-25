package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/model"

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
	rules map[string]map[string]map[string]*model.Route // domain -> version -> endpoint -> route
}

// RoutingRule represents a single routing rule.
type routingRule struct {
	Domain     string   `yaml:"domain"`
	Version    string   `yaml:"version"`
	TargetType string   `yaml:"targetType"` // "url", "publisher", "bpp", or "bap"
	Target     target   `yaml:"target,omitempty"`
	Endpoints  []string `yaml:"endpoints"`
}

// Target contains destination-specific details.
type target struct {
	URL         string `yaml:"url,omitempty"`         // URL for "url" or gateway endpoint for "bpp"/"bap"
	PublisherID string `yaml:"publisherId,omitempty"` // For "msgq" type
	ExcludeAction bool `yaml:"excludeAction,omitempty"` // For "url" type to exclude appending action to URL path
}

// TargetType defines possible target destinations.
const (
	targetTypeURL       = "url"       // Route to a specific URL
	targetTypePublisher = "publisher" // Route to a publisher
	targetTypeBPP       = "bpp"       // Route to a BPP endpoint
	targetTypeBAP       = "bap"       // Route to a BAP endpoint
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
		rules: make(map[string]map[string]map[string]*model.Route),
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
		// For v2.x.x, warn if domain is provided and normalize to wildcard "*"
		domain := rule.Domain
		if isV2Version(rule.Version) {
			if domain != "" {
				fmt.Printf("WARNING: Domain field '%s' is not needed for version %s and will be ignored. Consider removing it from your config.\n", domain, rule.Version)
			}
			domain = "*"
		}

		// Initialize domain map if not exists
		if _, ok := r.rules[domain]; !ok {
			r.rules[domain] = make(map[string]map[string]*model.Route)
		}

		// Initialize version map if not exists
		if _, ok := r.rules[domain][rule.Version]; !ok {
			r.rules[domain][rule.Version] = make(map[string]*model.Route)
		}

		// Add all endpoints for this rule
		for _, endpoint := range rule.Endpoints {
			var route *model.Route
			switch rule.TargetType {
			case targetTypePublisher:
				route = &model.Route{
					TargetType:  rule.TargetType,
					PublisherID: rule.Target.PublisherID,
				}
			case targetTypeURL:
				parsedURL, err := url.Parse(rule.Target.URL)
				if err != nil {
					return fmt.Errorf("invalid URL in rule: %w", err)
				}
				if !rule.Target.ExcludeAction {
					parsedURL.Path = joinPath(parsedURL, endpoint)
				}
				route = &model.Route{
					TargetType: rule.TargetType,
					URL:        parsedURL,
				}
			case targetTypeBPP, targetTypeBAP:
				var parsedURL *url.URL
				if rule.Target.URL != "" {
					parsedURL, err = url.Parse(rule.Target.URL)
					if err != nil {
						return fmt.Errorf("invalid URL in rule: %w", err)
					}
					parsedURL.Path = joinPath(parsedURL, endpoint)
				}
				route = &model.Route{
					TargetType: rule.TargetType,
					URL:        parsedURL,
				}
			}
			// Check for conflicting v2 rules
			if isV2Version(rule.Version) {
				if _, exists := r.rules[domain][rule.Version][endpoint]; exists {
					return fmt.Errorf("duplicate endpoint '%s' found for version %s. For v2.x.x, domain is ignored, so you can only define each endpoint once per version. Please remove the duplicate rule", endpoint, rule.Version)
				}
			}
			r.rules[domain][rule.Version][endpoint] = route
		}
	}

	return nil
}

// validateRules performs basic validation on the loaded routing rules.
func validateRules(rules []routingRule) error {
	for _, rule := range rules {
		// Ensure version and TargetType are present
		if rule.Version == "" || rule.TargetType == "" {
			return fmt.Errorf("invalid rule: version and targetType are required")
		}

		// Domain is required only for v1.x.x
		if !isV2Version(rule.Version) && rule.Domain == "" {
			return fmt.Errorf("invalid rule: domain is required for version %s", rule.Version)
		}

		// Validate based on TargetType
		switch rule.TargetType {
		case targetTypeURL:
			if rule.Target.URL == "" {
				return fmt.Errorf("invalid rule: url is required for targetType 'url'")
			}
			if _, err := url.Parse(rule.Target.URL); err != nil {
				return fmt.Errorf("invalid URL - %s: %w", rule.Target.URL, err)
			}
		case targetTypePublisher:
			if rule.Target.PublisherID == "" {
				return fmt.Errorf("invalid rule: publisherID is required for targetType 'publisher'")
			}
		case targetTypeBPP, targetTypeBAP:
			if rule.Target.URL != "" {
				if _, err := url.Parse(rule.Target.URL); err != nil {
					return fmt.Errorf("invalid URL - %s defined in routing config for target type %s: %w", rule.Target.URL, rule.TargetType, err)
				}
			}
			continue
		default:
			return fmt.Errorf("invalid rule: unknown targetType '%s'", rule.TargetType)
		}
	}
	return nil
}

// getContextString returns the value for a context field, checking the snake_case
// key first and falling back to the camelCase key. This supports both the legacy
// beckn spec (snake_case) and the new camelCase convention transparently.
func getContextString(ctx map[string]interface{}, snakeKey, camelKey string) string {
	if v, ok := ctx[snakeKey].(string); ok && v != "" {
		return v
	}
	if v, ok := ctx[camelKey].(string); ok && v != "" {
		return v
	}
	return ""
}

// Route determines the routing destination based on the request context.
func (r *Router) Route(ctx context.Context, url *url.URL, body []byte) (*model.Route, error) {
	// Parse domain and version via typed struct — unchanged from original.
	var requestBody struct {
		Context struct {
			Domain  string `json:"domain"`
			Version string `json:"version"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &requestBody); err != nil {
		return nil, fmt.Errorf("error parsing request body: %w", err)
	}

	// Parse context as a map solely to resolve URI fields that have both
	// snake_case (bpp_uri, bap_uri) and camelCase (bppUri, bapUri) variants.
	var uriBody struct {
		Context map[string]interface{} `json:"context"`
	}
	if err := json.Unmarshal(body, &uriBody); err != nil {
		return nil, fmt.Errorf("error parsing request body: %w", err)
	}
	if uriBody.Context == nil {
		return nil, fmt.Errorf("context field not found or invalid in request body")
	}
	bppURI := getContextString(uriBody.Context, "bpp_uri", "bppUri")
	bapURI := getContextString(uriBody.Context, "bap_uri", "bapUri")

	// Extract the endpoint from the URL
	endpoint := path.Base(url.Path)

	// For v2.x.x, ignore domain and use wildcard; for v1.x.x, use actual domain
	domain := requestBody.Context.Domain
	if isV2Version(requestBody.Context.Version) {
		domain = "*"
	}

	// Lookup route in the optimized map
	domainRules, ok := r.rules[domain]
	if !ok {
		if domain == "*" {
			return nil, fmt.Errorf("no routing rules found for version %s", requestBody.Context.Version)
		}
		return nil, fmt.Errorf("no routing rules found for domain %s", requestBody.Context.Domain)
	}

	versionRules, ok := domainRules[requestBody.Context.Version]
	if !ok {
		if domain == "*" {
			return nil, fmt.Errorf("no routing rules found for version %s", requestBody.Context.Version)
		}
		return nil, fmt.Errorf("no routing rules found for domain %s version %s", requestBody.Context.Domain, requestBody.Context.Version)
	}

	route, ok := versionRules[endpoint]
	if !ok {
		if domain == "*" {
			return nil, fmt.Errorf("endpoint '%s' is not supported for version %s in routing config", endpoint, requestBody.Context.Version)
		}
		return nil, fmt.Errorf("endpoint '%s' is not supported for domain %s and version %s in routing config",
			endpoint, requestBody.Context.Domain, requestBody.Context.Version)
	}
	// Handle BPP/BAP routing with request URIs
	switch route.TargetType {
	case targetTypeBPP:
		return handleProtocolMapping(route, bppURI, endpoint)
	case targetTypeBAP:
		return handleProtocolMapping(route, bapURI, endpoint)
	}
	return route, nil
}

// handleProtocolMapping handles both BPP and BAP routing with proper URL construction
func handleProtocolMapping(route *model.Route, npURI, endpoint string) (*model.Route, error) {
	target := strings.TrimSpace(npURI)
	if len(target) == 0 {
		if route.URL == nil {
			return nil, fmt.Errorf("could not determine destination for endpoint '%s': neither request contained a %s URI nor was a default URL configured in routing rules", endpoint, strings.ToUpper(route.TargetType))
		}
		return &model.Route{
			TargetType: targetTypeURL,
			URL:        route.URL,
		}, nil
	}
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid %s URI - %s in request body for %s: %w", strings.ToUpper(route.TargetType), target, endpoint, err)
	}
	targetURL.Path = joinPath(targetURL, endpoint)
	return &model.Route{
		TargetType: targetTypeURL,
		URL:        targetURL,
	}, nil
}

func joinPath(u *url.URL, endpoint string) string {
	if u.Path == "" {
		u.Path = "/"
	}
	return path.Join(u.Path, endpoint)
}

// isV2Version checks if the version is 2.x.x
func isV2Version(version string) bool {
	return strings.HasPrefix(version, "2.")
}