package dediregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/hashicorp/go-retryablehttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const defaultCacheTTL = 5 * time.Minute

// dediAllRegistriesWildcard is the wildcard constant required by the DeDi registry service
// to search across all cached registries in Beckn One. This value must not be configured externally.
const dediAllRegistriesWildcard = "subscribers.beckn.one"

// Config holds configuration parameters for the DeDi registry client.
type Config struct {
	URL               string        `yaml:"url" json:"url"`
	CacheTTL          time.Duration `yaml:"cacheTTL" json:"cacheTTL"`
	AllowedNetworkIDs []string      `yaml:"allowedNetworkIDs" json:"allowedNetworkIDs"`
	Timeout           int           `yaml:"timeout" json:"timeout"`
	RetryMax          int           `yaml:"retry_max" json:"retry_max"`
	RetryWaitMin      time.Duration `yaml:"retry_wait_min" json:"retry_wait_min"`
	RetryWaitMax      time.Duration `yaml:"retry_wait_max" json:"retry_wait_max"`
}

// DeDiRegistryClient encapsulates the logic for calling the DeDi registry endpoints.
type DeDiRegistryClient struct {
	config   *Config
	client   *retryablehttp.Client
	cache    definition.Cache
	cacheTTL time.Duration
}

// validate checks if the provided DeDi registry configuration is valid.
func validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("DeDi registry config cannot be nil")
	}
	if cfg.URL == "" {
		return fmt.Errorf("url cannot be empty")
	}
	return nil
}

// New creates a new instance of DeDiRegistryClient.
func New(ctx context.Context, cache definition.Cache, cfg *Config) (*DeDiRegistryClient, func() error, error) {
	log.Debugf(ctx, "Initializing DeDi Registry client with config: %+v", cfg)

	if err := validate(cfg); err != nil {
		return nil, nil, err
	}

	retryClient := retryablehttp.NewClient()

	// Configure timeout if provided
	if cfg.Timeout > 0 {
		retryClient.HTTPClient.Timeout = time.Duration(cfg.Timeout) * time.Second
	}

	// Configure retry settings if provided
	if cfg.RetryMax > 0 {
		retryClient.RetryMax = cfg.RetryMax
	}
	if cfg.RetryWaitMin > 0 {
		retryClient.RetryWaitMin = cfg.RetryWaitMin
	}
	if cfg.RetryWaitMax > 0 {
		retryClient.RetryWaitMax = cfg.RetryWaitMax
	}

	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}

	client := &DeDiRegistryClient{
		config:   cfg,
		client:   retryClient,
		cache:    cache,
		cacheTTL: ttl,
	}

	// Cleanup function
	closer := func() error {
		log.Debugf(ctx, "Cleaning up DeDi Registry client resources")
		if client.client != nil {
			client.client.HTTPClient.CloseIdleConnections()
		}
		return nil
	}

	log.Infof(ctx, "DeDi Registry client connection established successfully")
	return client, closer, nil
}

// fetchDeDiData executes a GET request to url, reads the body, checks the status,
// unmarshals the JSON envelope, and returns the inner "data" object.
// operation is used only in error and log messages (e.g. "lookup", "registry metadata").
func (c *DeDiRegistryClient) fetchDeDiData(ctx context.Context, url, operation string) (map[string]any, error) {
	httpReq, err := retryablehttp.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s request: %w", operation, err)
	}
	httpReq = httpReq.WithContext(ctx)

	log.Debugf(ctx, "Making DeDi %s request to: %s", operation, url)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send DeDi %s request: %w", operation, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s response body: %w", operation, err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Errorf(ctx, nil, "DeDi %s request failed with status: %s, response: %s", operation, resp.Status, string(body))
		return nil, fmt.Errorf("DeDi %s request failed with status: %s", operation, resp.Status)
	}

	var responseData map[string]any
	if err := json.Unmarshal(body, &responseData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s response body: %w", operation, err)
	}

	data, ok := responseData["data"].(map[string]any)
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi %s response format: missing or invalid data field", operation)
		return nil, fmt.Errorf("invalid %s response format: missing data field", operation)
	}

	return data, nil
}

