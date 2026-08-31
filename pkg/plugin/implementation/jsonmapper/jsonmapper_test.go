package jsonmapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// twoActionMapping is the published form: one file, every action it serves.
// Request files are keyed by the action they translate; response files by the
// action they produce, which is why on_select rather than select appears there.
const twoActionMapping = `actions:
  select: |
    { "lat": _local.lat, "txn": beckn.context.transactionId }
  confirm: |
    { "booking": beckn.context.messageId }
`

const responseMapping = `actions:
  on_select: |
    { "lat": _local.lat, "txn": beckn.context.transactionId, "rain": response.fcstday1.rain }
`

func requestInput() map[string]any {
	return map[string]any{
		"beckn":  map[string]any{"context": map[string]any{"transactionId": "txn-123", "messageId": "msg-1"}},
		"_local": map[string]any{"lat": 19.9975, "lon": 73.7898},
	}
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

// ref builds a mapping reference. The filename carries no meaning -- the file's
// own keys say which actions it serves.
func ref(base string) string { return base + "/mappings/anything.yaml" }

// --- transformation --------------------------------------------------------

func TestTransformRunsTheMappingForTheRequestedAction(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, twoActionMapping, nil)
	defer srv.Close()

	got, err := newTestMapper(t).Transform(context.Background(), ref(srv.URL), "select", requestInput())
	if err != nil {
		t.Fatalf("Transform() returned an unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("failed to decode the result: %v", err)
	}
	if result["lat"] != 19.9975 {
		t.Errorf("lat = %v, want 19.9975 -- _local was not reachable", result["lat"])
	}
	if result["txn"] != "txn-123" {
		t.Errorf("txn = %v, want txn-123 -- beckn was not reachable", result["txn"])
	}
}

// One file, several actions, each reached by name. This is what the format
// exists for: adding an action is a new key, not a new file.
func TestTransformPicksTheRightActionFromOneFile(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, twoActionMapping, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	selected, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput())
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	confirmed, err := mapper.Transform(context.Background(), ref(srv.URL), "confirm", requestInput())
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if !strings.Contains(string(selected), `"lat"`) {
		t.Errorf("select produced %s, want the select mapping's output", selected)
	}
	if !strings.Contains(string(confirmed), `"booking"`) {
		t.Errorf("confirm produced %s, want the confirm mapping's output", confirmed)
	}
}

// The response leg is keyed by the action it produces, so a caller asks for
// on_select rather than select.
func TestTransformExposesTheResponseAlongsideTheRequest(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, responseMapping, nil)
	defer srv.Close()

	input := requestInput()
	input["response"] = map[string]any{"fcstday1": map[string]any{"rain": 12.4}}

	got, err := newTestMapper(t).Transform(context.Background(), ref(srv.URL), "on_select", input)
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
	}{{"lat", 19.9975}, {"txn", "txn-123"}, {"rain", 12.4}} {
		if result[field.key] != field.want {
			t.Errorf("%s = %v, want %v", field.key, result[field.key], field.want)
		}
	}
}

// --- declared but empty -----------------------------------------------------

// The ordinary case for a provider taking query parameters: the action is
// declared so the file still says what the capability serves, but there is no
// document to build.
func TestTransformReportsADeclaredButEmptyAction(t *testing.T) {
	t.Parallel()

	mapping := `actions:
  select: ""
  confirm: |
    { "booking": beckn.context.messageId }
`
	srv := newMappingServer(t, mapping, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	_, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput())
	if !errors.Is(err, definition.ErrNoTransform) {
		t.Errorf("expected ErrNoTransform, got %v", err)
	}

	// Its neighbours are unaffected: one action needing no transform says
	// nothing about the rest of the file.
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), "confirm", requestInput()); err != nil {
		t.Errorf("a sibling action must still be served: %v", err)
	}
}

