package jsonmapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// bothDirections is the published form: one binding-action, both halves.
//
// A mapping reads only what a party sent -- the inbound payload, and on the way
// back the provider's answer. Values a provider plugin resolved before the call
// are not passed in: the plugin holds them already and uses them directly, so
// routing them through the mapping would be a detour.
const bothDirections = `request: |
  { "txn": beckn.context.transactionId }

response: |
  { "txn": beckn.context.transactionId, "rain": response.fcstday1.rain }
`

// requestOnly is the shape of a mapping whose answer needs no translation, or
// whose response half has not been written yet.
const requestOnly = `request: |
  { "txn": beckn.context.transactionId }
`

func requestInput() map[string]any {
	return map[string]any{
		"beckn": map[string]any{"context": map[string]any{"transactionId": "txn-123", "messageId": "msg-1"}},
	}
}

func responseInput() map[string]any {
	input := requestInput()
	input["response"] = map[string]any{"fcstday1": map[string]any{"rain": 12.4}}
	return input
}

// newMappingServer serves body at every path and counts what was asked for.
func newMappingServer(t *testing.T, body string, fetches *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fetches != nil {
			fetches.Add(1)
		}
		fmt.Fprint(w, body)
	}))
}

func newTestMapper(t *testing.T, tweak ...func(*Config)) *Mapper {
	t.Helper()

	cfg := &Config{
		FetchTimeout:    2 * time.Second,
		MaxMappingBytes: DefaultMaxMappingBytes,
		CacheTTL:        time.Minute,
		NegativeTTL:     time.Minute,
		MaxCacheEntries: DefaultMaxCacheEntries,
	}
	for _, apply := range tweak {
		apply(cfg)
	}

	mapper, closer, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() returned an unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = closer() })
	return mapper
}

// ref is what the registry carries: the fully-qualified URL of one published
// file. Which action it serves is decided by the registry entry pointing at it,
// so the name carries no meaning here.
func ref(base string) string {
	return base + "/mappings/mausamgram/weather-observation.select.yaml"
}

// --- transformation --------------------------------------------------------

func TestTransformRunsTheRequestHalf(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, bothDirections, nil)
	defer srv.Close()

	got, err := newTestMapper(t).Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput())
	if err != nil {
		t.Fatalf("Transform() returned an unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("failed to decode the result: %v", err)
	}
	if result["txn"] != "txn-123" {
		t.Errorf("txn = %v, want txn-123 -- beckn was not reachable", result["txn"])
	}
}

// The response half reads the upstream answer under response, alongside the
// original payload under beckn -- the context to echo and the offer to quote
// against are only in the request that produced the answer.
func TestTransformRunsTheResponseHalfAlongsideTheRequest(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, bothDirections, nil)
	defer srv.Close()

	got, err := newTestMapper(t).Transform(context.Background(), ref(srv.URL), definition.DirectionResponse, responseInput())
	if err != nil {
		t.Fatalf("Transform() returned an unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("failed to decode the result: %v", err)
	}
	for _, field := range []struct {
		key  string
		want any
	}{{"txn", "txn-123"}, {"rain", 12.4}} {
		if result[field.key] != field.want {
			t.Errorf("%s = %v, want %v", field.key, result[field.key], field.want)
		}
	}
}

// The two halves are separate expressions, not one applied twice.
func TestTransformKeepsTheHalvesApart(t *testing.T) {
	t.Parallel()

	mapping := `request: |
  { "leg": "out" }

response: |
  { "leg": "back" }
`
	srv := newMappingServer(t, mapping, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	out, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput())
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	back, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionResponse, responseInput())
	if err != nil {
		t.Fatalf("response: %v", err)
	}

	if !strings.Contains(string(out), `"out"`) {
		t.Errorf("request produced %s, want the request half's output", out)
	}
	if !strings.Contains(string(back), `"back"`) {
		t.Errorf("response produced %s, want the response half's output", back)
	}
}

// --- a half with no transform ----------------------------------------------

