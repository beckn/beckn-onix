package dediregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// networkAgnosticSubscriberIDs identifies the common catalog service. These subscriber
// IDs are treated as belonging to every network, since the catalog service is shared
// across all networks by design — network-membership validation is skipped entirely
// for lookups of these subscribers. Must not be configured externally.
var networkAgnosticSubscriberIDs = map[string]struct{}{
	"staging.catalg.fabric.nfh.global": {},
	"fabric.nfh.global":                {},
}

// isNetworkAgnosticSubscriber reports whether subscriberID identifies the common catalog service.
func isNetworkAgnosticSubscriber(subscriberID string) bool {
	_, ok := networkAgnosticSubscriberIDs[subscriberID]
	return ok
}

// Error codes this plugin's *model.CodedErr failures carry. Most are Beckn
// v2.0.0 values; NET_ENTITY_NOT_FOUND, NET_DOWNSTREAM_INVALID_RESPONSE,
// NET_REQUEST_CANCELLED and AUT_NETWORK_NOT_ALLOWED are ONIX codes.
const (
	codeNetTimeout               = "NET_TIMEOUT"
	codeNetDownstreamUnavailable = "NET_DOWNSTREAM_UNAVAILABLE"
	codeNetDownstreamInvalidResp = "NET_DOWNSTREAM_INVALID_RESPONSE"
	codeNetRequestCancelled      = "NET_REQUEST_CANCELLED"
	codeNetInternalError         = "NET_INTERNAL_ERROR"
	codeNetEntityNotFound        = "NET_ENTITY_NOT_FOUND"
	codeAutSubscriberNotFound    = "AUT_SUBSCRIBER_NOT_FOUND"
	codeAutKeyNotFound           = "AUT_KEY_NOT_FOUND"
	codeAutSignatureInvalid      = "AUT_SIGNATURE_INVALID"
	codeAutNetworkNotAllowed     = "AUT_NETWORK_NOT_ALLOWED"
	codeCtxMissingField          = "CTX_MISSING_FIELD"
	codeCtxInvalidField          = "CTX_INVALID_FIELD"
)

// maxLookupResponseBytes caps a single-record /lookup response read into
// memory. A record is a few KiB; this only guards against a runaway body.
const maxLookupResponseBytes = 1 << 20

// maxErrorBodyLogBytes caps how much of a non-200 body is read for the log
// line — the same limit retryablehttp drains by default — so a large error
// page from DeDi or a proxy is never loaded whole.
const maxErrorBodyLogBytes = 4 << 10

// notFoundFunc classifies a 404 from DeDi. What a missing record means
// depends on the caller: for Lookup it is an unknown signing subscriber
// (AUT_*), for the metadata lookups it is simply an absent entity.
type notFoundFunc func(err error) *model.CodedErr

// subscriberNotFound classifies a 404 on a signing-key Lookup.
func subscriberNotFound(err error) *model.CodedErr {
	return model.NewSignValidationErr(codeAutSubscriberNotFound, err)
}

// entityNotFound classifies a 404 on a metadata lookup or network query. It
// uses NewCodedErr rather than model.NewNotFoundErr, whose "Endpoint not
// found: " message prefix describes a missing route, not a missing record.
func entityNotFound(err error) *model.CodedErr {
	return model.NewCodedErr(http.StatusNotFound, codeNetEntityNotFound, err)
}

// badUpstreamResponse classifies a 200 whose body DeDi should never have
// sent: not JSON, or missing a required envelope field.
func badUpstreamResponse(err error) *model.CodedErr {
	return model.NewCodedErr(http.StatusBadGateway, codeNetDownstreamInvalidResp, err)
}

// downstreamUnavailable classifies DeDi being unreachable or failing.
func downstreamUnavailable(err error) *model.CodedErr {
	return model.NewCodedErr(http.StatusServiceUnavailable, codeNetDownstreamUnavailable, err)
}

// internalErr classifies a failure on this adapter's side of the call.
func internalErr(err error) *model.CodedErr {
	return model.NewCodedErr(http.StatusInternalServerError, codeNetInternalError, err)
}

// publicErr is an error whose text is safe to send to a remote participant
// in a NACK, while Unwrap keeps the detailed cause for errors.Is/errors.As.
// CodedErr puts its wrapped error's text on the wire, and transport and
// request-build failures carry the internal DeDi URL, host and IP.
type publicErr struct {
	msg   string
	cause error
}