// Lookup implements RegistryLookup interface - calls the DeDi wrapper lookup endpoint and returns Subscription.
// Results are cached using the subscriber ID and key ID as the cache key.
// On a cache hit the network call is skipped entirely.
func (c *DeDiRegistryClient) Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error) {
	// Extract subscriber ID and key ID from request (both come from Authorization header parsing)
	subscriberID := req.SubscriberID
	keyID := req.KeyID
	log.Infof(ctx, "DeDi Registry: Looking up subscriber ID: %s, key ID: %s", subscriberID, keyID)
	if subscriberID == "" {
		return nil, fmt.Errorf("subscriber_id is required for DeDi lookup")
	}
	if keyID == "" {
		return nil, fmt.Errorf("key_id is required for DeDi lookup")
	}

	cacheKey := fmt.Sprintf("dedi_lookup_%s_%s", subscriberID, keyID)
	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))

	if c.cache != nil {
		cacheCtx, cacheSpan := tracer.Start(ctx, "cache lookup")
		cached, err := c.cache.Get(cacheCtx, cacheKey)
		if err == nil {
			var results []model.Subscription
			if err := json.Unmarshal([]byte(cached), &results); err == nil {
				log.Debugf(ctx, "DeDi registry lookup cache hit for key: %s", cacheKey)
				cacheSpan.End()
				if len(results) > 0 {
					if err := c.validateMemberships(ctx, results[0].NetworkMemberships, results[0].Subscriber.SubscriberID); err != nil {
						return nil, err
					}
				}
				return results, nil
			}
		}
		cacheSpan.End()
	}

	lookupURL := fmt.Sprintf("%s/lookup/%s/%s/%s", c.config.URL, subscriberID, dediAllRegistriesWildcard, keyID)

	httpCtx, httpSpan := tracer.Start(ctx, "http lookup")
	data, err := c.fetchDeDiData(httpCtx, lookupURL, "record lookup")
	httpSpan.End()
	if err != nil {
		return nil, err
	}

	log.Debugf(ctx, "DeDi lookup request successful, parsing response")

	details, ok := data["details"].(map[string]any)
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi response format: missing or invalid details field")
		return nil, fmt.Errorf("invalid response format: missing details field")
	}

	// Extract required fields from details
	signingPublicKey, ok := details["signing_public_key"].(string)
	if !ok || signingPublicKey == "" {
		return nil, fmt.Errorf("invalid or missing signing_public_key in response")
	}

	// Extract fields from details
	detailsURL, _ := details["url"].(string)
	detailsType, _ := details["type"].(string)
	detailsDomain, _ := details["domain"].(string)
	detailsSubscriberID, _ := details["subscriber_id"].(string)

	// Validate network memberships if configured.
	networkMemberships := extractStringSlice(ctx, "network_memberships", data["network_memberships"])
	if err := c.validateMemberships(ctx, networkMemberships, detailsSubscriberID); err != nil {
		return nil, err
	}

	// Extract encr_public_key if available (optional field)
	encrPublicKey, _ := details["encr_public_key"].(string)

	// Extract fields from data level
	createdAt, _ := data["created_at"].(string)
	updatedAt, _ := data["updated_at"].(string)

	// Convert to Subscription format
	subscription := model.Subscription{
		Subscriber: model.Subscriber{
			SubscriberID: detailsSubscriberID,
			URL:          detailsURL,
			Domain:       detailsDomain,
			Type:         detailsType,
		},
		KeyID:              keyID, // Use original keyID from request
		SigningPublicKey:   signingPublicKey,
		EncrPublicKey:      encrPublicKey, // May be empty if not provided
		Created:            parseTime(createdAt),
		Updated:            parseTime(updatedAt),
		NetworkMemberships: networkMemberships,
	}

	results := []model.Subscription{subscription}
	log.Debugf(ctx, "DeDi lookup successful, found subscription for subscriber: %s", detailsSubscriberID)

	if c.cache != nil {
		ttl := c.cacheTTL
		if ttlSec, ok := data["ttl"].(float64); ok && ttlSec > 0 {
			ttl = time.Duration(ttlSec) * time.Second
		}
		if encoded, err := json.Marshal(results); err == nil {
			if err := c.cache.Set(ctx, cacheKey, string(encoded), ttl); err != nil {
				log.Warnf(ctx, "Failed to cache DeDi registry lookup result for key %s: %v", cacheKey, err)
			}
		}
	}

	return results, nil
}

