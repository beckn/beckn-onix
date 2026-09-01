package mausamgram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

const selectBody = `{
  "context": { "version": "2.0.0", "action": "select", "transactionId": "txn-1" },
  "message": { "contract": { "commitments": [{
    "resources": [{ "resourceAttributes": {
      "@type": "openagrinet:WeatherObservation",
      "location": { "type": "Point", "coordinates": [73.7898, 19.9975] } } }],
    "offer": { "provider": { "id": "mausamgram" } }
  }] } }
}`

// --- test doubles -----------------------------------------------------------

type stubRegistry struct {
	plan *model.ProviderRecord
	err  error
}

func (s *stubRegistry) ProviderRecord(context.Context, string) (*model.ProviderRecord, error) {
	return s.plan, s.err
}

// stubMapper records what it was asked and returns canned results, so a test can
// testMappingRef is the one reference an action carries: the URL of a single
// published file holding both halves.
const testMappingRef = "https://mappings.example.com/mausamgram/weather-observation.select.yaml"

// assert what reached the mapping without writing one.
type stubMapper struct {
	requestResult  []byte
	responseResult []byte
	err            error
	requestErr     error

	verifyErr error
	verified  bool

	requestInput  any
	responseInput any
	directions    []definition.Direction
	refs          []string
}

// verifyErr is what Verify answers with, so a test can stand in for a mapping
// whose precondition refused.
func (s *stubMapper) Verify(_ context.Context, mappingRef string, input any) error {
	s.verified = true
	return s.verifyErr
}

func (s *stubMapper) Transform(_ context.Context, mappingRef string, direction definition.Direction, input any) ([]byte, error) {
	s.directions = append(s.directions, direction)
	s.refs = append(s.refs, mappingRef)
	if s.err != nil {
		return nil, s.err
	}
	if direction == definition.DirectionRequest {
		s.requestInput = input
		if s.requestErr != nil {
			return nil, s.requestErr
		}
		return s.requestResult, nil
	}
	s.responseInput = input
	return s.responseResult, nil
}

func testPlan(baseURL, method string) *model.ProviderRecord {
	return &model.ProviderRecord{
		BindingKey:     DefaultBindingKey,
		ParticipantID:  "mausamgram",
		CapabilityCode: "openagrinet:WeatherObservation",
		BaseURL:        baseURL,
		Actions: map[string]model.ActionPlan{
			"select": {Method: method, Path: "/get-daily", Mappings: testMappingRef,
				TimeoutMs: 2000, RetryMax: 1},
		},
	}
}

func newStep(t *testing.T, registry definition.ProviderRecordLookup, mapper definition.Mapper, tweak ...func(*Config)) *Step {
	t.Helper()

	cfg := &Config{}
	for _, apply := range tweak {
		apply(cfg)
	}
	step, closer, err := New(context.Background(), registry, mapper, cfg)
	if err != nil {
		t.Fatalf("New() returned an unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = closer() })
	return step
}

func runStep(t *testing.T, step *Step, body string) (*model.StepContext, error) {
	t.Helper()
	ctx := &model.StepContext{Context: t.Context(), Body: []byte(body)}
	return ctx, step.Run(ctx)
}

// --- construction -----------------------------------------------------------

func TestNewRequiresItsDependencies(t *testing.T) {
	t.Parallel()

	if _, _, err := New(context.Background(), nil, &stubMapper{}, &Config{}); err == nil {
		t.Error("expected a missing registry to be refused")
	}
	if _, _, err := New(context.Background(), &stubRegistry{}, nil, &Config{}); err == nil {
		t.Error("expected a missing mapper to be refused")
	}
}

func TestNewValidatesTheAuthScheme(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		config *Config
		valid  bool
	}{
		{"none by default", &Config{}, true},
		{"basic with both variables", &Config{AuthScheme: AuthSchemeBasic, UsernameEnv: "U", PasswordEnv: "P"}, true},
		{"basic missing the password variable", &Config{AuthScheme: AuthSchemeBasic, UsernameEnv: "U"}, false},
		{"basic missing the username variable", &Config{AuthScheme: AuthSchemeBasic, PasswordEnv: "P"}, false},
		{"header with both settings", &Config{AuthScheme: AuthSchemeHeader, HeaderName: "X-Key", HeaderValueEnv: "V"}, true},
		{"header missing the value variable", &Config{AuthScheme: AuthSchemeHeader, HeaderName: "X-Key"}, false},
		{"an unknown scheme", &Config{AuthScheme: "oauth"}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := New(context.Background(), &stubRegistry{}, &stubMapper{}, tc.config)
			if tc.valid && err != nil {
				t.Errorf("expected the config to be accepted, got %v", err)
			}
			if !tc.valid && err == nil {
				t.Error("expected the config to be refused")
			}
		})
	}
}