func (e *publicErr) Error() string { return e.msg }
func (e *publicErr) Unwrap() error { return e.cause }

// sanitize logs cause at Error level and returns an error whose text is only
// msg. Use it for registry and adapter faults; caller-triggered outcomes
// build a publicErr directly.
func sanitize(ctx context.Context, msg string, cause error) error {
	log.Errorf(ctx, cause, "DeDi registry: %s", msg)
	return &publicErr{msg: msg, cause: cause}
}

// validSegment reports whether s can be used as one DeDi path segment: not
// empty or whitespace-only, and not "." or "..". url.PathEscape leaves dot
// segments as they are, and a server or proxy that normalises them would
// then serve a different path.
func validSegment(s string) bool {
	return strings.TrimSpace(s) != "" && s != "." && s != ".."
}

// validPath reports whether every "/"-separated segment of p is valid.
func validPath(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if !validSegment(s) {
			return false
		}
	}
	return true
}

// escapePath path-escapes each "/"-separated segment of p, keeping the "/"
// separators. Callers check validPath first.
func escapePath(p string) string {
	segments := strings.Split(p, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

// transportErr classifies a failure to complete the round trip (dial, DNS,
// TLS, reset, redirect loop, or a body read cut short) by cause: the caller
// cancelling, a timeout, or DeDi being unreachable.
func transportErr(ctx context.Context, operation string, err error) *model.CodedErr {
	switch {
	case errors.Is(err, context.Canceled):
		// The inbound request went away; DeDi was not at fault, so this is
		// not logged as a registry error.
		msg := fmt.Sprintf("DeDi %s was cancelled", operation)
		log.Warnf(ctx, "DeDi registry: %s: %v", msg, err)
		return model.NewCodedErr(http.StatusInternalServerError, codeNetRequestCancelled, &publicErr{msg: msg, cause: err})
	case isTimeout(err):
		return model.NewCodedErr(http.StatusGatewayTimeout, codeNetTimeout,
			sanitize(ctx, fmt.Sprintf("DeDi %s timed out", operation), err))
	default:
		return downstreamUnavailable(sanitize(ctx, fmt.Sprintf("DeDi %s failed: registry unreachable", operation), err))
	}
}

// isTimeout reports whether err is a timeout: a context deadline, or any
// error in the chain with a Timeout() bool method reporting true (*url.Error
// from http.Client.Do, net.Error).
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}

// classifyStatus classifies a completed round trip that returned a non-200
// status. A 4xx other than 404/429 means DeDi rejected a request this adapter
// built — our fault, not the caller's. Anything else (5xx and 429, seen here
// only after retries are exhausted — 501 is never retried — or an unexpected
// 1xx/2xx/3xx) means DeDi is not serving the lookup.
func classifyStatus(operation string, resp *http.Response, onNotFound notFoundFunc) *model.CodedErr {
	err := fmt.Errorf("DeDi %s request failed with status: %s", operation, resp.Status)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return onNotFound(err)
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests:
		return internalErr(err)
	default:
		return downstreamUnavailable(err)
	}
}

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
	// Caught here rather than per request: these all build a request fine but
	// send every call to the wrong place (paths are appended as "%s/lookup/...").
	// A trailing slash is harmless and is trimmed in New.
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url must be an absolute http(s) URL, got %q", cfg.URL)
	}
	// Checked on the raw string: url.Parse leaves RawQuery and Fragment empty
	// for a bare trailing "?" or "#".
	if strings.ContainsAny(cfg.URL, "?#") {
		return fmt.Errorf("url must not have a query or fragment, got %q", cfg.URL)
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
	// Return the last response once retries are exhausted, instead of the
	// library default of discarding it for a bare "giving up" error, so a
	// 5xx/429 from DeDi can be classified by its status (classifyStatus).
	retryClient.ErrorHandler = retryablehttp.PassthroughErrorHandler

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

	// Trim a trailing slash so request paths don't start with "//"; the copy
	// leaves the caller's Config unchanged.
	clientCfg := *cfg
	clientCfg.URL = strings.TrimRight(cfg.URL, "/")

	client := &DeDiRegistryClient{
		config:   &clientCfg,
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

// doGet executes a GET request to url and returns the body of a 200
// response, reading at most maxBytes of it. Every failure is returned as a
// classified *model.CodedErr; onNotFound decides what a 404 means for this
// caller. operation is used only in error and log messages (e.g. "record
// lookup", "network query").
func (c *DeDiRegistryClient) doGet(ctx context.Context, url, operation string, maxBytes int64, onNotFound notFoundFunc) ([]byte, error) {
	httpReq, err := retryablehttp.NewRequest("GET", url, nil)
	if err != nil {
		return nil, internalErr(sanitize(ctx, fmt.Sprintf("DeDi %s failed: could not build request", operation),
			fmt.Errorf("failed to create %s request: %w", operation, err)))
	}
	httpReq = httpReq.WithContext(ctx)

	log.Debugf(ctx, "Making DeDi %s request to: %s", operation, url)
	resp, err := c.client.Do(httpReq)
	// A non-nil err means the round trip failed, even if a response came with
	// it (a CheckRedirect failure, or the context ending after headers).
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, transportErr(ctx, operation, fmt.Errorf("failed to send DeDi %s request: %w", operation, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyLogBytes))
		msg := fmt.Sprintf("DeDi %s request failed with status: %s, response: %s", operation, resp.Status, snippet)
		if resp.StatusCode == http.StatusNotFound {
			// An unknown record is routine and caller-triggered, not a registry fault.
			log.Warnf(ctx, "%s", msg)
		} else {
			log.Errorf(ctx, nil, "%s", msg)
		}
		return nil, classifyStatus(operation, resp, onNotFound)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, transportErr(ctx, operation, fmt.Errorf("failed to read %s response body: %w", operation, err))
	}
	if int64(len(body)) > maxBytes {
		// A runaway body is an unusable upstream response, not an adapter fault.
		return nil, badUpstreamResponse(sanitize(ctx, fmt.Sprintf("DeDi %s response exceeds max %d bytes", operation, maxBytes),
			fmt.Errorf("%s response from %s exceeds max %d bytes", operation, url, maxBytes)))
	}
	return body, nil
}

// fetchDeDiData executes a GET request to url via doGet, unmarshals the JSON
// envelope, and returns the inner "data" object.
func (c *DeDiRegistryClient) fetchDeDiData(ctx context.Context, url, operation string, onNotFound notFoundFunc) (map[string]any, error) {
	body, err := c.doGet(ctx, url, operation, maxLookupResponseBytes, onNotFound)
	if err != nil {
		return nil, err
	}

	var responseData map[string]any
	if err := json.Unmarshal(body, &responseData); err != nil {
		log.Errorf(ctx, err, "Invalid DeDi %s response: body is not JSON", operation)
		return nil, badUpstreamResponse(fmt.Errorf("failed to unmarshal %s response body: %w", operation, err))
	}

	data, ok := responseData["data"].(map[string]any)
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi %s response format: missing or invalid data field", operation)
		return nil, badUpstreamResponse(fmt.Errorf("invalid %s response format: missing data field", operation))
	}

	return data, nil
}

