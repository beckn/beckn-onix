// Package mausamgram serves the IMD Mausamgram point-forecast capability.
//
// It is the first provider step, and the shape every other one follows: it
// recognises its own capability, resolves what the provider needs beyond the
// Beckn payload, calls it, and lets the mapper translate in both directions.
// Nothing about weather forecasts appears outside this package, and nothing
// about mapping appears inside it.
package mausamgram

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
	// DefaultBindingKey is the capability this step serves. It is configurable
	// so a deployment can rename the participant without a rebuild, but it has a
	// default because a step that serves nothing is never what an operator meant.
	DefaultBindingKey = "mausamgram|openagrinet:WeatherObservation"
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
	AuthSchemeNone   = "none"
	AuthSchemeBasic  = "basic"
	AuthSchemeHeader = "header"
)

// codeUpstreamUnavailable reports a provider that could not be reached or
// answered with a failure. It is not this adapter's fault and not the caller's.
const codeUpstreamUnavailable = "NET_DOWNSTREAM_UNAVAILABLE"

// Config holds configuration parameters for the step.
type Config struct {
	// BindingKey is the capability this step answers to. A request for anything
	// else passes through untouched.
	BindingKey string `yaml:"bindingKey" json:"bindingKey"`

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

	// MaxResponseBytes caps what is read from the provider.
	MaxResponseBytes int64 `yaml:"maxResponseBytes" json:"maxResponseBytes"`
}

// Step serves the Mausamgram capability. It is safe for concurrent use.
type Step struct {
	config     *Config
	registry   definition.ProviderRecordLookup
	mapper     definition.Mapper
	httpClient *http.Client
}