// A half that is absent, or present and empty, has no transform to apply. That
// is not a failure and not a special case: it produces nothing, and the caller
// decides what nothing means for the leg it is on. A request half with no
// transform means no request document -- so no body.
func TestTransformProducesNothingForAHalfWithNoTransform(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		mapping string
	}{
		{"the half is absent", requestOnly},
		{"the half is present and empty", "request: |\n  { \"txn\": beckn.context.transactionId }\nresponse: \"\"\n"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newMappingServer(t, tc.mapping, nil)
			defer srv.Close()
			mapper := newTestMapper(t)

			got, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionResponse, responseInput())
			if err != nil {
				t.Errorf("a half with no transform is not an error, got %v", err)
			}
			if len(got) != 0 {
				t.Errorf("produced %q, want nothing", got)
			}

			// The other half is unaffected: one direction having no transform
			// says nothing about the other.
			out, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput())
			if err != nil {
				t.Errorf("the other half must still be served: %v", err)
			}
			if len(out) == 0 {
				t.Error("the other half produced nothing, want its output")
			}
		})
	}
}

// Nothing and a failure must stay distinguishable. A half that will not compile
// produces an error, not nothing -- reading it as nothing would send an unmapped
// upstream answer out as a Beckn response.
func TestTransformSeparatesNothingFromAFailure(t *testing.T) {
	t.Parallel()

	mapping := `request: ""

response: |
  {{{
`
	srv := newMappingServer(t, mapping, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	got, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput())
	if err != nil || len(got) != 0 {
		t.Errorf("the empty half: got %q, %v -- want nothing and no error", got, err)
	}

	if _, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionResponse, responseInput()); err == nil {
		t.Error("the uncompilable half must report an error, not nothing")
	}
}

// --- preconditions ----------------------------------------------------------
//
// A mapping decides what a provider is asked for. It has to be able to decide
// that a request cannot be served at all, or that judgement stays in Go and
// every provider with its own rule needs its own build.
//
// required is a list of predicates over the same payload the request half reads.
// A predicate that is false refuses the request, carrying the message the
// mapping supplied -- so the caller gets a sentence about their payload rather
// than a mapping error about ours.

const withChecks = `required:
  - check: beckn.message.location.type = "Point"
    message: "this capability needs a Point location"
  - check: $exists(beckn.message.validity)
    message: "this capability needs a validity window"

request: |
  { "txn": beckn.context.transactionId }

response: |
  { "txn": beckn.context.transactionId }
`

// verifyInput is a payload that satisfies withChecks. The beckn wrapper is
// what the caller passes, so a precondition reads the payload by the same name
// the request half does.
func verifyInput() map[string]any {
	return map[string]any{
		"beckn": map[string]any{
			"context": map[string]any{"transactionId": "txn-123"},
			"message": map[string]any{
				"location": map[string]any{"type": "Point", "coordinates": []any{73.7898, 19.9975}},
				"validity": map[string]any{"startsAt": "2026-09-01"},
			},
		},
	}
}

// becknOf reaches into the wrapper, so a test can spoil one field.
func becknOf(input map[string]any) map[string]any {
	return input["beckn"].(map[string]any)
}

func TestVerifyPassesWhenEveryPredicateHolds(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, withChecks, nil)
	defer srv.Close()

	if err := newTestMapper(t).Verify(context.Background(), ref(srv.URL), verifyInput()); err != nil {
		t.Errorf("Verify() refused a payload that satisfies every predicate: %v", err)
	}
}

// The message belongs to the mapping, so the caller is told what is wrong with
// their payload rather than that an expression failed.
func TestVerifyRefusesWithTheMappingsOwnMessage(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, withChecks, nil)
	defer srv.Close()

	input := verifyInput()
	becknOf(input)["message"].(map[string]any)["location"] = map[string]any{"type": "Polygon"}

	err := newTestMapper(t).Verify(context.Background(), ref(srv.URL), input)
	if err == nil {
		t.Fatal("expected a payload failing a predicate to be refused")
	}
	if !strings.Contains(err.Error(), "needs a Point location") {
		t.Errorf("error %q should carry the mapping's own message", err)
	}

	// A bad request: the payload is the caller's, so this must not read as a
	// fault of this adapter.
	var coded *model.CodedErr
	if !errors.As(err, &coded) || coded.HTTPStatus() != http.StatusBadRequest {
		t.Errorf("error is %T, want a 400 so the caller is not blamed for our fault: %v", err, err)
	}
}

