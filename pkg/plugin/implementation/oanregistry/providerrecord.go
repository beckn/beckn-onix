package oanregistry

// providerrecord.go resolves a capability binding into a call plan: what to
// call, how to call it, and which mappings translate in and out.
//
// This is the second thing the OAN registry is asked for, and it is a different
// question from the signing-key lookup in oanregistry.go. That one asks "who
// sent this", keyed by an inbound Authorization header. This one asks "who do I
// call next", keyed by a binding taken from the request body. Different subject,
// different cache, different meaning of failure -- so they share transport and
// nothing else.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/hashicorp/go-retryablehttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Provider-record outcomes, used as the error_type metric dimension and in logs.
// Each refusal is kept distinct: they all deny the call, but "this capability was
// withdrawn" and "this provider was suspended" are different operational events
// and must not collapse into one series.
const (
	outcomeBindingNotFound     = "binding_not_found"
	outcomeBindingInactive     = "binding_inactive"
	outcomeBindingUnowned      = "binding_unowned"
	outcomeBindingNoActions    = "binding_no_actions"
	outcomeParticipantNotFound = "participant_not_found"
	outcomeParticipantInactive = "participant_inactive"
	outcomeNoUpstreamURL       = "no_upstream_url"
	outcomeNoBindingKey        = "no_binding_key"
)

// providerBinding is the subset of a capability-binding record this plugin
// reads. As with participant, the registry carries more -- Sunbird audit fields,
// the enricher name -- and none of it is modelled: the enricher is resolved by
// the provider plugin from its own code, not from the registry.
type providerBinding struct {
	BindingKey     string       `json:"bindingKey"`
	ParticipantID  string       `json:"participantId"`
	CapabilityCode string       `json:"capabilityCode"`
	Actions        []actionPlan `json:"actions"`
	Status         string       `json:"status"`
}