// parseSubscriptionFromData extracts a Subscription from a DeDi response data map.
// Shared by Lookup, LookupNode and QueryByNetwork (which skips a record on
// error rather than failing) to avoid duplicating response-parsing logic.
// When requireSigningKey is true an error is returned if signing_public_key is absent
// — required for signature validation (Lookup) but not for URL resolution (LookupNode).
func (c *DeDiRegistryClient) parseSubscriptionFromData(ctx context.Context, data map[string]any, requireSigningKey bool) (*model.Subscription, error) {
	details, ok := data["details"].(map[string]any)
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi response format: missing or invalid details field")
		return nil, badUpstreamResponse(errors.New("invalid response format: missing details field"))
	}

	signingPublicKey, _ := details["signing_public_key"].(string)
	if requireSigningKey && signingPublicKey == "" {
		// The record exists but carries no key to verify against.
		return nil, model.NewSignValidationErr(codeAutKeyNotFound, errors.New("invalid or missing signing_public_key in response"))
	}

	detailsURL, _ := details["url"].(string)
	detailsType, _ := details["type"].(string)
	detailsDomain, _ := details["domain"].(string)
	detailsSubscriberID, _ := details["subscriber_id"].(string)

	encrPublicKey, _ := details["encr_public_key"].(string)
	createdAt, _ := data["created_at"].(string)
	updatedAt, _ := data["updated_at"].(string)

	return &model.Subscription{
		Subscriber: model.Subscriber{
			SubscriberID: detailsSubscriberID,
			URL:          detailsURL,
			Domain:       detailsDomain,
			Type:         detailsType,
		},
		SigningPublicKey: signingPublicKey,
		EncrPublicKey:    encrPublicKey,
		Created:          parseTime(createdAt),
		Updated:          parseTime(updatedAt),
	}, nil
}

