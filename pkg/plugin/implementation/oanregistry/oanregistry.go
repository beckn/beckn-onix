// Package oanregistry resolves participant signing keys from the OAN Registry
// (a SunbirdRC deployment) so inbound Beckn signatures can be verified.
//
// It implements definition.RegistryLookup only. Onboarding, key publication and
// status changes all happen through the registry's own Participant APIs.
package oanregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Defaults applied when an operator leaves a setting out. The timeout and retry
// budget are deliberately tighter than the sibling registry plugins': this call
// sits inside signature validation on every inbound message, so timeout x
// (retry_max + 1) is time a request spends waiting before it can be rejected.
// Exported so cmd/plugin.go applies the same values -- these are the single
// source of truth for them.
const (
	DefaultEntity         = "Participant"
	DefaultProviderEntity = "ProviderSchema"
	DefaultTimeoutSeconds = 2
	DefaultRetryMax       = 1
	DefaultRetryWaitMin   = 100 * time.Millisecond
	DefaultRetryWaitMax   = 500 * time.Millisecond
)

// Registry field names. They live here rather than in config because they
// change only when the registry API changes -- never between two deployments of
// this code -- and a typo in a config key would surface at runtime as a
// misleading "no key found" rather than at startup.
const (
	fieldParticipantID = "participantId"
	fieldBindingKey    = "bindingKey"
	searchPath         = "search"
)

// Key "use" values. A key is scoped to a single purpose, so the signing key is
// the only one resolvable by the id in a request header; an encryption key is
// picked up alongside it by use.
const (
	useSign = "sign"
	useEncr = "encr"
)

// keyEncodingPrefix labels the encoding of a published key value, e.g.
// "base64:xq4+...". model.Subscription carries the bare base64 that signvalidator
// hands straight to base64.StdEncoding.DecodeString, so the label is stripped on
// the way through -- left on, it fails every verification with a decode error
// pointing nowhere near the registry.
const keyEncodingPrefix = "base64:"

// statusActive is the only registry status that permits verification. It is
// checked at both levels: a participant stays active while one of its keys is
// retired, which is the whole point of per-key status.
const statusActive = "active"

// expectedAlgorithm is the only signing algorithm on this network. The request
// header is checked against it upstream; this is only used to flag a record that
// disagrees.
const expectedAlgorithm = "ed25519"

// Beckn subscription statuses, as understood by model.IsKeyStatusUsable.
const (
	statusSubscribed   = "SUBSCRIBED"
	statusUnsubscribed = "UNSUBSCRIBED"
)

// Lookup outcomes, used as the error_type metric dimension and in logs. Failure
// outcomes are kept distinct so a dead registry and a malformed body do not
// collapse into one series -- splitting on error_type is the whole point of
// recording it.
const (
	outcomeFound          = "found"
	outcomeCacheHit       = "cache_hit"
	outcomeNotFound       = "not_found"
	outcomeKeyIDMismatch  = "key_id_mismatch"
	outcomeKeyNotSigning  = "key_not_signing"
	outcomeInactive       = "inactive"
	outcomeKeyInactive    = "key_inactive"
	outcomeNoKey          = "no_key"
	outcomeTimeout        = "timeout"
	outcomeRegistryError  = "registry_error"
	outcomeDecodeError    = "decode_error"
	outcomeTransportError = "transport_error"
)

// Failure classes, wrapped so Lookup can tell them apart without inspecting
// error strings.
var (
	errRegistryStatus = errors.New("registry returned an error status")
	errDecodeResponse = errors.New("registry response could not be decoded")
)

// classify maps a search failure onto the outcome vocabulary above.
func classify(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return outcomeTimeout
	case errors.Is(err, errRegistryStatus):
		return outcomeRegistryError
	case errors.Is(err, errDecodeResponse):
		return outcomeDecodeError
	default:
		return outcomeTransportError
	}
}

const (
	pluginID                = "oanregistry"
	pluginType              = "registry"
	operationLookup         = "lookup"
	operationProviderRecord = "provider_record"
)