// actionPlan is one action's upstream call, as the registry publishes it.
//
// A list rather than a map keyed by action, because the registry models nested
// collections as arrays and injects its own osid/osCreatedAt fields into every
// object it stores. A map would have to hold those alongside real actions; a
// list of structs ignores them, the same way the key list on a participant
// already does.
type actionPlan struct {
	Action    string `json:"action"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Mappings  string `json:"mappings"`
	TimeoutMs int    `json:"timeoutMs"`
	RetryMax  int    `json:"retryMax"`
	Status    string `json:"status"`
}

var (
	_ definition.RegistryLookup       = (*Client)(nil)
	_ definition.ProviderRecordLookup = (*Client)(nil)
)

// searchURLFor builds the search endpoint for one registry entity.
func searchURLFor(baseURL, entity string) string {
	return fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(baseURL, "/"), entity, searchPath)
}

// ProviderRecord resolves bindingKey into everything needed to call the
// provider, reading the capability binding and then the participant that owns
// it.
//
// Every way of saying "this capability cannot be served" -- absent, withdrawn,
// suspended, unroutable -- returns ErrProviderRecordNotFound, because a caller
// does the same thing with all of them. A registry that could not be consulted
// returns its own error instead: that is an outage, not an answer.
func (c *Client) ProviderRecord(ctx context.Context, bindingKey string) (*model.ProviderRecord, error) {
	start := time.Now()
	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
	ctx, span := tracer.Start(ctx, "oan registry provider record")
	defer span.End()

	if bindingKey == "" {
		// A caller bug rather than a registry miss: logged at Error so it is not
		// mistaken for routine traffic, and refused without a round trip.
		log.Errorf(ctx, nil, "OAN registry provider record requested with an empty binding key")
		return nil, c.refuse(ctx, span, start, outcomeNoBindingKey)
	}

	cacheKey := providerRecordCacheKey(bindingKey)
	if plan, found := c.cachedProviderRecord(ctx, tracer, cacheKey); found {
		log.Debugf(ctx, "OAN registry provider record cache hit for key: %s", cacheKey)
		span.SetAttributes(telemetry.AttrErrorType.String(outcomeCacheHit))
		c.emitMetrics(ctx, start, operationProviderRecord, outcomeCacheHit)
		return plan, nil
	}

	binding, outcome, err := c.activeBinding(ctx, tracer, bindingKey)
	if err != nil {
		return nil, c.fail(ctx, span, start, err)
	}
	if outcome != outcomeFound {
		return nil, c.refuse(ctx, span, start, outcome)
	}

	owner, outcome, err := c.activeUpstream(ctx, tracer, binding.ParticipantID)
	if err != nil {
		return nil, c.fail(ctx, span, start, err)
	}
	if outcome != outcomeFound {
		return nil, c.refuse(ctx, span, start, outcome)
	}

	plan := toProviderRecord(binding, owner)
	log.Debugf(ctx, "OAN registry resolved bindingKey=%s to %s serving %s", bindingKey, plan.BaseURL, strings.Join(servedActions(plan), ", "))
	c.cacheProviderRecord(ctx, cacheKey, plan)

	span.SetAttributes(telemetry.AttrErrorType.String(outcomeFound))
	c.emitMetrics(ctx, start, operationProviderRecord, outcomeFound)
	return plan, nil
}

// servedActions lists the actions a plan covers, sorted so the same record logs
// the same way twice.
func servedActions(plan *model.ProviderRecord) []string {
	names := make([]string, 0, len(plan.Actions))
	for action := range plan.Actions {
		names = append(names, action)
	}
	sort.Strings(names)
	return names
}

// refuse records a deliberate denial and returns the caller's sentinel. The
// registry answered; the answer was no.
func (c *Client) refuse(ctx context.Context, span trace.Span, start time.Time, outcome string) error {
	span.SetAttributes(telemetry.AttrErrorType.String(outcome))
	c.emitMetrics(ctx, start, operationProviderRecord, outcome)
	return definition.ErrProviderRecordNotFound
}

// fail records a registry that could not be consulted at all, which is not an
// answer and must never read as one.
func (c *Client) fail(ctx context.Context, span trace.Span, start time.Time, err error) error {
	outcome := classify(err)
	span.RecordError(err)
	span.SetStatus(codes.Error, outcome)
	span.SetAttributes(telemetry.AttrErrorType.String(outcome))
	c.emitMetrics(ctx, start, operationProviderRecord, outcome)
	return err
}

// activeBinding reads the capability binding and reports whether it may be used.
func (c *Client) activeBinding(ctx context.Context, tracer trace.Tracer, bindingKey string) (providerBinding, string, error) {
	bindings, err := searchRecords[providerBinding](ctx, c, tracer, c.providerSearchURL, map[string]eqFilter{
		fieldBindingKey: {Eq: bindingKey},
	})
	if err != nil {
		return providerBinding{}, "", err
	}

	if len(bindings) == 0 {
		log.Infof(ctx, "OAN registry has no capability binding for bindingKey=%s", bindingKey)
		return providerBinding{}, outcomeBindingNotFound, nil
	}
	if len(bindings) > 1 {
		// bindingKey is the schema's unique index, so this is a registry
		// integrity fault. The first row is used rather than refusing outright:
		// duplicates are near-always identical, and denying would turn a registry
		// hiccup into a total outage for the capability. Said loudly either way.
		log.Errorf(ctx, nil, "OAN registry returned %d bindings for bindingKey=%s, expected at most 1",
			len(bindings), bindingKey)
	}

	binding := bindings[0]
	if !isActive(binding.Status) {
		log.Infof(ctx, "OAN registry capability binding bindingKey=%s is not usable: status=%q", bindingKey, binding.Status)
		return providerBinding{}, outcomeBindingInactive, nil
	}
	if binding.ParticipantID == "" {
		// Nothing to look up next, so the plan can never be completed.
		log.Errorf(ctx, nil, "OAN registry capability binding bindingKey=%s names no participant", bindingKey)
		return providerBinding{}, outcomeBindingUnowned, nil
	}
	if len(servableActions(binding)) == 0 {
		// Active, owned, and callable for nothing. Refusing here says so, rather
		// than letting every action fail one at a time further down.
		log.Errorf(ctx, nil, "OAN registry capability binding bindingKey=%s serves no active actions", bindingKey)
		return providerBinding{}, outcomeBindingNoActions, nil
	}
	return binding, outcomeFound, nil
}

// servableActions returns the actions a binding can actually serve.
//
// An entry has to be named to be reachable at all, and active to be served: a
// per-action status is how one action is retired while the capability and every
// other action stay live, so an inactive entry is skipped rather than failing
// the whole record.
func servableActions(binding providerBinding) []actionPlan {
	servable := make([]actionPlan, 0, len(binding.Actions))
	for _, plan := range binding.Actions {
		if plan.Action != "" && isActive(plan.Status) {
			servable = append(servable, plan)
		}
	}
	return servable
}

// activeUpstream reads the participant that owns a binding and reports whether
// its upstream may be called.
func (c *Client) activeUpstream(ctx context.Context, tracer trace.Tracer, participantID string) (participant, string, error) {
	participants, err := searchRecords[participant](ctx, c, tracer, c.searchURL, map[string]eqFilter{
		fieldParticipantID: {Eq: participantID},
	})
	if err != nil {
		return participant{}, "", err
	}

	if len(participants) == 0 {
		log.Errorf(ctx, nil, "OAN registry has no participant %s, named by a live capability binding", participantID)
		return participant{}, outcomeParticipantNotFound, nil
	}
	if len(participants) > 1 {
		log.Errorf(ctx, nil, "OAN registry returned %d records for participantId=%s, expected at most 1",
			len(participants), participantID)
	}

	owner := participants[0]
	if !isActive(owner.Status) {
		log.Infof(ctx, "OAN registry participantId=%s is not usable: status=%q", participantID, owner.Status)
		return participant{}, outcomeParticipantInactive, nil
	}
	if owner.BaseURL == "" {
		// Active but unroutable. Denying here gives a clear reason rather than a
		// request sent to an empty host further down.
		log.Errorf(ctx, nil, "OAN registry participantId=%s publishes no upstream base url", participantID)
		return participant{}, outcomeNoUpstreamURL, nil
	}
	return owner, outcomeFound, nil
}

// isActive reports whether a registry status permits use.
//
// An allow-list, deliberately, and for the same reason resolveStatus is one: a
// deny-list lets every status nobody thought of through, so "withdrawn" or
// "draft" would read as callable.
func isActive(status string) bool {
	return strings.EqualFold(status, statusActive)
}

// toProviderRecord joins the two records into the plan a caller consumes.
// Mapping references are carried verbatim: they are URLs the mapper resolves,
// and this plugin does not interpret them.
func toProviderRecord(binding providerBinding, owner participant) *model.ProviderRecord {
	// Keyed by action for the caller, which looks one up rather than scanning.
	// An entry naming no action is skipped: it cannot be reached, and refusing
	// the whole record over one malformed row would take down the actions that
	// are fine.
	actions := make(map[string]model.ActionPlan, len(binding.Actions))
	for _, plan := range servableActions(binding) {
		actions[plan.Action] = model.ActionPlan{
			Method:    plan.Method,
			Path:      plan.Path,
			Mappings:  plan.Mappings,
			TimeoutMs: plan.TimeoutMs,
			RetryMax:  plan.RetryMax,
		}
	}

	return &model.ProviderRecord{
		BindingKey:     binding.BindingKey,
		ParticipantID:  binding.ParticipantID,
		CapabilityCode: binding.CapabilityCode,
		BaseURL:        owner.BaseURL,
		Actions:        actions,
	}
}

// searchRecords posts a filter to one registry entity and decodes the matching
// records. It is the single transport path for both entities: they differ only
// in URL, filter and record type.
func searchRecords[T any](ctx context.Context, c *Client, tracer trace.Tracer, url string, filters map[string]eqFilter) ([]T, error) {
	body, err := json.Marshal(searchRequest{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %w", err)
	}

	// No Authorization header: the registry's search endpoint is public, and
	// sending a malformed or empty bearer is rejected before the endpoint's own
	// permit rule is reached.
	req, err := retryablehttp.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpCtx, httpSpan := tracer.Start(ctx, "http search")
	defer httpSpan.End()
	req = req.WithContext(httpCtx)

	log.Debugf(ctx, "Making OAN registry search request to: %s", url)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send search request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read search response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The body can carry registry internals, so it is logged but never
		// returned in the error.
		log.Errorf(ctx, nil, "OAN registry search failed with status: %s, response: %s", resp.Status, string(respBody))
		return nil, fmt.Errorf("%w: %s", errRegistryStatus, resp.Status)
	}
	return decodeRecords[T](respBody)
}

// decodeRecords accepts either shape the registry answers with: a bare array on
// some search backends, a data envelope on others. Depending on which one a
// deployment happens to run would be a needless coupling.
func decodeRecords[T any](body []byte) ([]T, error) {
	var records []T
	if err := json.Unmarshal(body, &records); err == nil {
		return records, nil
	} else {
		// Data is a pointer so an absent "data" key is distinguishable from an
		// empty one. Without that, any unrecognised JSON object -- an error body
		// returned with a 200, say -- would decode to zero records and be
		// reported as "no such record", hiding a real failure as a benign miss.
		var envelope struct {
			Data *[]T `json:"data"`
		}
		if envelopeErr := json.Unmarshal(body, &envelope); envelopeErr != nil || envelope.Data == nil {
			// Both attempts are reported. The envelope error is the one that
			// usually matters -- a registry answering with an envelope whose
			// records do not fit is a schema mismatch, and reporting only the
			// array failure ("cannot unmarshal object into []T") sends a reader
			// looking at the wrong level entirely.
			if envelopeErr != nil {
				return nil, fmt.Errorf("%w: as an array: %v; as a data envelope: %v", errDecodeResponse, err, envelopeErr)
			}
			return nil, fmt.Errorf("%w: %v", errDecodeResponse, err)
		}
		return *envelope.Data, nil
	}
}

// providerRecordCacheKey namespaces plans away from signing keys. The two share
// one cache but have different subjects and lifetimes, and a collision would
// serve one as the other.
func providerRecordCacheKey(bindingKey string) string {
	return "oan_provider_" + bindingKey
}

func (c *Client) cachedProviderRecord(ctx context.Context, tracer trace.Tracer, key string) (*model.ProviderRecord, bool) {
	if !c.cachingEnabled() {
		return nil, false
	}

	cacheCtx, span := tracer.Start(ctx, "cache lookup")
	defer span.End()

	raw, err := c.cache.Get(cacheCtx, key)
	if err != nil || raw == "" {
		return nil, false
	}
	var plan model.ProviderRecord
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		log.Warnf(ctx, "Discarding unreadable cache entry for key %s: %v", key, err)
		return nil, false
	}
	// A plan with nothing to call is unusable however it got here. The cache is
	// shared and outlives a deploy, so entries written by another version are
	// re-checked rather than trusted.
	if plan.BaseURL == "" {
		log.Warnf(ctx, "Discarding malformed cache entry for key %s", key)
		return nil, false
	}
	return &plan, true
}

// cacheProviderRecord caches a usable plan. Refusals are never passed here:
// caching one would keep a capability dark for the whole TTL after it is
// reinstated.
func (c *Client) cacheProviderRecord(ctx context.Context, key string, plan *model.ProviderRecord) {
	if !c.cachingEnabled() {
		return
	}
	data, err := json.Marshal(plan)
	if err != nil {
		log.Warnf(ctx, "Failed to encode OAN registry provider record for caching, key %s: %v", key, err)
		return
	}
	if err := c.cache.Set(ctx, key, string(data), c.cacheTTL); err != nil {
		log.Warnf(ctx, "Failed to cache OAN registry provider record for key %s: %v", key, err)
	}
}