// --- dispatch ---------------------------------------------------------------

// Passing through is how dispatch works: several provider steps share a
// pipeline, and each must leave alone what is not its own.
func TestRunPassesThroughWhatIsNotItsCapability(t *testing.T) {
	t.Parallel()

	testCases := []struct{ name, body string }{
		{"another provider", strings.Replace(selectBody, `"id": "mausamgram"`, `"id": "agmarknet"`, 1)},
		{"another capability", strings.Replace(selectBody, "openagrinet:WeatherObservation", "openagrinet:MarketPrice", 1)},
		{"a payload with no binding at all", `{"context":{"action":"select"},"message":{}}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			registry := &stubRegistry{err: errors.New("the registry must not be consulted")}
			ctx, err := runStep(t, newStep(t, registry, &stubMapper{}), tc.body)
			if err != nil {
				t.Fatalf("Run() returned an unexpected error: %v", err)
			}
			if ctx.ResponseBody != nil {
				t.Error("a passed-through request must not be answered")
			}
		})
	}
}

func TestRunReportsAnUnreadablePayload(t *testing.T) {
	t.Parallel()

	if _, err := runStep(t, newStep(t, &stubRegistry{}, &stubMapper{}), `{"message":`); err == nil {
		t.Error("expected unreadable JSON to be reported")
	}
}

// --- the exchange -----------------------------------------------------------

func TestRunServesItsCapabilityEndToEnd(t *testing.T) {
	t.Parallel()

	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"fcstday1":{"rain":12.4}}`)
	}))
	defer upstream.Close()

	mapper := &stubMapper{
		requestResult:  []byte(`{"lat":19.9975,"lon":73.7898}`),
		responseResult: []byte(`{"context":{"action":"on_select"}}`),
	}
	registry := &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}

	ctx, err := runStep(t, newStep(t, registry, mapper), selectBody)
	if err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}

	if string(ctx.ResponseBody) != `{"context":{"action":"on_select"}}` {
		t.Errorf("response body = %q, want the mapped answer", ctx.ResponseBody)
	}
	if !strings.Contains(gotQuery, "lat=19.9975") || !strings.Contains(gotQuery, "lon=73.7898") {
		t.Errorf("upstream query = %q, want the mapped fields", gotQuery)
	}
	// Each leg asks for the action it deals in: the request translates a select,
	// the response produces an on_select. Asking for the same name on both would
	// make one file unable to hold both directions.
	if want := []definition.Direction{definition.DirectionRequest, definition.DirectionResponse}; !slices.Equal(mapper.directions, want) {
		t.Errorf("mapper was asked for %v, want %v", mapper.directions, want)
	}
	// Both halves come from the one file the action names. Two references here
	// would mean the step had gone back to treating the legs as separate.
	if want := []string{testMappingRef, testMappingRef}; !slices.Equal(mapper.refs, want) {
		t.Errorf("mapper was handed %v, want both halves from %q", mapper.refs, testMappingRef)
	}
}