// Declared-but-empty and absent are different facts and must not collapse: the
// first says "I serve this, build it yourself", the second says "I do not serve
// this at all". Confusing them would send an empty request where the answer
// should have been a refusal.
func TestTransformSeparatesAnEmptyActionFromAnAbsentOne(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, "actions:\n  select: \"\"\n", nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	if _, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput()); !errors.Is(err, definition.ErrNoTransform) {
		t.Errorf("declared-but-empty should report ErrNoTransform, got %v", err)
	}
	err := func() error {
		_, err := mapper.Transform(context.Background(), ref(srv.URL), "confirm", requestInput())
		return err
	}()
	if errors.Is(err, definition.ErrNoTransform) {
		t.Error("an absent action must not report ErrNoTransform -- it is not served at all")
	}
	if err == nil {
		t.Error("expected an absent action to be refused")
	}
}

// --- an action the file does not serve --------------------------------------

// A capability that publishes no mapping for an action does not serve it. The
// refusal has to be clear, because the alternative -- running whichever mapping
// happened to be there -- succeeds quietly and produces nonsense.
func TestTransformRefusesAnActionTheFileDoesNotServe(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, twoActionMapping, nil)
	defer srv.Close()

	_, err := newTestMapper(t).Transform(context.Background(), ref(srv.URL), "init", requestInput())
	if err == nil {
		t.Fatal("expected an unserved action to be refused")
	}
	if !strings.Contains(err.Error(), "init") {
		t.Errorf("error %q should name the action that was asked for", err)
	}
	// Naming what it does serve turns a deploy mistake into a one-line fix.
	if !strings.Contains(err.Error(), "select") {
		t.Errorf("error %q should say which actions the mapping does serve", err)
	}
}

func TestTransformRefusesAnEmptyAction(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, twoActionMapping, nil)
	defer srv.Close()

	if _, err := newTestMapper(t).Transform(context.Background(), ref(srv.URL), "", requestInput()); err == nil {
		t.Fatal("expected an empty action to be refused")
	}
}

// The filename says nothing. Naming a file after one action while it serves
// several would be worse than naming it nothing at all.
func TestTransformIgnoresTheFilename(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, twoActionMapping, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	for _, name := range []string{"/anything.yaml", "/confirm.yaml", "/x/y/z"} {
		if _, err := mapper.Transform(context.Background(), srv.URL+name, "select", requestInput()); err != nil {
			t.Errorf("Transform(%q) returned an unexpected error: %v", name, err)
		}
	}
}

// --- reference validation ---------------------------------------------------

func TestTransformRefusesAnUnusableReference(t *testing.T) {
	t.Parallel()

	testCases := []struct{ name, ref string }{
		{"empty", ""},
		{"a bare path with no scheme", "/mappings/select.yaml"},
		{"a file url", "file:///etc/passwd"},
		{"a scheme that is not http", "ftp://example.com/select.yaml"},
		{"no host", "http:///select.yaml"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := newTestMapper(t).Transform(context.Background(), tc.ref, "select", requestInput()); err == nil {
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
		{name: "malformed yaml", status: http.StatusOK, body: "actions: [unclosed"},
		{name: "no actions key", status: http.StatusOK, body: "other: value\n"},
		{name: "an empty actions map", status: http.StatusOK, body: "actions: {}\n"},
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

			if _, err := newTestMapper(t).Transform(context.Background(), ref(srv.URL), "select", requestInput()); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// One unusable action must not take the rest of the file down with it: a typo
// in confirm is no reason for select to stop being served.
func TestTransformIsolatesABrokenAction(t *testing.T) {
	t.Parallel()

	mapping := `actions:
  select: |
    { "lat": _local.lat }
  confirm: |
    {{{
  init: ""
`
	srv := newMappingServer(t, mapping, nil)
	defer srv.Close()
	mapper := newTestMapper(t)

	if _, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput()); err != nil {
		t.Errorf("a healthy action must still be served: %v", err)
	}
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), "confirm", requestInput()); err == nil {
		t.Error("expected an uncompilable action to be refused")
	}
	// An action declared with no mapping is a statement, not a fault: the caller
	// builds that request itself. It is reported as its own sentinel so a caller
	// that does not handle it fails loudly rather than sending nothing.
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), "init", requestInput()); !errors.Is(err, definition.ErrNoTransform) {
		t.Errorf("expected ErrNoTransform for a declared-but-empty action, got %v", err)
	}
}