// Predicates are checked in order and the first failure is the one reported.
// Reporting the last, or all of them, buries the thing to fix.
func TestVerifyReportsTheFirstFailure(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, withChecks, nil)
	defer srv.Close()

	// Both predicates fail.
	input := verifyInput()
	becknOf(input)["message"] = map[string]any{"location": map[string]any{"type": "Polygon"}}

	err := newTestMapper(t).Verify(context.Background(), ref(srv.URL), input)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "needs a Point location") {
		t.Errorf("error %q should report the first failing predicate", err)
	}
	if strings.Contains(err.Error(), "validity window") {
		t.Error("only the first failure should be reported")
	}
}

// A mapping that declares no preconditions imposes none. That is what lets one
// provider adopt the key while others have not, so a second provider arriving
// with its own rules does not force every existing mapping to be rewritten.
func TestVerifyAllowsAMappingWithNoChecks(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, bothDirections, nil)
	defer srv.Close()

	if err := newTestMapper(t).Verify(context.Background(), ref(srv.URL), requestInput()); err != nil {
		t.Errorf("a mapping with no required block must impose nothing: %v", err)
	}
}

// A predicate has to answer true or false. Anything else -- a string, a number,
// nothing at all -- is a mapping fault, and must not be read as permission: a
// typo that yields undefined would otherwise wave every request through.
func TestVerifyRefusesAPredicateThatIsNotABoolean(t *testing.T) {
	t.Parallel()

	testCases := []struct{ name, check string }{
		{"a string", `"yes"`},
		{"a number", `1`},
		{"a field that does not exist", `beckn.message.nothing.here`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mapping := "required:\n  - check: " + tc.check + "\n    message: \"nope\"\n\nrequest: |\n  { \"a\": 1 }\n"
			srv := newMappingServer(t, mapping, nil)
			defer srv.Close()

			if err := newTestMapper(t).Verify(context.Background(), ref(srv.URL), verifyInput()); err == nil {
				t.Error("a predicate that is not a boolean must be refused, not treated as permission")
			}
		})
	}
}

// A predicate that will not compile is the mapping's fault, and is reported as
// one -- but it must not take the halves down with it, exactly as a broken half
// does not take its sibling down.
func TestVerifyIsolatesAPredicateThatWillNotCompile(t *testing.T) {
	t.Parallel()

	mapping := `required:
  - check: "{{{"
    message: "unreachable"

request: |
  { "txn": beckn.context.transactionId }
`
	srv := newMappingServer(t, mapping, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	if err := mapper.Verify(context.Background(), ref(srv.URL), verifyInput()); err == nil {
		t.Error("expected an uncompilable predicate to be reported")
	}
	// The request half still works: a broken precondition is not a broken file.
	if _, err := mapper.Transform(context.Background(), ref(srv.URL),
		definition.DirectionRequest, verifyInput()); err != nil {
		t.Errorf("the request half must still be served: %v", err)
	}
}

// An entry with no message is a mapping that refuses without saying why, which
// is the failure this whole key exists to avoid.
func TestVerifyRefusesAPredicateWithNoMessage(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, "required:\n  - check: 'false'\n\nrequest: |\n  { \"a\": 1 }\n", nil)
	defer srv.Close()

	if err := newTestMapper(t).Verify(context.Background(), ref(srv.URL), verifyInput()); err == nil {
		t.Error("a predicate with no reject message must be refused as a mapping fault")
	}
}

// Preconditions are fetched and compiled with the halves: one round trip leaves
// the whole file ready.
func TestVerifyCompilesWithTheRestOfTheFile(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	srv := newMappingServer(t, withChecks, &fetches)
	defer srv.Close()
	mapper := newTestMapper(t)

	if err := mapper.Verify(context.Background(), ref(srv.URL), verifyInput()); err != nil {
		t.Fatalf("Verify() returned an unexpected error: %v", err)
	}
	if _, err := mapper.Transform(context.Background(), ref(srv.URL),
		definition.DirectionRequest, verifyInput()); err != nil {
		t.Fatalf("Transform() returned an unexpected error: %v", err)
	}
	if got := fetches.Load(); got != 1 {
		t.Errorf("fetched %d times, want 1 -- preconditions refetched the file", got)
	}
}

