// Package upstream serves a Beckn capability by calling an ordinary API that has
// never heard of Beckn.
//
// "upstream" is the registry's own word for such an API -- a Participant of type
// upstream, as against a node that speaks Beckn. This package is the machinery
// for calling one: recognise the capability, resolve the call plan, translate
// out, call, translate back.
//
// It holds nothing about any provider or any domain. What varies per capability
// comes from the registry (endpoint, method, budget, which mapping) and from the
// mapping itself (what the payload must satisfy, what to send, what to return).
// A domain package wraps this, supplying only its name and whatever prerequisite
// work a mapping cannot express.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/oanbinding"
)

// Defaults applied when the registry or the operator leaves a setting out.
const (
	// DefaultTimeout and DefaultRetryMax are the registry contract's defaults
	// for an action that leaves timeoutMs or retryMax out. Zero retries is
	// deliberate: a provider that failed is retried only where the operator
	// said so, because a retry on a non-idempotent action is a second booking.
	DefaultTimeout  = 15 * time.Second
	DefaultRetryMax = 0
	// DefaultMaxResponseBytes caps what is read from the provider. The response
	// is mapped in memory, so an unbounded one is an unbounded allocation.
	DefaultMaxResponseBytes = 4 << 20 // 4 MiB
)

// Auth schemes this step can present upstream. Credentials themselves are never
// configured here or held in the registry -- config names the environment
// variable to read, so a secret reaches the process through its environment and
// nothing else.
const (
	// RetryBackoffBase is the first wait between attempts, doubling from there
	// up to RetryBackoffMax. Short, because the retry budget comes from the
	// registry and an operator setting 5 retries did not ask for seconds of
	// latency -- only for the provider's brief unavailability to be ridden out.
	RetryBackoffBase = 50 * time.Millisecond
	RetryBackoffMax  = 800 * time.Millisecond

	AuthSchemeNone   = "none"
	AuthSchemeBasic  = "basic"
	AuthSchemeHeader = "header"
	// AuthSchemeQuery puts the credential in the query string, which some
	// upstreams are built around whatever anyone thinks of it. It is the least
	// safe of the four -- a query string is logged by proxies and appears in a
	// transport error -- so the value is redacted from anything this package
	// logs or returns. See redact.
	AuthSchemeQuery = "query"
)

// codeUpstreamUnavailable reports a provider that could not be reached or
// answered with a failure. It is not this adapter's fault and not the caller's.
const codeUpstreamUnavailable = "NET_DOWNSTREAM_UNAVAILABLE"

// Prerequisites are the values a capability needs that its payload does not
// carry, keyed by binding key.
//
// A mapping cannot produce them: a station id comes from a spatial lookup, a
// session token from an exchange, a market code from a table. That is real I/O,
// and no expression language should be able to do it.
//
// Whatever a function returns is handed to the mapping as _local, so the mapping
// still decides what the provider is finally asked for. A capability with no
// entry needs nothing, which is the common case.
type Prerequisites map[string]func(context.Context, any) (map[string]any, error)