// parseMetaFromData extracts the node manifest metadata from a DeDi response
// data map, split into two maps: plain string values (meta), and
// array-shaped values (metaArrays) -- e.g. NFH-014's
// meta.catalog_index_urls: [{url: "..."}]. Returns empty maps (not an
// error) when the meta field is absent or null — the participant has
// simply not published a node manifest yet.
//
// A string value is first tried as a double-encoded array (a JSON string
// whose content IS a [{url}] array, e.g.
// `"catalog_index_urls": "[{\"url\": \"...\"}]"` -- a real-world quirk from
// whatever wrote the record serializing it twice) before falling back to a
// plain meta string. Without this, a double-encoded value would silently
// land in meta instead of metaArrays and never surface to a caller that
// only reads metaArrays (e.g. catalogcrawler's QueryByNetwork consumer).
func parseMetaFromData(ctx context.Context, data map[string]any) (map[string]string, map[string][]string) {
	meta := make(map[string]string)
	metaArrays := make(map[string][]string)
	rawMetaValue, ok := data["meta"]
	if !ok || rawMetaValue == nil {
		return meta, metaArrays
	}
	rawMetaMap, ok := rawMetaValue.(map[string]any)
	if !ok {
		return meta, metaArrays
	}
	for key, value := range rawMetaMap {
		switch v := value.(type) {
		case string:
			if arr, ok := parseDoubleEncodedURLArray(ctx, key, v); ok {
				metaArrays[key] = arr
			} else {
				meta[key] = v
			}
		case []any:
			metaArrays[key] = extractURLObjectArray(ctx, key, v)
		default:
			log.Warnf(ctx, "Ignoring subscriber metadata value of unsupported shape for key %q: got %T", key, value)
		}
	}
	return meta, metaArrays
}

// parseDoubleEncodedURLArray reports whether s is valid JSON for a native
// array (of any shape -- extractURLObjectArray itself filters to {url}
// objects), and if so returns the extracted URLs. Returns ok=false for any
// string that isn't JSON-array syntax, which is the common case (an
// ordinary plain meta string), so those are left for the caller to store as
// a plain string.
func parseDoubleEncodedURLArray(ctx context.Context, fieldName, s string) ([]string, bool) {
	var items []any
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil, false
	}
	return extractURLObjectArray(ctx, fieldName, items), true
}

// extractURLObjectArray extracts the "url" field from each element of a
// meta array shaped like NFH-014's catalog_index_urls: an array of
// {"url": "..."} objects. An element that isn't such an object, or whose
// "url" isn't a non-empty string, is skipped with a warning rather than
// failing the whole lookup.
func extractURLObjectArray(ctx context.Context, fieldName string, items []any) []string {
	out := make([]string, 0, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			log.Warnf(ctx, "Ignoring invalid %s entry at index %d: expected a {url} object, got %T", fieldName, i, item)
			continue
		}
		url, ok := obj["url"].(string)
		if !ok || url == "" {
			log.Warnf(ctx, "Ignoring invalid %s entry at index %d: missing or non-string \"url\" field", fieldName, i)
			continue
		}
		out = append(out, url)
	}
	return out
}

// lookupCacheKey is Lookup's cache key. The IDs are path-escaped, so they
// contain no "/" and different subscriber/key pairs can't share a key (the
// old "dedi_lookup_<sub>_<key>" joined "a_b"+"c" and "a"+"b_c" alike). The
// "v2" prefix keeps any old-format entry from ever being read.
func lookupCacheKey(subscriberID, keyID string) string {
	return fmt.Sprintf("dedi_lookup_v2_%s/%s", url.PathEscape(subscriberID), url.PathEscape(keyID))
}