// --- direction validation ---------------------------------------------------

// A direction outside the two is a caller bug, not a mapping problem, and must
// not be read as either half.
func TestTransformRefusesAnUnknownDirection(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, bothDirections, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	for _, direction := range []definition.Direction{"", "on_select", "REQUEST"} {
		if _, err := mapper.Transform(context.Background(), ref(srv.URL), direction, requestInput()); err == nil {
			t.Errorf("expected direction %q to be refused", direction)
		}
	}
}

// The filename says nothing: the registry entry that points at a file decides
// which action it serves, so a mapper reading meaning into the path would give
// the same file two answers.
func TestTransformIgnoresTheFilename(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, bothDirections, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	for _, name := range []string{"/anything.yaml", "/confirm.yaml", "/x/y/z"} {
		if _, err := mapper.Transform(context.Background(), srv.URL+name, definition.DirectionRequest, requestInput()); err != nil {
			t.Errorf("Transform(%q) returned an unexpected error: %v", name, err)
		}
	}
}

// --- reference validation ---------------------------------------------------

// A reference comes from the registry, so it is external input rather than
// something to trust. This cannot constrain WHICH host -- a registry record
// chooses that -- but it can refuse a reference that is not a fetchable http
// URL at all, which is what stops a record naming a local file and having the
// adapter read it.
func TestTransformRefusesAnUnusableReference(t *testing.T) {
	t.Parallel()

	testCases := []struct{ name, ref string }{
		{"empty", ""},
		{"a bare path with no scheme", "/mappings/select.yaml"},
		{"a relative path", "mappings/select.yaml"},
		{"a file url", "file:///etc/passwd"},
		{"a scheme that is not http", "ftp://example.com/select.yaml"},
		{"no host", "http:///select.yaml"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := newTestMapper(t).Transform(context.Background(), tc.ref, definition.DirectionRequest, requestInput()); err == nil {
				t.Errorf("expected reference %q to be refused", tc.ref)
			}
		})
	}
}

// --- fetch and parse failures -----------------------------------------------

func TestTransformReportsAFailedFetch(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "a not-found status", status: http.StatusNotFound},
		{name: "a server error status", status: http.StatusInternalServerError},
		{name: "malformed yaml", status: http.StatusOK, body: "request: [unclosed"},
		{name: "neither half", status: http.StatusOK, body: "other: value\n"},
		{name: "an empty document", status: http.StatusOK, body: "\n"},
		// Both halves present and empty is a file that says nothing, and is
		// refused whole rather than per half -- there is no half left to serve.
		{name: "both halves empty", status: http.StatusOK, body: "request: \"\"\nresponse: \"\"\n"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
					return
				}
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			if _, err := newTestMapper(t).Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput()); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// One unusable half must not take the other down with it: a typo in the response
// mapping is no reason to stop making the call, and finding out on the way back
// beats finding out before the call was made.
func TestTransformIsolatesABrokenHalf(t *testing.T) {
	t.Parallel()

	mapping := `request: |
  { "txn": beckn.context.transactionId }

response: |
  {{{
`
	srv := newMappingServer(t, mapping, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	if _, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput()); err != nil {
		t.Errorf("the healthy half must still be served: %v", err)
	}
	err := func() error {
		_, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionResponse, responseInput())
		return err
	}()
	if err == nil {
		t.Fatal("expected an uncompilable half to be refused")
	}
}

// A mapping is fetched into memory and compiled, so an unbounded one is an
// unbounded allocation driven by whoever can write the registry record.
func TestTransformEnforcesASizeCap(t *testing.T) {
	t.Parallel()

	oversized := "request: |\n  " + strings.Repeat("x", 2048) + "\n"
	srv := newMappingServer(t, oversized, nil)
	defer srv.Close()

	mapper := newTestMapper(t, func(c *Config) { c.MaxMappingBytes = 512 })
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput()); err == nil {
		t.Fatal("expected an oversized mapping to be refused")
	}
}

