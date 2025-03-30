package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"

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

// parseTargetURL parses a URL string into a url.URL object with strict validation
func parseTargetURL(urlStr string) (*url.URL, error) {
	if urlStr == "" {
		return nil, nil
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL '%s': %w", urlStr, err)
	}

	// Enforce scheme requirement
	if parsed.Scheme == "" {
		return nil, fmt.Errorf("URL '%s' must include a scheme (http/https)", urlStr)
	}

	// Optionally validate scheme is http or https
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("URL '%s' must use https scheme", urlStr)
	}

	return parsed, nil
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
			r.rules[rule.Domain] = make(map[string]map[string]*model.Route)
		}

		// Initialize version map if not exists
		if _, ok := r.rules[rule.Domain][rule.Version]; !ok {
			r.rules[rule.Domain][rule.Version] = make(map[string]*model.Route)
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
				parsedURL, err := parseTargetURL(rule.Target.URL)
				if err != nil {
					return fmt.Errorf("invalid URL in rule: %w", err)
				}
				route = &model.Route{
					TargetType: rule.TargetType,
					URL:        parsedURL,
				}
			case targetTypeBPP, targetTypeBAP:
				var parsedURL *url.URL
				if rule.Target.URL != "" {
					parsedURL, err = parseTargetURL(rule.Target.URL)
					if err != nil {
						return fmt.Errorf("invalid URL in rule: %w", err)
					}
				}
				route = &model.Route{
					TargetType: rule.TargetType,
					URL:        parsedURL,
				}
			}
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
			if _, err := parseTargetURL(rule.Target.URL); err != nil {
				return fmt.Errorf("invalid URL - %s: %w", rule.Target.URL, err)
			}
		case targetTypePublisher:
			if rule.Target.PublisherID == "" {
				return fmt.Errorf("invalid rule: publisherID is required for targetType 'publisher'")
			}
		case targetTypeBPP, targetTypeBAP:
			if rule.Target.URL != "" {
				if _, err := parseTargetURL(rule.Target.URL); err != nil {
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

// Route determines the routing destination based on the request context.
func (r *Router) Route(ctx context.Context, url *url.URL, body []byte) (*model.Route, error) {
	if r == nil {

		log.Debug(ctx, "In Router :Router not set")
	}
	log.Debugf(ctx, "In Router: Routing request with url %v and body: %s", url, string(body))
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
	log.Debugf(ctx, "In Router: Routing request with %v and body: %#s", url, requestBody)

	// Extract the endpoint from the URL
	endpoint := path.Base(url.Path)

	if r.rules == nil {

		log.Debug(ctx, "In Router :Routing rules not set")
	}
	log.Debugf(ctx, "In Router :Routing rules len :%d", len(r.rules))

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
		return handleProtocolMapping(route, requestBody.Context.BPPURI, endpoint)
	case targetTypeBAP:
		return handleProtocolMapping(route, requestBody.Context.BAPURI, endpoint)
	}
	return route, nil
}

// handleProtocolMapping handles both BPP and BAP routing with proper URL construction
func handleProtocolMapping(route *model.Route, requestURI, endpoint string) (*model.Route, error) {
	uri := strings.TrimSpace(requestURI)
	var targetURL *url.URL
	if len(uri) != 0 {
		parsedURL, err := parseTargetURL(uri)
		if err != nil {
			return nil, fmt.Errorf("invalid %s URI - %s in request body for %s: %w", strings.ToUpper(route.TargetType), uri, endpoint, err)
		}
		targetURL = parsedURL
	}

	// If no request URI, fall back to configured URL with endpoint appended
	if targetURL == nil {
		if route.URL == nil {
			return nil, fmt.Errorf("could not determine destination for endpoint '%s': neither request contained a %s URI nor was a default URL configured in routing rules", endpoint, strings.ToUpper(route.TargetType))
		}

		targetURL = &url.URL{
			Scheme: route.URL.Scheme,
			Host:   route.URL.Host,
			Path:   path.Join(route.URL.Path, endpoint),
		}
	}

	return &model.Route{
		TargetType: targetTypeURL,
		URL:        targetURL,
	}, nil
}
