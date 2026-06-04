package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

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
	rules    map[string]map[string]map[string]*model.Route // domain -> version -> endpoint -> route
	registry definition.RegistryLookup                    // optional; used for NodeID-based URL resolution
}

// RoutingRule represents a single routing rule.
type routingRule struct {
	Domain     string   `yaml:"domain"`
	Version    string   `yaml:"version"`
	TargetType string   `yaml:"targetType"` // "url", "publisher", "bpp"/"receiver", or "bap"/"sender"
	Target     target   `yaml:"target,omitempty"`
	Endpoints  []string `yaml:"endpoints"`
}

// Target contains destination-specific details.
type target struct {
	URL           string `yaml:"url,omitempty"`           // URL for "url" or gateway endpoint for "bpp"/"bap"
	PublisherID   string `yaml:"publisherId,omitempty"`   // For "msgq" type
	ExcludeAction bool   `yaml:"excludeAction,omitempty"` // For "url" type to exclude appending action to URL path
}

// TargetType defines possible target destinations.
// The legacy values "bpp" and "bap" are retained for backward compatibility;
// new configs should use "receiver" and "sender" respectively.
const (
	targetTypeURL       = "url"       // Route to a specific URL
	targetTypePublisher = "publisher" // Route to a publisher
	targetTypeBPP       = "bpp"       // Route to a BPP endpoint (legacy; prefer "receiver")
	targetTypeBAP       = "bap"       // Route to a BAP endpoint (legacy; prefer "sender")
	targetTypeReceiver  = "receiver"  // Route to receiver (BPP) endpoint — Beckn spec v2 name
	targetTypeSender    = "sender"    // Route to sender (BAP) endpoint — Beckn spec v2 name
)