// A mapping host that accepts the connection and then goes quiet must not hold
// a request open indefinitely.
func TestTransformBoundsTheFetch(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	mapper := newTestMapper(t, func(c *Config) { c.FetchTimeout = 50 * time.Millisecond })

	done := make(chan error, 1)
	go func() {
		_, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput())
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected a stalled fetch to fail")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Transform() did not return: the fetch is unbounded")
	}
}

// --- caching ----------------------------------------------------------------

// Compiling is the expensive half, and it cannot be cached anywhere but in
// memory: a compiled expression is code, not data.
func TestTransformCompilesEachMappingOnce(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	srv := newMappingServer(t, bothDirections, &fetches)
	defer srv.Close()

	mapper := newTestMapper(t)
	for i := 0; i < 3; i++ {
		if _, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput()); err != nil {
			t.Fatalf("Transform() returned an unexpected error: %v", err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Errorf("fetched %d times, want 1 -- the mapping is being refetched per request", got)
	}
}

// One fetch serves both halves. This is the practical gain of one file: the
// response leg of a round trip does not pay a second round trip to be mapped.
func TestTransformFetchesOnceForBothHalves(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	srv := newMappingServer(t, bothDirections, &fetches)
	defer srv.Close()

	mapper := newTestMapper(t)
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput()); err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionResponse, responseInput()); err != nil {
		t.Fatalf("response: %v", err)
	}
	if got := fetches.Load(); got != 1 {
		t.Errorf("fetched %d times, want 1 -- the response half refetched the file", got)
	}
}

// Two references are two mappings even when they compile to the same thing.
func TestTransformCachesPerReference(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	srv := newMappingServer(t, bothDirections, &fetches)
	defer srv.Close()

	mapper := newTestMapper(t)
	for _, r := range []string{srv.URL + "/a.yaml", srv.URL + "/b.yaml"} {
		if _, err := mapper.Transform(context.Background(), r, definition.DirectionRequest, requestInput()); err != nil {
			t.Fatalf("Transform() returned an unexpected error: %v", err)
		}
	}
	if got := fetches.Load(); got != 2 {
		t.Errorf("fetched %d times, want 2 -- distinct references shared a cache entry", got)
	}
}

// A reference that cannot be fetched must not be retried on every request: a
// broken mapping would otherwise turn each inbound message into an outbound one.
func TestTransformNegativeCachesAFailure(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	mapper := newTestMapper(t)
	for i := 0; i < 3; i++ {
		if _, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput()); err == nil {
			t.Fatal("expected a missing mapping to fail")
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Errorf("fetched %d times, want 1 -- a broken reference is being retried per request", got)
	}
}

// An expired entry is refetched, so a corrected mapping takes effect without a
// restart.
func TestTransformRefetchesAfterTheTTL(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	srv := newMappingServer(t, bothDirections, &fetches)
	defer srv.Close()

	mapper := newTestMapper(t, func(c *Config) { c.CacheTTL = 20 * time.Millisecond })
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput()); err != nil {
		t.Fatalf("Transform() returned an unexpected error: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), definition.DirectionRequest, requestInput()); err != nil {
		t.Fatalf("Transform() returned an unexpected error: %v", err)
	}

	if got := fetches.Load(); got != 2 {
		t.Errorf("fetched %d times, want 2 -- an expired mapping was not refetched", got)
	}
}

// The cache is bounded: references come from the registry, so an unbounded one
// would grow with the number of capabilities ever seen.
func TestTransformBoundsTheCache(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, bothDirections, nil)
	defer srv.Close()

	mapper := newTestMapper(t, func(c *Config) { c.MaxCacheEntries = 2 })
	for i := 0; i < 5; i++ {
		if _, err := mapper.Transform(context.Background(),
			fmt.Sprintf("%s/%d.yaml", srv.URL, i), definition.DirectionRequest, requestInput()); err != nil {
			t.Fatalf("Transform() returned an unexpected error: %v", err)
		}
	}
	if got := mapper.cachedCount(); got > 2 {
		t.Errorf("cache holds %d entries, want at most 2", got)
	}
}