// The response mapping sees the point resolved before the call. The provider
// does not echo it back, so nothing else can supply it.
func TestRunKeepsResolvedValuesInScopeForTheResponse(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"fcstday1":{"rain":12.4}}`)
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}
	_, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper), selectBody)
	if err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}

	input, ok := mapper.responseInput.(map[string]any)
	if !ok {
		t.Fatalf("response input = %T, want a map", mapper.responseInput)
	}
	for _, key := range []string{"beckn", "response"} {
		if _, present := input[key]; !present {
			t.Errorf("response mapping cannot see %q", key)
		}
	}

	// A mapping is handed only what a party sent. Values this step resolved
	// before the call stay in the step: it holds them already and uses them
	// directly, so passing them through the mapping would be a detour and a
	// second name for the same data.
	if len(input) != 2 {
		t.Errorf("response input carries %d keys (%v), want exactly beckn and response",
			len(input), keysOf(input))
	}
}

// keysOf names what an input document carries, for a readable failure.
func keysOf(input map[string]any) []string {
	names := make([]string, 0, len(input))
	for name := range input {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// The request leg is handed the payload and nothing else.
func TestRunGivesTheRequestMappingOnlyTheInboundPayload(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}
	if _, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper), selectBody); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}

	input, ok := mapper.requestInput.(map[string]any)
	if !ok {
		t.Fatalf("request input = %T, want a map", mapper.requestInput)
	}
	if len(input) != 1 {
		t.Errorf("request input carries %v, want only beckn", keysOf(input))
	}
	if _, present := input["beckn"]; !present {
		t.Error("request mapping cannot see beckn")
	}
}

func TestRunSendsTheMappedBodyForAMethodThatTakesOne(t *testing.T) {
	t.Parallel()

	var gotBody, gotType string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotBody = string(body)
		gotType = r.Header.Get("Content-Type")
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: []byte(`{"lat":19.9975}`), responseResult: []byte(`{}`)}
	if _, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodPost)}, mapper), selectBody); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}

	if gotBody != `{"lat":19.9975}` {
		t.Errorf("upstream body = %q, want the mapped request", gotBody)
	}
	if gotType != "application/json" {
		t.Errorf("content type = %q, want application/json", gotType)
	}
}

// The mapping decides what the provider is asked, so what it produces IS the
// request. Nothing is substituted when it produces nothing: an empty request
// half means an empty request, on a method with a body or without one.
//
// This step used to extract a point itself and fall back to sending it. That put
// the choice of which payload fields reach the provider in Go, so adding a
// parameter meant a rebuild. It is the mapping's now.
func TestRunSendsWhatTheMappingProducedAsQueryParameters(t *testing.T) {
	t.Parallel()

	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	// Four fields, none of them known to this step: whatever the mapping named.
	mapper := &stubMapper{
		requestResult:  []byte(`{"lat":19.9975,"lon":73.7898,"from":"2026-08-30","to":"2026-09-03"}`),
		responseResult: []byte(`{}`),
	}
	if _, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper), selectBody); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}

	for _, want := range []string{"lat=19.9975", "lon=73.7898", "from=2026-08-30", "to=2026-09-03"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q is missing %q", gotQuery, want)
		}
	}
}

// An empty request half on a method with no body means no query parameters. The
// step has nothing of its own to send in their place.
func TestRunSendsNoQueryWhenTheMappingProducesNothing(t *testing.T) {
	t.Parallel()

	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: nil, responseResult: []byte(`{}`)}
	if _, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper), selectBody); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want none -- nothing is substituted for an empty mapping", gotQuery)
	}
}

// A method that takes a body, and a mapping that produces nothing, means no
// body -- not the resolved values dressed up as one. Query parameters are the
// step's own doing; a body is the mapping's, and there is nothing to send.
func TestRunSendsNoBodyWhenTheMappingProducesNothing(t *testing.T) {
	t.Parallel()

	var gotBody string
	var gotLength int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLength = r.ContentLength
		body := make([]byte, 64)
		n, _ := r.Body.Read(body)
		gotBody = string(body[:n])
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: nil, responseResult: []byte(`{}`)}
	if _, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodPost)}, mapper), selectBody); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}

	if gotLength > 0 || gotBody != "" {
		t.Errorf("upstream got a %d byte body (%q), want none", gotLength, gotBody)
	}
}

// A response half that produces nothing leaves no Beckn answer to return.
// Failing is the only honest outcome: answering with the provider's own shape
// would put a non-Beckn body on the wire under a valid signature.
func TestRunRefusesWhenTheResponseMappingProducesNothing(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"fcstday1":{"rain":12.4}}`)
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: nil}
	ctx, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper), selectBody)
	if err == nil {
		t.Fatal("expected an empty response mapping to be refused")
	}
	if len(ctx.ResponseBody) != 0 {
		t.Errorf("ResponseBody = %q, want nothing written", ctx.ResponseBody)
	}
}