// LookupRegistry fetches registry-level metadata for the given DeDi registry path.
func (c *DeDiRegistryClient) LookupRegistry(ctx context.Context, namespaceIdentifier, registryName string) (*model.RegistryMetadata, error) {
	if namespaceIdentifier == "" {
		return nil, fmt.Errorf("namespaceIdentifier is required for DeDi registry lookup")
	}
	if registryName == "" {
		return nil, fmt.Errorf("registryName is required for DeDi registry lookup")
	}

	lookupURL := fmt.Sprintf("%s/lookup/%s/%s", c.config.URL, namespaceIdentifier, registryName)

	data, err := c.fetchDeDiData(ctx, lookupURL, "registry lookup")
	if err != nil {
		return nil, err
	}

	rawMetaValue, ok := data["meta"]
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi response format: missing meta field")
		return nil, fmt.Errorf("invalid response format: missing meta field")
	}
	rawMeta, ok := rawMetaValue.(map[string]any)
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi response format: invalid meta field")
		return nil, fmt.Errorf("invalid response format: invalid meta field")
	}

	meta := make(map[string]string, len(rawMeta))
	for key, value := range rawMeta {
		strValue, ok := value.(string)
		if !ok {
			log.Warnf(ctx, "Ignoring non-string registry metadata value for key %q: got %T", key, value)
			continue
		}
		meta[key] = strValue
	}

	return &model.RegistryMetadata{
		NamespaceIdentifier: namespaceIdentifier,
		RegistryName:        registryName,
		RawMeta:             meta,
	}, nil
}

// parseTime converts string timestamp to time.Time
func parseTime(timeStr string) time.Time {
	if timeStr == "" {
		return time.Time{}
	}
	parsedTime, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return time.Time{}
	}
	return parsedTime
}

func extractStringSlice(ctx context.Context, fieldName string, value any) []string {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			str, ok := item.(string)
			if !ok {
				log.Warnf(ctx, "Ignoring invalid %s entry at index %d during registry lookup: expected a string network ID, got %T. This entry will not be considered for allowlist validation.", fieldName, i, item)
				continue
			}
			if str != "" {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

func containsAny(values []string, allowed []string) bool {
	if len(values) == 0 || len(allowed) == 0 {
		return false
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, entry := range allowed {
		if entry == "" {
			continue
		}
		allowedSet[entry] = struct{}{}
	}
	for _, value := range values {
		if _, ok := allowedSet[value]; ok {
			return true
		}
	}
	return false
}

// validateMemberships runs both the static allowedNetworkIDs guard and the per-request
// context.network_id check against the subscriber's network_memberships.
// Called on both the cache-hit and HTTP paths.
func (c *DeDiRegistryClient) validateMemberships(ctx context.Context, networkMemberships []string, subscriberID string) error {
	if len(c.config.AllowedNetworkIDs) > 0 {
		if len(networkMemberships) == 0 || !containsAny(networkMemberships, c.config.AllowedNetworkIDs) {
			return fmt.Errorf("registry entry with subscriber_id '%s' does not belong to any configured networks (registry.config.allowedNetworkIDs)", subscriberID)
		}
	}
	return c.validateContextNetworkID(ctx, networkMemberships, subscriberID)
}

// validateContextNetworkID checks context.network_id (when present in the request body) against
// the caller's network_memberships and the configured allowedNetworkIDs.
// Returns nil when context.network_id is absent or empty.
func (c *DeDiRegistryClient) validateContextNetworkID(ctx context.Context, networkMemberships []string, subscriberID string) error {
	networkID := extractContextNetworkID(ctx)
	if networkID == "" {
		return nil
	}
	if !containsAny(networkMemberships, []string{networkID}) {
		return fmt.Errorf("context.network_id %q is not in network_memberships of subscriber %q", networkID, subscriberID)
	}
	if len(c.config.AllowedNetworkIDs) > 0 && !containsAny([]string{networkID}, c.config.AllowedNetworkIDs) {
		return fmt.Errorf("context.network_id %q is not in configured allowedNetworkIDs", networkID)
	}
	return nil
}

// extractContextNetworkID returns context.network_id from the request.
// Primary path: reads the context value set by reqpreprocessor (survives any OTel wrapping depth).
// Fallback path: type-asserts to *model.StepContext and parses Body directly — works when
// simplekeymanager preserves *model.StepContext through its OTel span, and also with keymanager.
// Checks both "network_id" (snake_case) and "networkId" (camelCase). Returns "" when absent.
func extractContextNetworkID(ctx context.Context) string {
	if v, _ := ctx.Value(model.ContextKeyNetworkID).(string); v != "" {
		return v
	}
	if sc, ok := ctx.(*model.StepContext); ok && len(sc.Body) > 0 {
		var payload struct {
			Context map[string]interface{} `json:"context"`
		}
		if err := json.Unmarshal(sc.Body, &payload); err == nil && payload.Context != nil {
			for _, key := range []string{"network_id", "networkId"} {
				val, exists := payload.Context[key]
				if !exists {
					continue
				}
				v, ok := val.(string)
				if !ok {
					log.Warnf(ctx, "context.%s is present but not a string (got %T); network_id check will be skipped", key, val)
					break
				}
				if v != "" {
					return v
				}
			}
		}
	}
	return ""
}