// Config holds configuration parameters for the OAN registry client.
type Config struct {
	// URL is the registry base including any API version prefix,
	// e.g. "http://registry:8081/api/v1".
	URL string `yaml:"url" json:"url"`
	// Entity names the participant entity, ProviderEntity the capability-binding
	// entity. They are separate registry collections and are searched separately.
	Entity         string        `yaml:"entity" json:"entity"`
	ProviderEntity string        `yaml:"providerEntity" json:"providerEntity"`
	CacheTTL       time.Duration `yaml:"cacheTTL" json:"cacheTTL"`
	Timeout        int           `yaml:"timeout" json:"timeout"`
	RetryMax       int           `yaml:"retry_max" json:"retry_max"`
	RetryWaitMin   time.Duration `yaml:"retry_wait_min" json:"retry_wait_min"`
	RetryWaitMax   time.Duration `yaml:"retry_wait_max" json:"retry_wait_max"`
}

// Client resolves participants from the OAN registry. It is safe for concurrent
// use: every field is set once in New and never mutated afterwards.
type Client struct {
	searchURL         string
	providerSearchURL string
	client            *retryablehttp.Client
	cache             definition.Cache
	cacheTTL          time.Duration
}

// participant is the subset of a registry record this plugin reads. The
// registry carries more -- Sunbird audit fields, the display name, an auth
// block -- and none of it is modelled here. encoding/json drops what it cannot
// place, so every field left out is one less thing to break when the schema
// moves.
//
// Flat, with no wrapper object, because type is the discriminator rather than
// the shape: a node speaks Beckn and publishes keys, an upstream is an ordinary
// API and publishes auth instead. baseUrl serves both -- for a node it is where
// Beckn messages go, for an upstream it is what a binding's path is appended to.
//
// The auth block is left out DELIBERATELY, not pending. An upstream's credential
// is the provider plugin's own configuration -- which scheme, and which
// environment variables hold the values -- so nothing is gained by also reading
// the registry's copy, and reading both would create two places that can
// disagree about how to authenticate a call. The registry publishes it as
// documentation of what a provider expects; the adapter presents what its
// operator configured.
type participant struct {
	ParticipantID string `json:"participantId"`
	Type          string `json:"type"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	BaseURL       string `json:"baseUrl"`
	Keys          []key  `json:"keys"`
}

// key is one published key. A participant publishes several -- separate signing
// and encryption keys, and more than one signing key while a rotation is in
// flight -- so a key is identified by its own OSID rather than by its position.
type key struct {
	OSID       string `json:"osid"`
	KeyID      string `json:"keyId"`
	Use        string `json:"use"`
	Algorithm  string `json:"alg"`
	Value      string `json:"key"`
	Status     string `json:"status"`
	ValidFrom  string `json:"validFrom"`
	ValidUntil string `json:"validUntil"`
}

// publicKey returns the key material with its encoding label removed, ready for
// the base64 decode the caller performs.
func (k key) publicKey() string {
	return strings.TrimPrefix(k.Value, keyEncodingPrefix)
}

// isSigning reports whether this key may verify a signature.
//
// An absent use is accepted: it predates the discriminator, and if the guess is
// wrong the signature simply fails to verify, which is the safe direction. An
// explicitly non-signing use is refused, so an encryption key's osid arriving in
// a signing header is reported as exactly that rather than as a missing key.
func (k key) isSigning() bool {
	return k.Use == "" || strings.EqualFold(k.Use, useSign)
}

type eqFilter struct {
	Eq string `json:"eq"`
}

type searchRequest struct {
	Filters map[string]eqFilter `json:"filters"`
}

// validate checks if the provided OAN registry configuration is valid.
func validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("oan registry config cannot be nil")
	}
	if cfg.URL == "" {
		return fmt.Errorf("oan registry URL cannot be empty")
	}
	// url.Parse accepts almost anything, so check the parts that actually have
	// to be there. Catching "registry:8081" (no scheme) at startup is far
	// cheaper than watching every lookup fail once traffic arrives.
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return fmt.Errorf("invalid oan registry URL %q: %w", cfg.URL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("oan registry URL %q must include a scheme and host, e.g. http://<host>:<port>/api/v1", cfg.URL)
	}
	return nil
}

// New creates a new instance of Client.
func New(ctx context.Context, cache definition.Cache, cfg *Config) (*Client, func() error, error) {
	log.Debugf(ctx, "Initializing OAN registry client with config: %+v", cfg)

	if err := validate(cfg); err != nil {
		return nil, nil, err
	}

	// A TTL with no cache plugin is caching silently switched off, and the cost
	// is three registry round trips inside every request -- key lookup, binding
	// search, participant search -- each inside signature validation's budget.
	// Said out loud at startup, because nothing downstream ever complains.
	if cfg.CacheTTL > 0 && cache == nil {
		log.Warnf(ctx, "OAN registry: cacheTTL is %s but no cache plugin is configured, "+
			"so nothing is cached and every message makes its registry calls again", cfg.CacheTTL)
	}

	entity := cfg.Entity
	if entity == "" {
		entity = DefaultEntity
	}
	providerEntity := cfg.ProviderEntity
	if providerEntity == "" {
		providerEntity = DefaultProviderEntity
	}

	rc := retryablehttp.NewClient()

	// retryablehttp logs every attempt and retry straight to stderr, outside
	// pkg/log, so it is neither structured nor filterable. The same information is
	// already emitted by this plugin's own logging and metrics.
	rc.Logger = nil

	// Always bounded. The sibling registry plugins apply their timeout only when
	// one is configured, which leaves it at zero -- meaning no timeout at all --
	// when it is not. That matters because the transport retryablehttp ships with
	// sets no ResponseHeaderTimeout, so a peer that accepts the connection and
	// then goes quiet is otherwise bounded by nothing.
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds
	}
	rc.HTTPClient.Timeout = time.Duration(timeout) * time.Second

	// Retry settings are taken as given: RetryMax of 0 is a legitimate "do not
	// retry", so it must not be confused with "unset". parseConfig supplies the
	// defaults, since only it can tell the two apart.
	rc.RetryMax = cfg.RetryMax
	if cfg.RetryWaitMin > 0 {
		rc.RetryWaitMin = cfg.RetryWaitMin
	}
	if cfg.RetryWaitMax > 0 {
		rc.RetryWaitMax = cfg.RetryWaitMax
	}

	// DefaultBackoff honours a Retry-After header on 429 and 503 and returns it
	// *unclamped* -- it bypasses its own RetryWaitMax ceiling. A registry, or any
	// ingress in front of one, answering "Retry-After: 3600" would park this
	// goroutine for an hour inside signature validation. The inbound request
	// context typically has no deadline of its own, so nothing else would cut it
	// short. Clamp it back to the configured ceiling.
	rc.Backoff = func(min, max time.Duration, attempt int, resp *http.Response) time.Duration {
		if wait := retryablehttp.DefaultBackoff(min, max, attempt, resp); wait < max {
			return wait
		}
		return max
	}

	client := &Client{
		searchURL:         searchURLFor(cfg.URL, entity),
		providerSearchURL: searchURLFor(cfg.URL, providerEntity),
		client:            rc,
		cache:             cache,
		cacheTTL:          cfg.CacheTTL,
	}

	closer := func() error {
		log.Debugf(ctx, "Cleaning up OAN registry client resources")
		if client.client != nil {
			client.client.HTTPClient.CloseIdleConnections()
		}
		return nil
	}

	log.Infof(ctx, "OAN registry client is created successfully")
	return client, closer, nil
}

// Lookup resolves the signing key for the participant and key named in the
// request. The caller populates only SubscriberID and KeyID; every other field
// on the request is zero.
//
// A missing participant returns (nil, nil) rather than an error: "not found" is
// a legitimate answer, and the caller turns an empty slice into its own
// not-found error. A participant that exists but may not sign is returned with
// a status the caller rejects, so that "unknown" and "suspended" stay
// distinguishable instead of collapsing into the same empty result.
func (c *Client) Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error) {
	start := time.Now()
	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
	ctx, span := tracer.Start(ctx, "oan registry lookup")
	defer span.End()

	// M2: an empty key id would match any record whose OSID is absent. Unreachable
	// under the current shape, where every record carries one -- but cheap, and it
	// stops being unreachable the moment OSID maps to a field that can be missing.
	if req.SubscriberID == "" || req.KeyID == "" {
		span.SetAttributes(telemetry.AttrErrorType.String(outcomeNotFound))
		c.emitMetrics(ctx, start, operationLookup, outcomeNotFound)
		return nil, nil
	}

	cacheKey := fmt.Sprintf("oan_lookup_%s_%s", req.SubscriberID, req.KeyID)
	if cached, ok := c.cached(ctx, tracer, cacheKey); ok {
		log.Debugf(ctx, "OAN registry lookup cache hit for key: %s", cacheKey)
		span.SetAttributes(telemetry.AttrErrorType.String(outcomeCacheHit))
		c.emitMetrics(ctx, start, operationLookup, outcomeCacheHit)
		return cached, nil
	}

	found, matched, searchOutcome, err := c.search(ctx, tracer, req.SubscriberID, req.KeyID)
	if err != nil {
		outcome := classify(err)
		span.RecordError(err)
		span.SetStatus(codes.Error, outcome)
		span.SetAttributes(telemetry.AttrErrorType.String(outcome))
		c.emitMetrics(ctx, start, operationLookup, outcome)
		return nil, err
	}
	span.SetAttributes(telemetry.AttrErrorType.String(searchOutcome))
	if searchOutcome != outcomeFound {
		// Both of these give the caller an empty result, but they are very
		// different facts and must not share a metric. "not_found" means this
		// caller is not registered; "key_id_mismatch" means the participant is
		// registered and the key identity model is wrong -- a sustained rate of
		// the latter is a total outage that would otherwise hide inside routine
		// misses.
		c.emitMetrics(ctx, start, operationLookup, searchOutcome)
		return nil, nil
	}

	status, outcome := resolveStatus(found, matched)
	results := []model.Subscription{toSubscription(found, matched, status)}

	// The header's algorithm was already validated upstream, so a disagreement
	// here cannot let a bad signature through -- but it means the record and the
	// caller disagree about the key, which is worth surfacing before it becomes a
	// verification failure nobody can explain.
	if matched.Algorithm != "" && !strings.EqualFold(matched.Algorithm, expectedAlgorithm) {
		log.Warnf(ctx, "OAN registry participantId=%s osid=%s declares algorithm %q, expected %q",
			req.SubscriberID, req.KeyID, matched.Algorithm, expectedAlgorithm)
	}

	if outcome == outcomeFound {
		log.Debugf(ctx, "OAN registry resolved participantId=%s osid=%s", req.SubscriberID, req.KeyID)
		c.cacheResult(ctx, cacheKey, results)
	} else {
		// Not an error -- the plugin looked, and correctly declined. Logged at
		// Info so a refused signature is traceable without reading as a fault.
		log.Infof(ctx, "OAN registry participantId=%s osid=%s is not usable: %s", req.SubscriberID, req.KeyID, outcome)
	}

	span.SetAttributes(telemetry.AttrErrorType.String(outcome))
	c.emitMetrics(ctx, start, operationLookup, outcome)
	return results, nil
}

// search asks the registry for the participant holding this business id, and
// returns it only if it carries the key the caller asked about.
//
// Only participantId is filtered on, deliberately. It is the schema's
// uniqueIndexFields, so the registry already guarantees at most one match. osid
// is a system-generated field and is not indexed at all, so adding it as a
// second filter does not narrow anything -- on an Elasticsearch-backed registry
// it matches nothing, which would turn every lookup into a not-found. The key
// identity is therefore checked below, client-side, where it works on any
// backend and enforces exactly the same property.
//
// status is deliberately not filtered on either. Excluding suspended
// participants server-side would return an empty result, making "suspended"
// indistinguishable from "unknown" and losing the reason the caller reports.
func (c *Client) search(ctx context.Context, tracer trace.Tracer, participantID, keyID string) (participant, key, string, error) {
	records, err := searchRecords[participant](ctx, c, tracer, c.searchURL, map[string]eqFilter{
		fieldParticipantID: {Eq: participantID},
	})
	if err != nil {
		return participant{}, key{}, "", err
	}

	if len(records) == 0 {
		log.Infof(ctx, "OAN registry has no record for participantId=%s", participantID)
		return participant{}, key{}, outcomeNotFound, nil
	}
	if len(records) > 1 {
		// participantId is the schema's unique index, so this is a registry
		// integrity fault rather than something to resolve. Carry on -- the key
		// check below still decides -- but say so loudly.
		log.Errorf(ctx, nil, "OAN registry returned %d records for participantId=%s, expected at most 1",
			len(records), participantID)
	}

	// The key identity check the filter cannot do. Keys hang off the node, so this
	// walks both levels. Scanning records rather than taking records[0] also covers
	// the case the second filter was originally meant to guard: a stale or
	// soft-deleted record sharing the participantId.
	for _, record := range records {
		for _, k := range record.Keys {
			if k.OSID != keyID {
				continue
			}
			if !k.isSigning() {
				// The osid resolved -- to a key that may not sign. Kept apart from a
				// miss because it is a different fact: the caller is registered and
				// sent a real key id, just one scoped to another purpose.
				log.Errorf(ctx, nil, "OAN registry participantId=%s osid=%s is a %q key, not a signing key",
					participantID, keyID, k.Use)
				return participant{}, key{}, outcomeKeyNotSigning, nil
			}
			return record, k, outcomeFound, nil
		}
	}

	// Reported separately from not-found on purpose: the participant exists, so
	// this says the key identity model is wrong rather than that the caller is
	// unregistered. Logged at Error because a sustained rate of it is an outage.
	log.Errorf(ctx, nil, "OAN registry has %d record(s) for participantId=%s but none carrying key osid %s",
		len(records), participantID, keyID)
	return participant{}, key{}, outcomeKeyIDMismatch, nil
}

// resolveStatus maps the registry's own vocabulary onto the Beckn status the
// caller checks, and reports which outcome was reached.
//
// Only `status` is consulted -- at both levels. The key validity window
// (validFrom / validUntil) is deliberately NOT enforced: the Network Operator controls
// participation entirely through `status`, so an expired key is taken off the
// network by setting status rather than by this plugin timing it out. Those two
// fields are mapped onto the result for a caller to read, and nothing acts on
// them. Decided 20 Aug 2026; flagged as provisional.
//
// This is a security control, not a formatting step. model.IsKeyStatusUsable is
// a deny-list, so any status it does not recognise counts as usable -- passing
// the registry's "inactive" through unchanged would let a suspended
// participant's signature verify. Everything therefore denies unless explicitly
// allowed.
func resolveStatus(p participant, k key) (status, outcome string) {
	if !strings.EqualFold(p.Status, statusActive) {
		return statusUnsubscribed, outcomeInactive
	}
	// Checked separately from the participant's: a participant stays active while a
	// single key is retired, and a retired key has to stop verifying on its own.
	if !strings.EqualFold(k.Status, statusActive) {
		return statusUnsubscribed, outcomeKeyInactive
	}
	if k.publicKey() == "" {
		// Active but unusable. Denying here gives the caller a clear reason
		// instead of an empty key that fails opaquely further down.
		return statusUnsubscribed, outcomeNoKey
	}
	return statusSubscribed, outcomeFound
}

// parseTime reads an RFC3339 timestamp, reporting whether it was present and
// well formed. An absent or unparseable value yields the zero time rather than
// an error: these timestamps are informational, so a malformed one must not
// fail a lookup.
func parseTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// toSubscription builds the value the sign-validation step consumes.
//
// This is the only place a model.Subscription is constructed, which is what
// guarantees Status is always set: its zero value "" is absent from
// IsKeyStatusUsable's deny-list and would therefore authorise the caller.
func toSubscription(p participant, k key, status string) model.Subscription {
	// Informational only: nothing acts on these. The validity window is not
	// enforced -- participation is controlled entirely through `status`.
	validFrom, _ := parseTime(k.ValidFrom)
	validUntil, _ := parseTime(k.ValidUntil)

	// Domain is absent from the OAN record and so is left unset. Nothing on the
	// signature-validation path reads it.
	return model.Subscription{
		Subscriber: model.Subscriber{
			SubscriberID: p.ParticipantID,
			URL:          p.BaseURL,
			// role is the Beckn role -- BAP, BPP or NETWORK. type is the
			// registry's own discriminator (node or upstream) and means
			// something else entirely, so it is not what a subscriber's Type is.
			Type: p.Role,
		},
		KeyID:            k.OSID,
		SigningPublicKey: k.publicKey(),
		EncrPublicKey:    encryptionKey(p),
		ValidFrom:        validFrom,
		ValidUntil:       validUntil,
		Status:           status,
	}
}

// encryptionKey returns the participant's active encryption key, or "" when it
// publishes none. It is resolved by use rather than by id: the request header
// names the signing key only, so there is nothing to match an encryption key
// against.
func encryptionKey(p participant) string {
	for _, k := range p.Keys {
		if strings.EqualFold(k.Use, useEncr) && strings.EqualFold(k.Status, statusActive) {
			return k.publicKey()
		}
	}
	return ""
}

// cachingEnabled reports whether the cache should be consulted at all.
//
// cacheTTL is 0 by default, which disables caching entirely rather than writing
// entries with a zero TTL. A cached entry keeps a suspended participant
// verifying until it expires, so the TTL is exactly the suspension-propagation
// window and is left to the operator to opt into.
func (c *Client) cachingEnabled() bool {
	return c.cache != nil && c.cacheTTL > 0
}

func (c *Client) cached(ctx context.Context, tracer trace.Tracer, key string) ([]model.Subscription, bool) {
	if !c.cachingEnabled() {
		return nil, false
	}

	cacheCtx, span := tracer.Start(ctx, "cache lookup")
	defer span.End()

	raw, err := c.cache.Get(cacheCtx, key)
	if err != nil {
		return nil, false
	}
	var results []model.Subscription
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		log.Warnf(ctx, "Discarding unreadable cache entry for key %s: %v", key, err)
		return nil, false
	}

	// toSubscription guarantees Status is always set, but that invariant only
	// covers values this process wrote. A cache is shared, outlives a deploy and
	// can hold entries written by another version -- and an empty Status is
	// absent from IsKeyStatusUsable's deny-list, so it would read as usable.
	// Re-check on the way in rather than trusting the entry.
	if len(results) != 1 || results[0].Status == "" {
		log.Warnf(ctx, "Discarding malformed cache entry for key %s", key)
		return nil, false
	}
	return results, true
}

// cacheResult caches a usable result. Callers must not pass a not-found or unusable
// participant: caching those would extend an outage and delay a reinstatement.
//
// The TTL comes only from configuration, never from the record's own validity
// window -- that window is typically a year, which would keep a suspended
// participant verifying for a year.
func (c *Client) cacheResult(ctx context.Context, key string, results []model.Subscription) {
	if !c.cachingEnabled() {
		return
	}
	data, err := json.Marshal(results)
	if err != nil {
		log.Warnf(ctx, "Failed to encode OAN registry lookup for caching, key %s: %v", key, err)
		return
	}
	if err := c.cache.Set(ctx, key, string(data), c.cacheTTL); err != nil {
		log.Warnf(ctx, "Failed to cache OAN registry lookup for key %s: %v", key, err)
	}
}

// successOutcomes is an allow-list, deliberately: a new outcome counts as a
// failure until someone says otherwise.
//
// The inverse -- listing the failures and letting anything unlisted fall through
// as success -- is the same shape as model.IsKeyStatusUsable, which is the bug
// resolveStatus exists to work around. Here the blast radius is a dashboard
// rather than an auth decision, but the failure is just as silent: add a
// success-like outcome, forget to list it, and the error rate quietly stops
// being true.
var successOutcomes = map[string]bool{
	outcomeFound:    true,
	outcomeCacheHit: true,
}

// emitMetrics emits the duration of every lookup, and the shared plugin error counter
// for anything that did not resolve a key.
//
// Note that "not a success" includes outcomes that are the plugin working
// correctly: refusing a suspended participant is a successful denial. Split on
// error_type when alerting, or a routine suspension reads as an incident.
func (c *Client) emitMetrics(ctx context.Context, start time.Time, operation, outcome string) {
	m, err := telemetry.GetMetrics(ctx)
	if err != nil {
		return
	}

	attrs := metric.WithAttributes(
		telemetry.AttrPluginID.String(pluginID),
		telemetry.AttrPluginType.String(pluginType),
		telemetry.AttrOperation.String(operation),
		telemetry.AttrErrorType.String(outcome),
	)

	m.PluginExecutionDuration.Record(ctx, time.Since(start).Seconds(), attrs)
	if !successOutcomes[outcome] {
		m.PluginErrorsTotal.Add(ctx, 1, attrs)
	}
}