// New creates the step.
func New(ctx context.Context, registry definition.ProviderRecordLookup, mapper definition.Mapper, cfg *Config) (*Step, func() error, error) {
	if registry == nil {
		return nil, nil, errors.New("mausamgram: a provider record lookup is required")
	}
	if mapper == nil {
		return nil, nil, errors.New("mausamgram: a mapper is required")
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if err := applyDefaults(cfg); err != nil {
		return nil, nil, err
	}

	step := &Step{
		config:   cfg,
		registry: registry,
		mapper:   mapper,
		// Timeout is set per request from the registry's own budget, so the
		// client carries none of its own.
		httpClient: &http.Client{},
	}

	closer := func() error {
		log.Debugf(ctx, "Cleaning up mausamgram step resources")
		step.httpClient.CloseIdleConnections()
		return nil
	}

	log.Infof(ctx, "Mausamgram step created for binding %s", cfg.BindingKey)
	return step, closer, nil
}

// applyDefaults fills in what was left out and rejects what cannot be defaulted.
func applyDefaults(cfg *Config) error {
	if cfg.BindingKey == "" {
		cfg.BindingKey = DefaultBindingKey
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
			return errors.New("mausamgram: authScheme basic requires usernameEnv and passwordEnv")
		}
	case AuthSchemeHeader:
		if cfg.HeaderName == "" || cfg.HeaderValueEnv == "" {
			return errors.New("mausamgram: authScheme header requires headerName and headerValueEnv")
		}
	default:
		return fmt.Errorf("mausamgram: unknown authScheme %q: must be none, basic or header", cfg.AuthScheme)
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
	binding, err := oanbinding.From(ctx.Body)
	if errors.Is(err, oanbinding.ErrNoBinding) {
		return nil
	}
	if err != nil {
		return err
	}
	if binding.Key() != s.config.BindingKey {
		log.Debugf(ctx, "mausamgram: %s is not this step's capability, passing through", binding.Key())
		return nil
	}

	plan, err := s.registry.ProviderRecord(ctx, binding.Key())
	if err != nil {
		return fmt.Errorf("mausamgram: no call plan for %s: %w", binding.Key(), err)
	}

	return s.serve(ctx, plan)
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
			"mausamgram: %s does not serve action %q; it serves %s",
			plan.BindingKey, action, strings.Join(servedActions(plan), ", ")))
	}

	local, err := resolvePoint(ctx.Body)
	if err != nil {
		return err
	}

	beckn, err := decodeBody(ctx.Body)
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
		return fmt.Errorf("mausamgram: provider answered with something that is not JSON: %w", err)
	}

	// The same mapping reference as the request, other half: one file carries
	// both directions for this action.
	//
	// The mapping is handed what each party sent and nothing else. The values
	// resolved above are not passed in: this step holds them and used them to
	// make the call, so handing them to the mapping would be a second name for
	// the same data.
	becknResponse, err := s.mapper.Transform(ctx, call.Mappings, definition.DirectionResponse, map[string]any{
		"beckn":    beckn,
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
		return fmt.Errorf("mausamgram: the response half of %s produced nothing, so %s cannot be answered",
			call.Mappings, plan.BindingKey)
	}

	ctx.ResponseBody = becknResponse
	log.Infof(ctx, "mausamgram: served %s in %d bytes", plan.BindingKey, len(becknResponse))
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
// The mapping is handed the inbound payload and nothing else, and it produces a
// document or it produces nothing. Nothing is the ordinary case here: this
// provider takes its parameters in the query string, this step resolved them
// already, and putting them through a fetch and a compile to arrive at the same
// two fields would buy nothing.
//
// What nothing means depends on the method, and both readings are deliberate:
//
//   - a method with no body -- the resolved values ARE the parameters. This step
//     knows this provider, so it does not need the mapping's help to call it.
//   - a method with a body -- there is no body. An empty mapping means an empty
//     request, not the resolved values dressed up as one; a body is the
//     mapping's business and it supplied none.
//
// A half with no transform and a transform that matched nothing are treated
// alike here, deliberately: for the request leg there is no document either way,
// and inventing one would send the provider something nobody asked for.
func (s *Step) buildRequest(ctx context.Context, call model.ActionPlan, beckn any, local point) ([]byte, error) {
	mapped, err := s.mapper.Transform(ctx, call.Mappings, definition.DirectionRequest, map[string]any{
		"beckn": beckn,
	})
	if err != nil {
		return nil, err
	}
	if len(mapped) > 0 {
		return mapped, nil
	}

	if hasBody(call.Method) {
		log.Debugf(ctx, "mausamgram: the request half of %s produced nothing; sending no body", call.Mappings)
		return nil, nil
	}

	log.Debugf(ctx, "mausamgram: the request half of %s produced nothing; sending the resolved point", call.Mappings)
	parameters, err := json.Marshal(local)
	if err != nil {
		return nil, fmt.Errorf("mausamgram: could not encode the resolved point: %w", err)
	}
	return parameters, nil
}

// point is what this provider needs beyond the Beckn payload: a latitude and a
// longitude, as separate numbers.
type point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// resolvePoint reads the coordinates the request is asking about.
//
// This is the prerequisite step: the work a provider needs done before it can be
// called, which in the old per-provider services was tangled together with
// building the response. Here it produces values and nothing else, and the
// mapping decides what they are called upstream.
func resolvePoint(body []byte) (point, error) {
	var payload struct {
		Message struct {
			Contract struct {
				Commitments []struct {
					Resources []struct {
						ResourceAttributes struct {
							Location struct {
								Coordinates []float64 `json:"coordinates"`
							} `json:"location"`
						} `json:"resourceAttributes"`
					} `json:"resources"`
				} `json:"commitments"`
			} `json:"contract"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return point{}, fmt.Errorf("mausamgram: request could not be read: %w", err)
	}

	for _, commitment := range payload.Message.Contract.Commitments {
		for _, resource := range commitment.Resources {
			coordinates := resource.ResourceAttributes.Location.Coordinates
			if len(coordinates) < 2 {
				continue
			}
			// GeoJSON order: longitude first, then latitude. Reading these the
			// other way round yields a point in the wrong hemisphere that is
			// still a valid request, so it fails as wrong data rather than as an
			// error.
			return point{Lon: coordinates[0], Lat: coordinates[1]}, nil
		}
	}
	return point{}, model.NewBadReqErr("",
		errors.New("mausamgram: request carries no location coordinates"))
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
		return nil, fmt.Errorf("mausamgram: could not read JSON: %w", err)
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
		body, err := s.attempt(ctx, call, endpoint, mapped, timeout)
		if err == nil {
			return body, nil
		}
		lastErr = err
		log.Warnf(ctx, "mausamgram: attempt %d/%d failed: %v", attempt, attempts, err)
	}
	return nil, model.NewCodedErr(http.StatusBadGateway, codeUpstreamUnavailable,
		fmt.Errorf("mausamgram: provider did not answer after %d attempts: %w", attempts, lastErr))
}

// attempt makes one upstream request.
func (s *Step) attempt(ctx context.Context, call model.ActionPlan, endpoint string, mapped []byte, timeout time.Duration) ([]byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, call.Method, endpoint, requestBody(call.Method, mapped))
	if err != nil {
		return nil, fmt.Errorf("could not build the request: %w", err)
	}
	if hasBody(call.Method) {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := s.authenticate(req); err != nil {
		return nil, err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, s.config.MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("could not read the response: %w", err)
	}
	if int64(len(body)) > s.config.MaxResponseBytes {
		return nil, fmt.Errorf("response exceeds the %d byte limit", s.config.MaxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider returned %s", resp.Status)
	}
	return body, nil
}

// authenticate presents this provider's credentials, read from the environment
// at call time so a rotated secret takes effect without a restart.
func (s *Step) authenticate(req *http.Request) error {
	switch s.config.AuthScheme {
	case AuthSchemeBasic:
		username, password := os.Getenv(s.config.UsernameEnv), os.Getenv(s.config.PasswordEnv)
		if username == "" || password == "" {
			return fmt.Errorf("mausamgram: %s and %s must both be set for basic auth",
				s.config.UsernameEnv, s.config.PasswordEnv)
		}
		req.SetBasicAuth(username, password)
	case AuthSchemeHeader:
		value := os.Getenv(s.config.HeaderValueEnv)
		if value == "" {
			return fmt.Errorf("mausamgram: %s must be set for header auth", s.config.HeaderValueEnv)
		}
		req.Header.Set(s.config.HeaderName, value)
	}
	return nil
}

// buildEndpoint joins the plan's base URL and path, carrying the mapped request
// as query parameters when the method takes no body.
func buildEndpoint(baseURL string, call model.ActionPlan, mapped []byte) (string, error) {
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
		return "", fmt.Errorf("mausamgram: mapped request is not an object, so it cannot become a query: %w", err)
	}

	values := url.Values{}
	for name, value := range fields {
		rendered, ok := renderScalar(value)
		if !ok {
			return "", fmt.Errorf("mausamgram: mapped field %q is not a scalar and cannot become a query parameter", name)
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