// New initializes a new Router instance with the provided configuration.
// It loads and validates the routing rules from the specified YAML file.
// registry is optional; when provided it is used to resolve subscriber URLs via NodeID lookup
// when no URI is present in the payload context.
// Returns an error if the configuration is invalid or the rules cannot be loaded.
func New(ctx context.Context, registry definition.RegistryLookup, config *Config) (*Router, func() error, error) {
	// Check if config is nil
	if config == nil {
		return nil, nil, fmt.Errorf("config cannot be nil")
	}
	router := &Router{
		rules:    make(map[string]map[string]map[string]*model.Route),
		registry: registry,
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
			case targetTypeBPP, targetTypeBAP, targetTypeReceiver, targetTypeSender:
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
		case targetTypeBPP, targetTypeBAP, targetTypeReceiver, targetTypeSender:
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

// getContextString returns the value for a context field, checking each key in
// order and returning the first non-empty string found. Supports snake_case
// (legacy), camelCase (Beckn spec v1 preferred), and the new Beckn spec v2
// camelCase names (e.g. senderUri, receiverUri) transparently.
func getContextString(ctx map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := ctx[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// Route determines the routing destination based on the request context.
// reqURL.Path holds the Beckn endpoint action already stripped of the module
// base path by the step layer (e.g. "search" or "catalog/subscription").
func (r *Router) Route(ctx context.Context, reqURL *url.URL, body []byte) (*model.Route, error) {
	endpoint := reqURL.Path
	// Bodyless requests (GET/DELETE) carry no JSON body; domain, version, and
	// BAP/BPP URIs are not available. Route using the v2 config version.
	if len(body) == 0 {
		return r.routeBodyless(endpoint)
	}

	// Parse domain and version via typed struct.
	var requestBody struct {
		Context struct {
			Domain  string `json:"domain"`
			Version string `json:"version"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &requestBody); err != nil {
		return nil, fmt.Errorf("error parsing request body: %w", err)
	}

	// Parse context as a map solely to resolve URI fields. Checks legacy
	// snake_case (bpp_uri, bap_uri), then camelCase (bppUri, bapUri), then
	// the new Beckn spec v2 names (receiverUri, senderUri).
	var uriBody struct {
		Context map[string]interface{} `json:"context"`
	}
	if err := json.Unmarshal(body, &uriBody); err != nil {
		return nil, fmt.Errorf("error parsing request body: %w", err)
	}
	if uriBody.Context == nil {
		return nil, fmt.Errorf("context field not found or invalid in request body")
	}
	bppURI := getContextString(uriBody.Context, "bpp_uri", "bppUri", "receiverUri")
	bapURI := getContextString(uriBody.Context, "bap_uri", "bapUri", "senderUri")
	bppNodeID := getContextString(uriBody.Context, "bpp_id", "bppId", "receiverId")
	bapNodeID := getContextString(uriBody.Context, "bap_id", "bapId", "senderId")

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
	// Handle BPP/BAP routing with request URIs.
	// Both legacy ("bpp"/"bap") and new spec v2 ("receiver"/"sender") values are accepted.
	switch route.TargetType {
	case targetTypeBPP, targetTypeReceiver:
		return handleProtocolMapping(ctx, route, bppURI, bppNodeID, endpoint, r.registry)
	case targetTypeBAP, targetTypeSender:
		return handleProtocolMapping(ctx, route, bapURI, bapNodeID, endpoint, r.registry)
	}
	return route, nil
}

// routeBodyless handles routing for GET/DELETE requests that carry no body.
// Since domain and version cannot be read from a missing payload, the version
// registered in the v2 wildcard config ("*") is used for the rules lookup.
//
// Supported target types:
//   - url:       routed to the configured static URL (primary use case).
//   - publisher: technically permitted — the empty-body message is forwarded
//     to the queue as-is. No known protocol use case exists for routing
//     catalog GET/DELETE requests through a message queue; this is allowed
//     rather than rejected to avoid unnecessary constraint, but operators
//     should not configure publisher targets for bodyless endpoints.
//   - bpp / bap / receiver / sender: rejected — the target URI is read from the request body,
//     which is absent for bodyless requests.
func (r *Router) routeBodyless(endpoint string) (*model.Route, error) {
	v2Rules, ok := r.rules["*"]
	if !ok {
		return nil, fmt.Errorf("no v2 routing rules found; bodyless requests require a v2 config")
	}

	for version, versionRules := range v2Rules {
		route, ok := versionRules[endpoint]
		if !ok {
			continue
		}
		switch route.TargetType {
		case targetTypeBPP, targetTypeBAP, targetTypeReceiver, targetTypeSender:
			return nil, fmt.Errorf("bodyless endpoint '%s' (version %s) is configured with target type '%s': dynamic BAP/BPP URI routing is not supported for bodyless requests", endpoint, version, route.TargetType)
		}
		return route, nil
	}

	return nil, fmt.Errorf("endpoint '%s' is not supported in v2 routing config", endpoint)
}

// canonicalRoleName returns a stable, human-readable role label for use in error
// messages regardless of which targetType alias ("bpp"/"receiver", "bap"/"sender")
// was used in the routing config.
func canonicalRoleName(targetType string) string {
	switch targetType {
	case targetTypeBPP, targetTypeReceiver:
		return "BPP"
	case targetTypeBAP, targetTypeSender:
		return "BAP"
	default:
		return strings.ToUpper(targetType)
	}
}

// handleProtocolMapping handles both BPP and BAP routing with proper URL construction.
// Resolution order:
//  1. URI from payload context (npURI) — existing behaviour.
//  2. NodeID-based registry lookup (nodeID) — when URI is absent and a NodeID in
//     namespace/registry/recordName format is present; requires a registry plugin.
//  3. Default URL configured in routing rules (route.URL).
//  4. Error — none of the above resolved a destination.
func handleProtocolMapping(ctx context.Context, route *model.Route, npURI, nodeID, endpoint string, registry definition.RegistryLookup) (*model.Route, error) {
	role := canonicalRoleName(route.TargetType)

	// 1. URI present in payload — use it directly.
	if target := strings.TrimSpace(npURI); target != "" {
		targetURL, err := url.Parse(target)
		if err != nil {
			return nil, fmt.Errorf("invalid %s URI - %s in request body for %s: %w", role, target, endpoint, err)
		}
		targetURL.Path = joinPath(targetURL, endpoint)
		return &model.Route{TargetType: targetTypeURL, URL: targetURL}, nil
	}

	// 2. NodeID present — resolve URL via registry lookup.
	if nodeID = strings.TrimSpace(nodeID); nodeID != "" {
		// Validate 3-part format before making any network call.
		parts := strings.Split(nodeID, "/")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			log.Errorf(ctx, nil, "Router: %s nodeID '%s' for endpoint '%s' is not in namespace/registry/recordName format", role, nodeID, endpoint)
			return nil, fmt.Errorf("could not resolve %s destination for endpoint '%s': nodeID '%s' is not in namespace/registry/recordName format", role, endpoint, nodeID)
		}
		if registry == nil {
			log.Errorf(ctx, nil, "Router: %s nodeID '%s' present for endpoint '%s' but no registry plugin is configured", role, nodeID, endpoint)
			return nil, fmt.Errorf("could not resolve %s destination for endpoint '%s': nodeID '%s' present but no registry plugin is configured", role, endpoint, nodeID)
		}
		subscription, err := registry.LookupNode(ctx, nodeID)
		if err != nil {
			log.Errorf(ctx, err, "Router: registry lookup failed for %s nodeID '%s' on endpoint '%s'", role, nodeID, endpoint)
			return nil, fmt.Errorf("registry lookup failed for %s nodeID '%s' on endpoint '%s': %w", role, nodeID, endpoint, err)
		}
		if subscription.URL == "" {
			log.Errorf(ctx, nil, "Router: registry entry for %s nodeID '%s' has no URL", role, nodeID)
			return nil, fmt.Errorf("registry entry for %s nodeID '%s' has no URL configured", role, nodeID)
		}
		resolvedURL, err := url.Parse(subscription.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid URL '%s' returned from registry for %s nodeID '%s': %w", subscription.URL, role, nodeID, err)
		}
		resolvedURL.Path = joinPath(resolvedURL, endpoint)
		log.Debugf(ctx, "Router: resolved %s URL '%s' via registry NodeID '%s' for endpoint '%s'", role, subscription.URL, nodeID, endpoint)
		return &model.Route{TargetType: targetTypeURL, URL: resolvedURL}, nil
	}

	// 3. Fall back to default URL from routing config.
	if route.URL == nil {
		return nil, fmt.Errorf("could not determine destination for endpoint '%s': neither request contained a %s URI nor was a default URL configured in routing rules", endpoint, role)
	}
	return &model.Route{TargetType: targetTypeURL, URL: route.URL}, nil
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