// Expired entries must not hold the cap. They used to: cached() treats an
// expiry as a miss but left the entry in the map, nothing ever deleted one, so
// the count only grew -- and once it reached MaxCacheEntries the cache refused
// every reference it was not already holding, permanently. The effect was not
// "compile again next time" but compile every time, for every mapping the
// deployment had.
func TestTransformKeepsCachingAfterEntriesExpire(t *testing.T) {
	t.Parallel()

	var fetches int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fetches++
		mu.Unlock()
		fmt.Fprint(w, bothDirections)
	}))
	defer srv.Close()

	// A TTL short enough to expire between calls, and a cap small enough that
	// stale entries would fill it.
	mapper := newTestMapper(t, func(c *Config) {
		c.MaxCacheEntries = 3
		c.CacheTTL = time.Millisecond
	})

	// Fill the cache and let everything in it go stale.
	for i := 0; i < 3; i++ {
		if _, err := mapper.Transform(context.Background(),
			fmt.Sprintf("%s/%d.yaml", srv.URL, i), definition.DirectionRequest, requestInput()); err != nil {
			t.Fatalf("Transform() returned an unexpected error: %v", err)
		}
	}
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	before := fetches
	mu.Unlock()

	// A fourth reference, asked for repeatedly. It should be cached after the
	// first fetch, because the three stale entries no longer occupy the cap.
	fourth := srv.URL + "/fourth.yaml"
	for i := 0; i < 5; i++ {
		if _, err := mapper.Transform(context.Background(), fourth,
			definition.DirectionRequest, requestInput()); err != nil {
			t.Fatalf("Transform() returned an unexpected error: %v", err)
		}
	}

	mu.Lock()
	after := fetches
	mu.Unlock()
	if got := after - before; got != 1 {
		t.Errorf("a new reference was fetched %d times over 5 requests, want 1 -- "+
			"stale entries are holding the cap", got)
	}
}

// A failure on the response leg is not the caller's fault. The input there is
// the PROVIDER's answer, so a provider that changed shape, or a bug in the
// response half, used to return 400 and send the caller off to fix a request
// that was fine.
func TestTransformBlamesTheRightPartyForEachDirection(t *testing.T) {
	t.Parallel()

	// A mapping whose halves both fail at evaluation: $number over a value that
	// is not a number.
	failing := "request: |\n  $number(beckn.notANumber)\nresponse: |\n  $number(response.notANumber)\n"
	srv := newMappingServer(t, failing, nil)
	defer srv.Close()

	mapper := newTestMapper(t)
	ref := srv.URL + "/failing.yaml"

	_, err := mapper.Transform(context.Background(), ref, definition.DirectionRequest,
		map[string]any{"beckn": map[string]any{"notANumber": "abc"}})
	if err == nil {
		t.Fatal("expected the request half to fail")
	}
	var coded *model.CodedErr
	if !errors.As(err, &coded) || coded.HTTPStatus() != http.StatusBadRequest {
		t.Errorf("request leg gave %v, want a 400 -- the caller's payload is what the mapping could not read", err)
	}

	_, err = mapper.Transform(context.Background(), ref, definition.DirectionResponse,
		map[string]any{"response": map[string]any{"notANumber": "abc"}})
	if err == nil {
		t.Fatal("expected the response half to fail")
	}
	if !errors.As(err, &coded) || coded.HTTPStatus() != http.StatusBadGateway {
		t.Errorf("response leg gave %v, want a 502 -- the provider's answer is what failed, not the request", err)
	}
}

// --- concurrency ------------------------------------------------------------

// Every inbound request shares one mapper, so the cache is read and written
// concurrently, and jsonata.Expression.Evaluate mutates what it is called on.
// Both halves are exercised: they hold separate locks, so a request and a
// response leg of the same mapping do run at the same time. Run with -race.
func TestTransformIsSafeUnderConcurrentUse(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, bothDirections, nil)
	defer srv.Close()

	mapper := newTestMapper(t)
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			direction, input := definition.DirectionRequest, requestInput()
			if i%2 == 1 {
				direction, input = definition.DirectionResponse, responseInput()
			}
			_, err := mapper.Transform(context.Background(),
				fmt.Sprintf("%s/%d.yaml", srv.URL, i%3), direction, input)
			errs <- err
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Transform() failed: %v", err)
		}
	}
}