// --- several capabilities, one step -------------------------------------------
//
// A provider can serve more than one capability -- the registry contract says so
// outright: "a provider serving two capabilities is one Participant and two
// ProviderSchema rows". The step has to be able to answer to all of them.
//
// It used to hold a single binding key, so a second capability meant a second
// providerSteps entry with the same plugin id. Those collide in the handler's
// id-keyed step map and the second silently wins, which loses a capability with
// no error anywhere.

func TestRunServesEveryBindingKeyItIsConfiguredFor(t *testing.T) {
	t.Parallel()

	for _, capability := range []string{
		"openagrinet:WeatherObservation",
		"openagrinet:WeatherAdvisory",
	} {
		t.Run(capability, func(t *testing.T) {
			t.Parallel()

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{}`)
			}))
			defer upstream.Close()

			key := "mausamgram|" + capability
			plan := testPlan(upstream.URL, http.MethodGet)
			plan.BindingKey = key

			mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}
			step := newStep(t, &stubRegistry{plan: plan}, mapper, func(c *Config) {
				c.BindingKeys = []string{
					"mausamgram|openagrinet:WeatherObservation",
					"mausamgram|openagrinet:WeatherAdvisory",
				}
			})

			body := strings.Replace(selectBody, "openagrinet:WeatherObservation", capability, 1)
			ctx, err := runStep(t, step, body)
			if err != nil {
				t.Fatalf("Run() returned an unexpected error: %v", err)
			}
			if len(ctx.ResponseBody) == 0 {
				t.Errorf("%s was not served, though the step is configured for it", key)
			}
		})
	}
}

// A capability the step is not configured for still passes through untouched --
// that is the dispatch mechanism, and widening to a list must not widen it into
// answering for everything.
func TestRunStillPassesThroughACapabilityItIsNotConfiguredFor(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the provider must not be called for another capability")
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}
	step := newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper,
		func(c *Config) { c.BindingKeys = []string{"someone-else|openagrinet:MandiPrice"} })

	ctx, err := runStep(t, step, selectBody)
	if err != nil {
		t.Fatalf("passing through must not be an error: %v", err)
	}
	if len(ctx.ResponseBody) != 0 {
		t.Error("the step answered for a capability it is not configured for")
	}
}

// Configured for nothing means the default capability, so an operator who names
// no binding key gets the one this plugin was written for rather than a step
// that answers to nothing.
func TestNewDefaultsToTheCapabilityThePluginIsFor(t *testing.T) {
	t.Parallel()

	step, closer, err := New(context.Background(), &stubRegistry{}, &stubMapper{}, &Config{})
	if err != nil {
		t.Fatalf("New() returned an unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = closer() })

	if got := step.config.BindingKeys; len(got) != 1 || got[0] != DefaultBindingKey {
		t.Errorf("binding keys = %v, want just the default %q", got, DefaultBindingKey)
	}
}

// A binding key naming no capability is a config mistake that would otherwise
// make the step answer to a key nothing can produce.
func TestNewRefusesAnEmptyBindingKey(t *testing.T) {
	t.Parallel()

	_, _, err := New(context.Background(), &stubRegistry{}, &stubMapper{},
		&Config{BindingKeys: []string{"mausamgram|openagrinet:WeatherObservation", "  "}})
	if err == nil {
		t.Error("expected an empty binding key to be refused")
	}
}

// --- preconditions ----------------------------------------------------------

// selectWithLocation renders a select payload whose resource carries the given
// GeoJSON geometry verbatim, or none at all when geometry is empty.
func selectWithLocation(t *testing.T, geometry string) string {
	t.Helper()
	location := ""
	if geometry != "" {
		location = `"location": ` + geometry + `,`
	}
	return `{
  "context": { "version": "2.0.0", "action": "select", "transactionId": "txn-1" },
  "message": { "contract": { "commitments": [ {
    "resources": [ { "id": "res:x", "resourceAttributes": {
      ` + location + `
      "@type": "openagrinet:WeatherObservation"
    } } ],
    "offer": { "id": "offer:x", "provider": { "id": "mausamgram" } }
  } ] } }
}`
}

// What a payload must satisfy is the mapping's rule, not this step's. The step's
// job is to ask, and to stop when the answer is no -- without calling the
// provider.
//
// This step used to hold the rule itself: it read the geometry and required a
// Point. That meant a capability with a different rule needed a different build.
// Which geometries the shipped mapping accepts is now asserted in
// mappings_test.go, against the published file.
func TestRunRefusesWhenTheMappingsPreconditionFails(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the provider must not be called when a precondition failed")
	}))
	defer upstream.Close()

	refusal := model.NewBadReqErr("", errors.New("this capability needs a Point location"))
	mapper := &stubMapper{verifyErr: refusal, requestResult: []byte(`{}`), responseResult: []byte(`{}`)}

	_, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper),
		selectWithLocation(t, `{"type":"Polygon","coordinates":[[[73.0,19.0],[74.0,19.0],[74.0,20.0],[73.0,19.0]]]}`))
	if err == nil {
		t.Fatal("expected a failed precondition to be refused")
	}
	// Propagated verbatim: the mapping's message is what the caller reads.
	if !errors.Is(err, refusal) {
		t.Errorf("error = %v, want the mapping's own refusal propagated", err)
	}
}

// Preconditions are checked before the request is built, so a mapping can refuse
// a payload its own request half could not have read.
func TestRunChecksPreconditionsBeforeBuildingTheRequest(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the provider must not be called when a precondition failed")
	}))
	defer upstream.Close()

	mapper := &stubMapper{
		verifyErr:      model.NewBadReqErr("", errors.New("nope")),
		requestResult:  []byte(`{}`),
		responseResult: []byte(`{}`),
	}
	if _, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper),
		selectBody); err == nil {
		t.Fatal("expected a refusal")
	}
	if mapper.requestInput != nil {
		t.Error("the request half ran despite a failed precondition")
	}
}

// A mapping declaring no preconditions imposes none, and the step asks anyway --
// so adopting the facility is per provider, not all at once.
func TestRunProceedsWhenTheMappingImposesNothing(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}
	if _, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper),
		selectWithLocation(t, `{"type":"Polygon","coordinates":[[[1.0,2.0]]]}`)); err != nil {
		t.Fatalf("a mapping imposing nothing must let a request through: %v", err)
	}
	if !mapper.verified {
		t.Error("the step did not ask the mapping at all")
	}
}

// A mapping that failed is a failure, and must not be papered over by sending
// the resolved values instead.
func TestRunDoesNotSubstituteForARealMappingFailure(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the provider must not be called when the mapping failed")
	}))
	defer upstream.Close()

	wantErr := errors.New("mapping is broken")
	mapper := &stubMapper{requestErr: wantErr, responseResult: []byte(`{}`)}

	_, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper), selectBody)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the mapping failure to propagate, got %v", err)
	}
}

// --- authentication ---------------------------------------------------------

func TestRunPresentsBasicCredentialsFromTheEnvironment(t *testing.T) {
	t.Setenv("TEST_MAUSAMGRAM_USER", "user-1")
	t.Setenv("TEST_MAUSAMGRAM_KEY", "key-1")

	var gotUser, gotPass string
	var hadAuth bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, hadAuth = r.BasicAuth()
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	step := newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)},
		&stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)},
		func(c *Config) {
			c.AuthScheme = AuthSchemeBasic
			c.UsernameEnv = "TEST_MAUSAMGRAM_USER"
			c.PasswordEnv = "TEST_MAUSAMGRAM_KEY"
		})

	if _, err := runStep(t, step, selectBody); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}
	if !hadAuth || gotUser != "user-1" || gotPass != "key-1" {
		t.Errorf("basic auth = (%q, %q, present=%v), want the configured credentials", gotUser, gotPass, hadAuth)
	}
}

func TestRunPresentsAHeaderCredentialFromTheEnvironment(t *testing.T) {
	t.Setenv("TEST_MAUSAMGRAM_TOKEN", "token-1")

	var gotHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	step := newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)},
		&stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)},
		func(c *Config) {
			c.AuthScheme = AuthSchemeHeader
			c.HeaderName = "X-Api-Key"
			c.HeaderValueEnv = "TEST_MAUSAMGRAM_TOKEN"
		})

	if _, err := runStep(t, step, selectBody); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}
	if gotHeader != "token-1" {
		t.Errorf("X-Api-Key = %q, want token-1", gotHeader)
	}
}

// A configured credential that is not in the environment is a deployment fault,
// and must fail rather than call the provider unauthenticated.
func TestRunFailsWhenAConfiguredCredentialIsAbsent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the provider must not be called without its credentials")
	}))
	defer upstream.Close()

	step := newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)},
		&stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)},
		func(c *Config) {
			c.AuthScheme = AuthSchemeBasic
			c.UsernameEnv = "TEST_MAUSAMGRAM_ABSENT_USER"
			c.PasswordEnv = "TEST_MAUSAMGRAM_ABSENT_KEY"
		})

	if _, err := runStep(t, step, selectBody); err == nil {
		t.Error("expected a missing credential to fail the request")
	}
}

// --- failures ---------------------------------------------------------------

func TestRunPropagatesAFailedLookup(t *testing.T) {
	t.Parallel()

	registry := &stubRegistry{err: definition.ErrProviderRecordNotFound}
	_, err := runStep(t, newStep(t, registry, &stubMapper{}), selectBody)
	if !errors.Is(err, definition.ErrProviderRecordNotFound) {
		t.Errorf("expected the lookup failure to propagate, got %v", err)
	}
}

func TestRunPropagatesAFailedMapping(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("mapping is broken")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	registry := &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}
	_, err := runStep(t, newStep(t, registry, &stubMapper{err: wantErr}), selectBody)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the mapping failure to propagate, got %v", err)
	}
}

func TestRunReportsAProviderThatWillNotAnswer(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	plan := testPlan(upstream.URL, http.MethodGet)
	plan.Actions["select"] = model.ActionPlan{Method: http.MethodGet, Path: "/get-daily",
		Mappings: testMappingRef, RetryMax: 3}
	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}

	_, err := runStep(t, newStep(t, &stubRegistry{plan: plan}, mapper), selectBody)
	if err == nil {
		t.Fatal("expected a failing provider to be reported")
	}
	// retryMax is retries, so the plan's 3 is the first call plus three more.
	if got := attempts.Load(); got != 4 {
		t.Errorf("made %d attempts, want 4 -- the call plus the plan's 3 retries", got)
	}

	var coded *model.CodedErr
	if !errors.As(err, &coded) || coded.HTTPStatus() != http.StatusBadGateway {
		t.Errorf("expected a 502 coded error, got %v", err)
	}
}

// An action that leaves retryMax out is called once. The contract's default is
// zero retries, and it has to stay zero: a retry on a non-idempotent action is
// a second booking, so retrying is only ever what the operator asked for.
func TestRunDoesNotRetryUnlessTheActionSaysSo(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	plan := testPlan(upstream.URL, http.MethodGet)
	plan.Actions["select"] = model.ActionPlan{Method: http.MethodGet, Path: "/get-daily",
		Mappings: testMappingRef}
	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}

	if _, err := runStep(t, newStep(t, &stubRegistry{plan: plan}, mapper), selectBody); err == nil {
		t.Fatal("expected a failing provider to be reported")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("made %d attempts, want 1 -- an absent retryMax must mean no retries", got)
	}
}

// A capability that publishes no endpoint for an action does not serve it. The
// refusal has to come before the call, not after one to the wrong place.
func TestRunRefusesAnActionWithNoEndpoint(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the provider must not be called for an action it does not serve")
	}))
	defer upstream.Close()

	// The plan serves select; the request is a confirm for the same capability.
	confirmBody := strings.Replace(selectBody, `"action": "select"`, `"action": "confirm"`, 1)
	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}

	_, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper), confirmBody)
	if err == nil {
		t.Fatal("expected an action with no endpoint to be refused")
	}
	if !strings.Contains(err.Error(), "confirm") {
		t.Errorf("error %q should name the action that was asked for", err)
	}
	// Naming what the capability does serve turns a registry mistake into a
	// one-line fix.
	if !strings.Contains(err.Error(), "select") {
		t.Errorf("error %q should say which actions the capability does serve", err)
	}
}

// Each action carries its own endpoint and budget: a confirm that commits
// rarely posts where a select that reads gets.
func TestRunUsesTheEndpointForTheRequestedAction(t *testing.T) {
	t.Parallel()

	var gotPath, gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	plan := testPlan(upstream.URL, http.MethodGet)
	plan.Actions["confirm"] = model.ActionPlan{Method: http.MethodPost, Path: "/book", TimeoutMs: 2000, RetryMax: 1}

	confirmBody := strings.Replace(selectBody, `"action": "select"`, `"action": "confirm"`, 1)
	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}

	if _, err := runStep(t, newStep(t, &stubRegistry{plan: plan}, mapper), confirmBody); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/book" {
		t.Errorf("called %s %s, want POST /book -- the select endpoint was used", gotMethod, gotPath)
	}
}

func TestRunReportsAProviderAnsweringWithNonJSON(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>down for maintenance</html>")
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: []byte(`{}`), responseResult: []byte(`{}`)}
	_, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper), selectBody)
	if err == nil {
		t.Error("expected a non-JSON answer to be reported")
	}
}

func TestRunReportsARequestWithNoCoordinates(t *testing.T) {
	t.Parallel()

	body := strings.Replace(selectBody, `"location": { "type": "Point", "coordinates": [73.7898, 19.9975] }`, `"location": {}`, 1)
	registry := &stubRegistry{plan: testPlan("http://upstream.invalid", http.MethodGet)}

	_, err := runStep(t, newStep(t, registry, &stubMapper{}), body)
	if err == nil {
		t.Error("expected a request with no coordinates to be refused")
	}
}

// A mapping producing something that cannot become a query has to fail loudly:
// dropping the field would call the provider with the wrong question.
func TestRunRefusesAMappedQueryItCannotRender(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the provider must not be called with an incomplete query")
	}))
	defer upstream.Close()

	mapper := &stubMapper{requestResult: []byte(`{"box":{"nested":true}}`), responseResult: []byte(`{}`)}
	_, err := runStep(t, newStep(t, &stubRegistry{plan: testPlan(upstream.URL, http.MethodGet)}, mapper), selectBody)
	if err == nil {
		t.Error("expected a non-scalar mapped field to be refused")
	}
}

// --- query rendering --------------------------------------------------------

func TestAsQueryRendersScalarsWithoutInventingPrecision(t *testing.T) {
	t.Parallel()

	got, err := asQuery([]byte(`{"lat":19.9975,"count":3,"name":"imd","live":true}`))
	if err != nil {
		t.Fatalf("asQuery() returned an unexpected error: %v", err)
	}
	for _, want := range []string{"lat=19.9975", "count=3", "name=imd", "live=true"} {
		if !strings.Contains(got, want) {
			t.Errorf("query %q is missing %q", got, want)
		}
	}
}

func TestAsQueryHandlesAnEmptyMapping(t *testing.T) {
	t.Parallel()

	got, err := asQuery([]byte(`{}`))
	if err != nil || got != "" {
		t.Errorf("asQuery({}) = (%q, %v), want an empty query and no error", got, err)
	}
}