// isSubscriberRecord reports whether a record whose subscriber_id is
// recordSubscriberID belongs to the requested subscriber. Subscriber IDs are
// domains, so case is ignored. A record without subscriber_id was addressed by
// the requested ID, so it is accepted as that subscriber's.
func isSubscriberRecord(recordSubscriberID, subscriberID string) bool {
	return recordSubscriberID == "" || strings.EqualFold(recordSubscriberID, subscriberID)
}

// cachedLookup returns Lookup's cached results for key, and false on a miss,
// an unreadable entry, or an entry for a subscriber other than subscriberID.
func (c *DeDiRegistryClient) cachedLookup(ctx context.Context, key, subscriberID string) ([]model.Subscription, bool) {
	if c.cache == nil {
		return nil, false
	}
	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
	cacheCtx, span := tracer.Start(ctx, "cache lookup")
	defer span.End()

	cached, err := c.cache.Get(cacheCtx, key)
	if err != nil {
		return nil, false
	}
	var results []model.Subscription
	if err := json.Unmarshal([]byte(cached), &results); err != nil {
		return nil, false
	}
	if len(results) > 0 && !isSubscriberRecord(results[0].Subscriber.SubscriberID, subscriberID) {
		// Never serve a signing key cached for a different subscriber.
		log.Warnf(ctx, "Ignoring cached DeDi record for key %s: subscriber %q does not match requested %q",
			key, results[0].Subscriber.SubscriberID, subscriberID)
		return nil, false
	}
	log.Debugf(ctx, "DeDi registry lookup cache hit for key: %s", key)
	return results, true
}