// A mapping is fetched into memory and compiled, so an unbounded one is an
// unbounded allocation driven by whoever can write the registry record.
func TestTransformEnforcesASizeCap(t *testing.T) {
	t.Parallel()

	oversized := "actions:\n  select: |\n    " + strings.Repeat("x", 2048) + "\n"
	srv := newMappingServer(t, oversized, nil)
	defer srv.Close()

	mapper := newTestMapper(t, func(c *Config) { c.MaxMappingBytes = 512 })
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput()); err == nil {
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
		_, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput())
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
	srv := newMappingServer(t, twoActionMapping, &fetches)
	defer srv.Close()

	mapper := newTestMapper(t)
	for i := 0; i < 3; i++ {
		if _, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput()); err != nil {
			t.Fatalf("Transform() returned an unexpected error: %v", err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Errorf("fetched %d times, want 1 -- the mapping is being refetched per request", got)
	}
}

// One fetch serves every action in the file. This is the whole reason a file
// holds several: a transaction walking select then confirm pays one round trip,
// not one per action.
func TestTransformFetchesOnceForEveryActionInAFile(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	srv := newMappingServer(t, twoActionMapping, &fetches)
	defer srv.Close()

	mapper := newTestMapper(t)
	for _, action := range []string{"select", "confirm", "select"} {
		if _, err := mapper.Transform(context.Background(), ref(srv.URL), action, requestInput()); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Errorf("fetched %d times, want 1 -- a second action refetched the file", got)
	}
}

// Two references are two mappings even when they compile to the same thing.
func TestTransformCachesPerReference(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	srv := newMappingServer(t, twoActionMapping, &fetches)
	defer srv.Close()

	mapper := newTestMapper(t)
	for _, r := range []string{srv.URL + "/a.yaml", srv.URL + "/b.yaml"} {
		if _, err := mapper.Transform(context.Background(), r, "select", requestInput()); err != nil {
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
		if _, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput()); err == nil {
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
	srv := newMappingServer(t, twoActionMapping, &fetches)
	defer srv.Close()

	mapper := newTestMapper(t, func(c *Config) { c.CacheTTL = 20 * time.Millisecond })
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput()); err != nil {
		t.Fatalf("Transform() returned an unexpected error: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := mapper.Transform(context.Background(), ref(srv.URL), "select", requestInput()); err != nil {
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

	srv := newMappingServer(t, twoActionMapping, nil)
	defer srv.Close()

	mapper := newTestMapper(t, func(c *Config) { c.MaxCacheEntries = 2 })
	for i := 0; i < 5; i++ {
		if _, err := mapper.Transform(context.Background(),
			fmt.Sprintf("%s/%d.yaml", srv.URL, i), "select", requestInput()); err != nil {
			t.Fatalf("Transform() returned an unexpected error: %v", err)
		}
	}
	if got := mapper.cachedCount(); got > 2 {
		t.Errorf("cache holds %d entries, want at most 2", got)
	}
}

// --- concurrency ------------------------------------------------------------

// Every inbound request shares one mapper, so the cache is read and written
// concurrently, and jsonata.Expression.Evaluate mutates what it is called on.
// Run with -race.
func TestTransformIsSafeUnderConcurrentUse(t *testing.T) {
	t.Parallel()

	srv := newMappingServer(t, twoActionMapping, nil)
	defer srv.Close()

	mapper := newTestMapper(t)
	actions := []string{"select", "confirm"}
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			_, err := mapper.Transform(context.Background(),
				fmt.Sprintf("%s/%d.yaml", srv.URL, i%3), actions[i%2], requestInput())
			errs <- err
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Transform() failed: %v", err)
		}
	}
}