// Config holds configuration parameters for the step.
type Config struct {
	// BindingKeys are the capabilities this step answers to. A request for
	// anything else passes through untouched.
	//
	// A list because a provider can serve more than one: the registry contract
	// says a provider serving two capabilities is one Participant and two
	// ProviderSchema rows. Configuring a second entry with the same plugin id
	// instead would collide in the handler's id-keyed step map, and one
	// capability would be lost with no error anywhere.
	//
	// What differs per capability -- the endpoint, the mapping, the budget --
	// comes from the registry, so one step serving several needs nothing else.
	BindingKeys []string `yaml:"bindingKeys" json:"bindingKeys"`

	// ProviderIDAt and CapabilityCodeAt override where the two halves of a
	// binding key sit in a payload. Absent means the Beckn v2 convention, which
	// is what every deployment should be using.
	//
	// This is a network convention rather than a deployment's preference --
	// every participant must agree, or two adapters disagree about what a
	// binding key is and requests silently fail to match. It is configurable
	// only so that a spec change can be tracked without waiting for a release,
	// and both must be given together.
	ProviderIDAt     string `yaml:"providerIdAt" json:"providerIdAt"`
	CapabilityCodeAt string `yaml:"capabilityCodeAt" json:"capabilityCodeAt"`

	// AuthScheme is how credentials are presented upstream: none, basic or
	// header. Providers differ here -- basic auth, a raw token header, a field
	// in the body -- which is why it is configuration and not an assumption.
	AuthScheme string `yaml:"authScheme" json:"authScheme"`

	// UsernameEnv and PasswordEnv name the environment variables holding basic
	// credentials. They are variable NAMES, never the values.
	UsernameEnv string `yaml:"usernameEnv" json:"usernameEnv"`
	PasswordEnv string `yaml:"passwordEnv" json:"passwordEnv"`

	// HeaderName and HeaderValueEnv configure the header scheme: which header to
	// set, and which environment variable holds its value.
	HeaderName     string `yaml:"headerName" json:"headerName"`
	HeaderValueEnv string `yaml:"headerValueEnv" json:"headerValueEnv"`

	// QueryName and QueryValueEnv configure authScheme query: the parameter
	// name to add, and the environment variable holding its value. Named the
	// same way as the header pair, for the same reason -- the credential is
	// never in this config, only the name of the variable carrying it.
	QueryName     string `yaml:"queryName" json:"queryName"`
	QueryValueEnv string `yaml:"queryValueEnv" json:"queryValueEnv"`

	// MaxResponseBytes caps what is read from the provider.
	MaxResponseBytes int64 `yaml:"maxResponseBytes" json:"maxResponseBytes"`
}

// Step serves whatever capabilities a domain package configures it for. It is
// safe for concurrent use.
type Step struct {
	config        *Config
	paths         oanbinding.Paths
	prerequisites Prerequisites
	registry      definition.ProviderRecordLookup
	mapper        definition.Mapper
	httpClient    *http.Client
}