// Lookup implements RegistryLookup: it returns the requested subscriber's signing key from DeDi,
// cached under lookupCacheKey.
func (c *DeDiRegistryClient) Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error) {
	subscriberID := req.SubscriberID
	keyID := req.KeyID
	log.Infof(ctx, "DeDi Registry: Looking up subscriber ID: %s, key ID: %s", subscriberID, keyID)
	if subscriberID == "" {
		// Both IDs come from the signature's keyId, so a missing one means a
		// malformed signature header, not a malformed request context.
		return nil, model.NewSignValidationErr(codeAutSignatureInvalid, errors.New("subscriber_id is required for DeDi lookup"))
	}
	if keyID == "" {
		return nil, model.NewSignValidationErr(codeAutSignatureInvalid, errors.New("key_id is required for DeDi lookup"))
	}
	if !validSegment(subscriberID) || !validSegment(keyID) {
		return nil, model.NewSignValidationErr(codeAutSignatureInvalid, fmt.Errorf("subscriber_id %q / key_id %q is not a valid DeDi path segment", subscriberID, keyID))
	}

	cacheKey := lookupCacheKey(subscriberID, keyID)
	if results, ok := c.cachedLookup(ctx, cacheKey, subscriberID); ok {
		if len(results) > 0 {
			if err := c.validateMemberships(ctx, results[0].NetworkMemberships, results[0].Subscriber.SubscriberID); err != nil {
				return nil, err
			}
		}
		return results, nil
	}

	// Both IDs come from the sender's signature keyId, so they are escaped.
	lookupURL := fmt.Sprintf("%s/lookup/%s/%s/%s", c.config.URL,
		url.PathEscape(subscriberID), dediAllRegistriesWildcard, url.PathEscape(keyID))

	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
	httpCtx, httpSpan := tracer.Start(ctx, "http lookup")
	data, err := c.fetchDeDiData(httpCtx, lookupURL, "record lookup", subscriberNotFound)
	httpSpan.End()
	if err != nil {
		return nil, err
	}

	log.Debugf(ctx, "DeDi lookup request successful, parsing response")

	parsed, err := c.parseSubscriptionFromData(ctx, data, true)
	if err != nil {
		return nil, err
	}
	// The record must be the requested subscriber's, or a signature claimed for
	// one subscriber would be checked against another's key. An empty
	// subscriber_id is kept as is, so the network checks below (including the
	// catalog exemption) see what DeDi returned.
	if !isSubscriberRecord(parsed.SubscriberID, subscriberID) {
		log.Errorf(ctx, nil, "DeDi record lookup for subscriber %q returned a record for %q", subscriberID, parsed.SubscriberID)
		return nil, model.NewSignValidationErr(codeAutSubscriberNotFound,
			fmt.Errorf("DeDi returned a record for subscriber %q, not %q", parsed.SubscriberID, subscriberID))
	}

	// AllowedNetworkIDs is a trust boundary specific to Lookup: it ensures signing keys are only
	// accepted from subscribers that belong to networks this adapter is configured to trust.
	// LookupNode intentionally skips this check — node record reads are not trust decisions.
	networkMemberships := extractStringSlice(ctx, "network_memberships", data["network_memberships"])
	if err := c.validateMemberships(ctx, networkMemberships, parsed.SubscriberID); err != nil {
		return nil, err
	}

	subscription := *parsed
	subscription.KeyID = keyID
	subscription.NetworkMemberships = networkMemberships

	results := []model.Subscription{subscription}
	log.Debugf(ctx, "DeDi lookup successful, found subscription for subscriber: %s", subscription.SubscriberID)

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
		return nil, model.NewBadReqErr(codeCtxMissingField, errors.New("namespaceIdentifier is required for DeDi registry lookup"))
	}
	if registryName == "" {
		return nil, model.NewBadReqErr(codeCtxMissingField, errors.New("registryName is required for DeDi registry lookup"))
	}
	if !validSegment(namespaceIdentifier) || !validSegment(registryName) {
		return nil, model.NewBadReqErr(codeCtxInvalidField, fmt.Errorf("namespaceIdentifier %q / registryName %q is not a valid DeDi path segment", namespaceIdentifier, registryName))
	}

	lookupURL := fmt.Sprintf("%s/lookup/%s/%s", c.config.URL, url.PathEscape(namespaceIdentifier), url.PathEscape(registryName))

	data, err := c.fetchDeDiData(ctx, lookupURL, "registry lookup", entityNotFound)
	if err != nil {
		return nil, err
	}

	rawMetaValue, ok := data["meta"]
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi response format: missing meta field")
		return nil, badUpstreamResponse(errors.New("invalid response format: missing meta field"))
	}
	rawMeta, ok := rawMetaValue.(map[string]any)
	if !ok {
		log.Errorf(ctx, nil, "Invalid DeDi response format: invalid meta field")
		return nil, badUpstreamResponse(errors.New("invalid response format: invalid meta field"))
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

// LookupNode looks up a subscriber record by its fully-qualified NodeID.
// nodeID must be in namespace/registry/recordName format: exactly 3 segments separated by "/",
// none empty, whitespace-only, "." or "..".
// Returns a SubscriberRecord with both subscriber details (URL, keys) and node manifest metadata
// from the same DeDi response. Meta is empty (not an error) when the participant has not yet
// published a node manifest. AllowedNetworkIDs is not applied — node record reads are not trust
// decisions and should not be gated by network membership.
func (c *DeDiRegistryClient) LookupNode(ctx context.Context, nodeID string) (*model.SubscriberRecord, error) {
	if nodeID == "" {
		return nil, model.NewBadReqErr(codeCtxMissingField, errors.New("nodeID is required for DeDi node lookup"))
	}
	if strings.Count(nodeID, "/") != 2 || !validPath(nodeID) {
		return nil, model.NewBadReqErr(codeCtxInvalidField, fmt.Errorf("nodeID %q must be namespace/registry/recordName with 3 segments, none empty, whitespace-only, \".\" or \"..\"", nodeID))
	}

	lookupURL := fmt.Sprintf("%s/lookup/%s", c.config.URL, escapePath(nodeID))

	data, err := c.fetchDeDiData(ctx, lookupURL, "node lookup", entityNotFound)
	if err != nil {
		return nil, err
	}

	subscription, err := c.parseSubscriptionFromData(ctx, data, false)
	if err != nil {
		return nil, err
	}

	meta, metaArrays := parseMetaFromData(ctx, data)

	log.Debugf(ctx, "DeDi node lookup successful for nodeID: %s, subscriber: %s, url: %s", nodeID, subscription.SubscriberID, subscription.URL)
	return &model.SubscriberRecord{
		Subscription: *subscription,
		Meta:         meta,
		MetaArrays:   metaArrays,
	}, nil
}

// maxQueryResponseBytes caps a /query response read into memory -- unlike
// /lookup's single-record response, /query returns one record per network
// participant, so a runaway or compromised registry could otherwise return
// an arbitrarily large body. A few MiB is far more than any real network
// needs.
const maxQueryResponseBytes = 8 << 20

// queryEnvelope is the /query response shape: a list of records, one per
// network participant, as opposed to /lookup's single-record "data" object.
type queryEnvelope struct {
	Data struct {
		Records []map[string]any `json:"records"`
	} `json:"data"`
}

// QueryByNetwork implements RegistryMetadataLookup — calls the DeDi /query endpoint
// for networkID and returns one SubscriberRecord per live participant record.
// A record that isn't state=="live", or whose details don't parse, is skipped
// rather than failing the whole query -- consistent with LookupNode/parseMetaFromData's
// skip-bad-record handling elsewhere in this file. Not cached: this is a bulk
// discovery read, not the hot per-request signing-key lookup Lookup caches.
func (c *DeDiRegistryClient) QueryByNetwork(ctx context.Context, networkID string) ([]model.SubscriberRecord, error) {
	if networkID == "" {
		return nil, model.NewBadReqErr(codeCtxMissingField, errors.New("networkID is required for DeDi query"))
	}
	if !validPath(networkID) {
		return nil, model.NewBadReqErr(codeCtxInvalidField, fmt.Errorf("networkID %q has an empty, whitespace-only, \".\" or \"..\" segment", networkID))
	}

	queryURL := fmt.Sprintf("%s/query/%s", c.config.URL, escapePath(networkID))

	body, err := c.doGet(ctx, queryURL, "network query", maxQueryResponseBytes, entityNotFound)
	if err != nil {
		return nil, err
	}

	var envelope queryEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		log.Errorf(ctx, err, "Invalid DeDi network query response: body is not the expected JSON shape")
		return nil, badUpstreamResponse(fmt.Errorf("failed to unmarshal network query response body: %w", err))
	}

	records := make([]model.SubscriberRecord, 0, len(envelope.Data.Records))
	for i, data := range envelope.Data.Records {
		state, _ := data["state"].(string)
		if state != "live" {
			continue
		}
		subscription, err := c.parseSubscriptionFromData(ctx, data, false)
		if err != nil {
			log.Warnf(ctx, "Skipping network query record at index %d for network %q: %v", i, networkID, err)
			continue
		}
		meta, metaArrays := parseMetaFromData(ctx, data)
		records = append(records, model.SubscriberRecord{
			Subscription: *subscription,
			Meta:         meta,
			MetaArrays:   metaArrays,
		})
	}

	log.Debugf(ctx, "DeDi network query successful for networkID: %s, live records: %d", networkID, len(records))
	return records, nil
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
	if isNetworkAgnosticSubscriber(subscriberID) {
		return nil
	}
	if len(c.config.AllowedNetworkIDs) > 0 {
		if len(networkMemberships) == 0 || !containsAny(networkMemberships, c.config.AllowedNetworkIDs) {
			// A routine policy rejection, not a registry fault: the public
			// text omits the adapter's config key, and it isn't logged here.
			return model.NewSignValidationErr(codeAutNetworkNotAllowed, &publicErr{
				msg:   fmt.Sprintf("subscriber %q is not a member of a network this adapter accepts", subscriberID),
				cause: fmt.Errorf("registry entry with subscriber_id '%s' does not belong to any configured networks (registry.config.allowedNetworkIDs)", subscriberID),
			})
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
		return model.NewSignValidationErr(codeAutNetworkNotAllowed, fmt.Errorf("context.network_id %q is not in network_memberships of subscriber %q", networkID, subscriberID))
	}
	if len(c.config.AllowedNetworkIDs) > 0 && !containsAny([]string{networkID}, c.config.AllowedNetworkIDs) {
		return model.NewSignValidationErr(codeAutNetworkNotAllowed, &publicErr{
			msg:   fmt.Sprintf("context.network_id %q is not a network this adapter accepts", networkID),
			cause: fmt.Errorf("context.network_id %q is not in configured allowedNetworkIDs", networkID),
		})
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
			if v := model.ResolveNetworkID(payload.Context); v != "" {
				return v
			}
		}
	}
	return ""
}