// New creates the step.
func New(ctx context.Context, registry definition.ProviderRecordLookup, mapper definition.Mapper,
	prerequisites Prerequisites, cfg *Config) (*Step, func() error, error) {
	if registry == nil {
		return nil, nil, errors.New("upstream: a provider record lookup is required")
	}
	if mapper == nil {
		return nil, nil, errors.New("upstream: a mapper is required")
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if err := applyDefaults(cfg); err != nil {
		return nil, nil, err
	}

	paths, err := bindingPaths(cfg)
	if err != nil {
		return nil, nil, err
	}

	step := &Step{
		config:        cfg,
		paths:         paths,
		prerequisites: prerequisites,
		registry:      registry,
		mapper:        mapper,
		// Timeout is set per request from the registry's own budget, so the
		// client carries none of its own.
		httpClient: &http.Client{},
	}

	closer := func() error {
		log.Debugf(ctx, "Cleaning up upstream step resources")
		step.httpClient.CloseIdleConnections()
		return nil
	}

	log.Infof(ctx, "Upstream step created for %s", strings.Join(cfg.BindingKeys, ", "))
	return step, closer, nil
}

// bindingPaths resolves where this step reads a binding key from.
//
// Both halves or neither: overriding one and leaving the other on the default
// is a half-configured deployment that would match nothing, and it would do so
// silently on every request rather than once at startup.
func bindingPaths(cfg *Config) (oanbinding.Paths, error) {
	if cfg.ProviderIDAt == "" && cfg.CapabilityCodeAt == "" {
		return oanbinding.BecknV2, nil
	}
	if cfg.ProviderIDAt == "" {
		return oanbinding.Paths{}, errors.New("upstream: capabilityCodeAt is set without providerIdAt")
	}
	if cfg.CapabilityCodeAt == "" {
		return oanbinding.Paths{}, errors.New("upstream: providerIdAt is set without capabilityCodeAt")
	}
	paths := oanbinding.Paths{ProviderID: cfg.ProviderIDAt, CapabilityCode: cfg.CapabilityCodeAt}
	if err := paths.Validate(); err != nil {
		return oanbinding.Paths{}, err
	}
	return paths, nil
}

// applyDefaults fills in what was left out and rejects what cannot be defaulted.
func applyDefaults(cfg *Config) error {
	// No default. This package serves whatever a domain package configures it
	// for, so a default would have to name one provider's capability -- wrong
	// for every other domain built on it, and silently wrong rather than loudly.
	if len(cfg.BindingKeys) == 0 {
		return errors.New("upstream: bindingKeys is required: it is what this step answers to")
	}
	for _, key := range cfg.BindingKeys {
		if strings.TrimSpace(key) == "" {
			return errors.New("upstream: bindingKeys carries an empty entry")
		}
	}
	if cfg.AuthScheme == "" {
		cfg.AuthScheme = AuthSchemeNone
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultMaxResponseBytes
	}

	switch cfg.AuthScheme {
	case AuthSchemeNone:
	case AuthSchemeBasic:
		if cfg.UsernameEnv == "" || cfg.PasswordEnv == "" {
			return errors.New("upstream: authScheme basic requires usernameEnv and passwordEnv")
		}
	case AuthSchemeHeader:
		if cfg.HeaderName == "" || cfg.HeaderValueEnv == "" {
			return errors.New("upstream: authScheme header requires headerName and headerValueEnv")
		}
	case AuthSchemeQuery:
		if cfg.QueryName == "" || cfg.QueryValueEnv == "" {
			return errors.New("upstream: authScheme query requires queryName and queryValueEnv")
		}
	default:
		return fmt.Errorf(
			"upstream: unknown authScheme %q: must be none, basic, header or query", cfg.AuthScheme)
	}
	return nil
}

// Run serves the request when it is for this step's capability, and does
// nothing when it is not.
//
// Doing nothing is the dispatch mechanism: several provider steps sit in one
// pipeline and each recognises its own work, so adding a provider is one more
// entry rather than a change to a routing table.
func (s *Step) Run(ctx *model.StepContext) error {
	binding, err := oanbinding.From(s.paths, ctx.Body)
	if errors.Is(err, oanbinding.ErrNoBinding) {
		return nil
	}
	if err != nil {
		// Everything From refuses is a statement about the payload: unreadable
		// JSON, or a request naming more than one call. Unclassified it becomes
		// a 500, which says this adapter broke and leaves the reason in a log
		// the caller cannot read.
		return model.NewBadReqErr("", err)
	}
	if !s.serves(binding.Key()) {
		log.Debugf(ctx, "upstream: %s is not one of this step's capabilities, passing through", binding.Key())
		return nil
	}

	plan, err := s.registry.ProviderRecord(ctx, binding.Key())
	if err != nil {
		// A definite "no such binding" is the caller naming something that is
		// not there, so 404 -- the same reasoning the no-route path uses to
		// refuse an unrecognised capability rather than ACK it. A registry that
		// could not be consulted is different and stays a 500: unclassified,
		// because it is this adapter that failed.
		if errors.Is(err, definition.ErrProviderRecordNotFound) {
			// %w, not %v: the sentinel has to stay unwrappable, or anything
			// upstream testing errors.Is against it silently stops matching.
			return model.NewNotFoundErr("", fmt.Errorf(
				"upstream: the registry publishes no active binding for %s: %w", binding.Key(), err))
		}
		return fmt.Errorf("upstream: no call plan for %s: %w", binding.Key(), err)
	}

	return s.serve(ctx, plan)
}

// resolve runs whatever prerequisite work this capability needs, and returns the
// values for the mapping to read under _local.
//
// Empty rather than nil when there is nothing: a mapping referring to _local on
// a capability that resolves nothing should read a missing field, not fail.
func (s *Step) resolve(ctx context.Context, bindingKey string, beckn any) (map[string]any, error) {
	prerequisite, needed := s.prerequisites[bindingKey]
	if !needed {
		return map[string]any{}, nil
	}
	local, err := prerequisite(ctx, beckn)
	if err != nil {
		return nil, fmt.Errorf("upstream: %s could not resolve what it needs before the call: %w", bindingKey, err)
	}
	if local == nil {
		return map[string]any{}, nil
	}
	return local, nil
}

// serves reports whether a binding key is one this step answers to.
//
// A slice rather than a set: a step serves a handful of capabilities at most, so
// the scan costs less than the map would, and the config order is preserved in
// the log line above.
func (s *Step) serves(key string) bool {
	return slices.Contains(s.config.BindingKeys, key)
}

// serve runs the exchange this step exists for: resolve, map out, call, map back.
func (s *Step) serve(ctx *model.StepContext, plan *model.ProviderRecord) error {
	action := extractAction(ctx.Body)
	call, served := plan.Actions[action]
	if !served {
		// The capability publishes no endpoint for this action, so it does not
		// serve it. Refused here rather than after a call to whichever endpoint
		// happened to be on the record -- naming what it does serve turns a
		// registry mistake into a one-line fix.
		return model.NewBadReqErr("", fmt.Errorf(
			"upstream: %s does not serve action %q; it serves %s",
			plan.BindingKey, action, strings.Join(servedActions(plan), ", ")))
	}

	beckn, err := decodeBody(ctx.Body)
	if err != nil {
		return err
	}

	// What this provider requires of a payload is declared by its mapping, not
	// by this step. A capability with a different rule is a different mapping
	// file rather than a different build -- and the rule sits beside the
	// extraction it guards.
	if err := s.mapper.Verify(ctx, call.Mappings, map[string]any{"beckn": beckn}); err != nil {
		return err
	}

	// Whatever this capability needs that its payload does not carry. Empty for
	// most: the mapping reads the payload directly and needs nothing resolved.
	local, err := s.resolve(ctx, plan.BindingKey, beckn)
	if err != nil {
		return err
	}

	upstreamRequest, err := s.buildRequest(ctx, call, beckn, local)
	if err != nil {
		return err
	}

	upstreamResponse, err := s.call(ctx, plan.BaseURL, call, upstreamRequest)
	if err != nil {
		return err
	}

	answer, err := decodeBody(upstreamResponse)
	if err != nil {
		return fmt.Errorf("upstream: provider answered with something that is not JSON: %w", err)
	}

	// The same mapping reference as the request, other half: one file carries
	// both directions for this action.
	//
	// The mapping is handed what each party sent and nothing else.
	becknResponse, err := s.mapper.Transform(ctx, call.Mappings, definition.DirectionResponse, map[string]any{
		"beckn":    beckn,
		"_local":   local,
		"response": answer,
	})
	if err != nil {
		return err
	}
	if len(becknResponse) == 0 {
		// Either the file has no response half, or its transform matched nothing
		// in this answer. Both leave no Beckn response to return, and returning
		// the provider's own shape instead would be worse than failing. The
		// message says what was observed rather than guessing which it was.
		return fmt.Errorf("upstream: the response half of %s produced nothing, so %s cannot be answered",
			call.Mappings, plan.BindingKey)
	}

	ctx.ResponseBody = becknResponse
	log.Infof(ctx, "upstream: served %s in %d bytes", plan.BindingKey, len(becknResponse))
	return nil
}

// servedActions lists the actions a capability covers, sorted so the same
// record reads the same way twice.
func servedActions(plan *model.ProviderRecord) []string {
	names := make([]string, 0, len(plan.Actions))
	for action := range plan.Actions {
		names = append(names, action)
	}
	sort.Strings(names)
	return names
}

// buildRequest produces what the provider is sent.
//
// Whatever the mapping produces IS the request: a body for a method that takes
// one, query parameters for a method that does not. Nothing is substituted when
// it produces nothing, so an empty request half means an empty request.
//
// This step used to extract a point from the payload and fall back to sending
// that. It meant the choice of which payload fields reach the provider lived in
// Go, so adding a parameter -- a date range, say -- was a rebuild. Now it is a
// mapping edit and nothing else.
func (s *Step) buildRequest(ctx context.Context, call model.ActionPlan, beckn any, local map[string]any) ([]byte, error) {
	mapped, err := s.mapper.Transform(ctx, call.Mappings, definition.DirectionRequest, map[string]any{
		"beckn":  beckn,
		"_local": local,
	})
	if err != nil {
		return nil, err
	}
	if len(mapped) == 0 {
		log.Debugf(ctx, "upstream: the request half of %s produced nothing; sending an empty request", call.Mappings)
	}
	return mapped, nil
}

// extractAction reads the Beckn action a request is for.
func extractAction(body []byte) string {
	var payload struct {
		Context struct {
			Action string `json:"action"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Context.Action
}

// decodeBody turns raw JSON into the generic value a mapping reads.
func decodeBody(body []byte) (any, error) {
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("upstream: could not read JSON: %w", err)
	}
	return decoded, nil
}

// call makes the upstream request described by the plan, retrying within its
// budget.
func (s *Step) call(ctx context.Context, baseURL string, call model.ActionPlan, mapped []byte) ([]byte, error) {
	endpoint, err := buildEndpoint(baseURL, call, mapped)
	if err != nil {
		return nil, err
	}

	timeout := DefaultTimeout
	if call.TimeoutMs > 0 {
		timeout = time.Duration(call.TimeoutMs) * time.Millisecond
	}
	// retryMax counts retries, not attempts, so the call itself is always made
	// once. An absent retryMax and an explicit 0 are the same instruction.
	retries := DefaultRetryMax
	if call.RetryMax > 0 {
		retries = call.RetryMax
	}
	attempts := retries + 1

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		// A caller that has gone away is not worth another attempt, and neither
		// is a budget already spent. Checked before the call rather than after,
		// so a cancelled request costs nothing.
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			break
		}

		body, err := s.attempt(ctx, call, endpoint, mapped, timeout)
		if err == nil {
			return body, nil
		}
		lastErr = s.redact(err)
		log.Warnf(ctx, "upstream: attempt %d/%d failed: %v", attempt, attempts, lastErr)

		// Only some failures are worth repeating. A 4xx, a request this step
		// could not build and a credential it could not read will fail
		// identically however many times they are tried -- and retrying the
		// credential case is the worst of them, because it reports an
		// operator's missing environment variable as the provider being down.
		if isPermanent(err) {
			break
		}
		if attempt < attempts {
			if err := sleep(ctx, backoff(attempt)); err != nil {
				break
			}
		}
	}
	return nil, model.NewCodedErr(http.StatusBadGateway, codeUpstreamUnavailable,
		fmt.Errorf("upstream: provider did not answer after %d attempts: %w", attempts, lastErr))
}

// permanentErr marks a failure no retry can fix. Kept unexported and detected
// with errors.As, so a caller of this package sees only the underlying error.
type permanentErr struct{ error }

func (p permanentErr) Unwrap() error { return p.error }

// doNotRetry marks err as not worth repeating.
func doNotRetry(err error) error { return permanentErr{err} }

// isPermanent reports whether err is one no further attempt would change.
func isPermanent(err error) bool {
	var permanent permanentErr
	return errors.As(err, &permanent)
}

// backoff is how long to wait before the next attempt.
//
// Exponential from a short base and capped, because the provider being briefly
// busy is the case worth waiting out; anything longer is a timeout's job. With
// no wait at all a retryMax of 5 spends its whole budget inside a couple of
// milliseconds, which is not a retry so much as the same failure six times.
func backoff(attempt int) time.Duration {
	wait := RetryBackoffBase << (attempt - 1)
	if wait > RetryBackoffMax {
		return RetryBackoffMax
	}
	return wait
}

// sleep waits, or reports that the context ended first.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// attempt makes one upstream request.
func (s *Step) attempt(ctx context.Context, call model.ActionPlan, endpoint string, mapped []byte, timeout time.Duration) ([]byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, call.Method, endpoint, requestBody(call.Method, mapped))
	if err != nil {
		return nil, doNotRetry(fmt.Errorf("could not build the request: %w", err))
	}
	if hasBody(call.Method) {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := s.authenticate(req); err != nil {
		// A missing or unreadable credential is configuration, not weather.
		return nil, doNotRetry(err)
	}

	// The URL as it actually went on the wire, credential removed. At info
	// rather than debug because this is the line that answers "what did we ask,
	// and what came back" -- the question every provider problem starts with.
	requested := s.redactString(req.URL.String())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, s.config.MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("could not read the response: %w", err)
	}
	log.Infof(ctx, "upstream: %s %s -> %s, %d bytes", call.Method, requested, resp.Status, len(body))
	if int64(len(body)) > s.config.MaxResponseBytes {
		// Asking again will not make the answer smaller.
		return nil, doNotRetry(fmt.Errorf("response exceeds the %d byte limit", s.config.MaxResponseBytes))
	}
	// Any 2xx, not 200 alone. A provider is entitled to answer 202 for work it
	// accepted, 204 for nothing to report, or 201 for something it created, and
	// treating those as failures would refuse a perfectly good exchange. 3xx
	// does not reach here: the client follows redirects.
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		err := fmt.Errorf("provider returned %s: %s", resp.Status, explain(body))
		// 5xx and 429 are the provider asking to be tried again. Every other
		// 4xx is a statement about the request, which will not improve.
		if resp.StatusCode < http.StatusInternalServerError && resp.StatusCode != http.StatusTooManyRequests {
			return nil, doNotRetry(err)
		}
		return nil, err
	}
	return body, nil
}

// explainLimit is how much of a failed response is quoted. Enough for a
// provider's own message, short enough not to put a page of HTML in a log line
// or a NACK.
const explainLimit = 300

// explain renders a failed response body for a human.
//
// The body was already read and then thrown away, so a provider's own account
// of what was wrong -- Agmarknet says "no data" in the body of a 400 -- never
// reached anyone. The status alone says a call failed and nothing about why,
// which is the first thing an operator needs and the thing that makes a real
// provider's behaviour observable at all.
func explain(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "(no body)"
	}
	// Collapse whitespace: a provider that answers with indented JSON or an
	// HTML error page should not spread one failure over forty log lines.
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > explainLimit {
		return text[:explainLimit] + "... (truncated)"
	}
	return text
}

// authenticate presents this provider's credentials, read from the environment
// at call time so a rotated secret takes effect without a restart.
func (s *Step) authenticate(req *http.Request) error {
	switch s.config.AuthScheme {
	case AuthSchemeBasic:
		username, password := os.Getenv(s.config.UsernameEnv), os.Getenv(s.config.PasswordEnv)
		if username == "" || password == "" {
			return fmt.Errorf("upstream: %s and %s must both be set for basic auth",
				s.config.UsernameEnv, s.config.PasswordEnv)
		}
		req.SetBasicAuth(username, password)
	case AuthSchemeHeader:
		value := os.Getenv(s.config.HeaderValueEnv)
		if value == "" {
			return fmt.Errorf("upstream: %s must be set for header auth", s.config.HeaderValueEnv)
		}
		req.Header.Set(s.config.HeaderName, value)
	case AuthSchemeQuery:
		value := os.Getenv(s.config.QueryValueEnv)
		if value == "" {
			return fmt.Errorf("upstream: %s must be set for query auth", s.config.QueryValueEnv)
		}
		// Set rather than Add: a second copy of the parameter is not a
		// credential, it is an ambiguity, and which one an upstream reads is
		// its own business.
		query := req.URL.Query()
		query.Set(s.config.QueryName, value)
		req.URL.RawQuery = query.Encode()
	}
	return nil
}

// redact removes a query-string credential from an error's text.
//
// Go's transport errors quote the whole URL -- `Get "http://host/p?token=..."
// dial tcp: ...` -- so without this, one unreachable host writes the credential
// into the log at warn level. Nothing else in this package puts a URL in a
// message, which is why this is the only place it is needed.
//
// A plain string replacement, because the value is what leaks and the value is
// what we hold. Parsing the error to find it would assume a shape net/http does
// not promise.
func (s *Step) redact(err error) error {
	if err == nil {
		return nil
	}
	text := s.redactString(err.Error())
	if text == err.Error() {
		return err
	}
	return errors.New(text)
}

// redactString removes a query-string credential from any text about to be
// logged or returned -- an error, or the URL that was requested.
//
// Logging the URL is deliberate: it says what was asked of whom, which is the
// first thing anyone wants when a provider misbehaves. This is what makes that
// safe to do at info level.
func (s *Step) redactString(text string) string {
	if s.config.AuthScheme != AuthSchemeQuery {
		return text
	}
	value := os.Getenv(s.config.QueryValueEnv)
	if value == "" {
		return text
	}
	return strings.ReplaceAll(text, value, "REDACTED")
}

// buildEndpoint joins the plan's base URL and path, carrying the mapped request
// as query parameters when the method takes no body.
func buildEndpoint(baseURL string, call model.ActionPlan, mapped []byte) (string, error) {
	if err := verifyPath(call.Path); err != nil {
		return "", err
	}

	// baseUrl cannot end in a slash and path must begin with one, so exactly one
	// separator appears between them. The trim is belt and braces: the registry
	// refuses a trailing slash on baseUrl, and this keeps a row that predates
	// that from producing a doubled one.
	endpoint := strings.TrimSuffix(baseURL, "/") + call.Path
	if hasBody(call.Method) {
		return endpoint, nil
	}

	query, err := asQuery(mapped)
	if err != nil {
		return "", err
	}
	if query == "" {
		return endpoint, nil
	}
	if strings.Contains(endpoint, "?") {
		return endpoint + "&" + query, nil
	}
	return endpoint + "?" + query, nil
}

// verifyPath refuses a published path nobody could have meant.
//
// The registry constrains this, but it is a separate deployable that may not be
// updated in step, so a row that slipped through has to fail here with something
// an operator can act on rather than as a provider's 404 three hops away.
//
// An empty segment is the case worth catching: "//get-daily" is never
// deliberate, and plenty of servers answer it differently from "/get-daily". A
// trailing slash is deliberately left alone -- "/api/" and "/api" are a
// distinction some APIs genuinely make, so stripping it would silently change
// the URL the operator published.
func verifyPath(path string) error {
	if path == "" {
		return model.NewBadReqErr("", errors.New("upstream: the registry publishes no path for this action"))
	}
	if !strings.HasPrefix(path, "/") {
		return model.NewBadReqErr("", fmt.Errorf(
			"upstream: path %q does not begin with a slash, so it cannot be joined to a base url", path))
	}
	if strings.Contains(path, "//") {
		return model.NewBadReqErr("", fmt.Errorf(
			"upstream: path %q has an empty segment; write it with single slashes", path))
	}
	return nil
}

// asQuery renders a mapped request as query parameters.
//
// A method with no body still needs the mapping's output somewhere, and the
// query string is the only place it can go. Only scalars are carried: a nested
// value has no single obvious encoding, and inventing one here would put a
// convention in Go that belongs in the mapping.
func asQuery(mapped []byte) (string, error) {
	if len(bytes.TrimSpace(mapped)) == 0 {
		return "", nil
	}
	var fields map[string]any
	if err := json.Unmarshal(mapped, &fields); err != nil {
		return "", fmt.Errorf("upstream: mapped request is not an object, so it cannot become a query: %w", err)
	}

	values := url.Values{}
	for name, value := range fields {
		rendered, ok := renderScalar(value)
		if !ok {
			return "", fmt.Errorf("upstream: mapped field %q is not a scalar and cannot become a query parameter", name)
		}
		values.Set(name, rendered)
	}
	return values.Encode(), nil
}

// renderScalar renders a JSON scalar as a query parameter value.
func renderScalar(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case float64:
		// 'g' with -1 precision round-trips without inventing trailing zeros, so
		// 19.9975 stays 19.9975 rather than becoming 19.997500.
		return strconv.FormatFloat(typed, 'g', -1, 64), true
	default:
		return "", false
	}
}

// requestBody returns the body to send, which is none for methods that take none.
func requestBody(method string, mapped []byte) io.Reader {
	if !hasBody(method) {
		return nil
	}
	return bytes.NewReader(mapped)
}

// hasBody reports whether a method carries a request body.
func hasBody(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodDelete, "":
		return false
	default:
		return true
	}
}
