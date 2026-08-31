package oanregistry

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

const (
	testBindingKey     = "mausamgram|openagrinet:WeatherObservation"
	testCapabilityCode = "openagrinet:WeatherObservation"
	testProviderID     = "mausamgram"
	testBaseURL        = "https://mausamgram.imd.gov.in/nwpapi"
	testMappings       = "https://mappings.example.com/mausamgram/weather-observation.select.yaml"
)

// envelopeJSON renders a registry search response in the data-envelope form.
func envelopeJSON[T any](t *testing.T, records ...T) string {
	t.Helper()
	if records == nil {
		records = []T{}
	}
	data, err := json.Marshal(struct {
		Data []T `json:"data"`
	}{Data: records})
	if err != nil {
		t.Fatalf("failed to marshal test records: %v", err)
	}
	return string(data)
}

// arrayJSON renders the same records as a bare array, the shape some search
// backends return instead of an envelope.
func arrayJSON[T any](t *testing.T, records ...T) string {
	t.Helper()
	if records == nil {
		records = []T{}
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("failed to marshal test records: %v", err)
	}
	return string(data)
}

// upstreamRecord is an active participant publishing a callable upstream. It is
// the provider half of a Participant record: no node, and so no keys.
func upstreamRecord() participant {
	return participant{
		ParticipantID: testProviderID,
		Type:          "upstream",
		Status:        "active",
		BaseURL:       testBaseURL,
	}
}

// bindingRecord is a healthy ProviderSchema row for testBindingKey.
func bindingRecord() providerBinding {
	return providerBinding{
		BindingKey:     testBindingKey,
		ParticipantID:  testProviderID,
		CapabilityCode: testCapabilityCode,
		Actions: []actionPlan{
			{Action: "select", Method: "GET", Path: "/get-daily", Mappings: testMappings,
				TimeoutMs: 30000, RetryMax: 3, Status: "active"},
		},
		Status: "active",
	}
}

// bodyForPath picks the response body for an entity search path.
func bodyForPath(t *testing.T, path, bindings, participants string) string {
	t.Helper()
	switch {
	case strings.Contains(path, "/"+DefaultProviderEntity+"/"):
		return bindings
	case strings.Contains(path, "/"+DefaultEntity+"/"):
		return participants
	default:
		t.Errorf("unexpected request path %q", path)
		return ""
	}
}

// newRegistryServer serves both entities from one server, routing on the path
// the client builds for each.
func newRegistryServer(t *testing.T, bindings, participants string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, bodyForPath(t, r.URL.Path, bindings, participants))
	}))
}

func resolvePlan(t *testing.T, c *Client) (*model.ProviderRecord, error) {
	t.Helper()
	return c.ProviderRecord(context.Background(), testBindingKey)
}

// --- happy path ------------------------------------------------------------

func TestProviderRecordResolvesACallPlan(t *testing.T) {
	t.Parallel()

	srv := newRegistryServer(t, envelopeJSON(t, bindingRecord()), envelopeJSON(t, upstreamRecord()))
	defer srv.Close()

	got, err := resolvePlan(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("ProviderRecord() returned an unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected a call plan, got nil")
	}

	for _, field := range []struct{ name, got, want string }{
		{"binding key", got.BindingKey, testBindingKey},
		{"participant id", got.ParticipantID, testProviderID},
		{"capability code", got.CapabilityCode, testCapabilityCode},
		{"base url", got.BaseURL, testBaseURL},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}

	call, served := got.Actions["select"]
	if !served {
		t.Fatalf("no call plan for select, got actions %v", got.Actions)
	}
	if call.Mappings != testMappings {
		t.Errorf("mappings = %q, want %q", call.Mappings, testMappings)
	}
	if call.Method != "GET" || call.Path != "/get-daily" {
		t.Errorf("select call = %s %s, want GET /get-daily", call.Method, call.Path)
	}
	if call.TimeoutMs != 30000 || call.RetryMax != 3 {
		t.Errorf("select budget = timeout %d retry %d, want 30000 and 3", call.TimeoutMs, call.RetryMax)
	}
}

// One capability, several actions, each with its own endpoint. This is what the
// per-action plan exists for: a confirm posting somewhere a select does not.
func TestProviderRecordResolvesAnEndpointPerAction(t *testing.T) {
	t.Parallel()

	binding := bindingRecord()
	binding.Actions = append(binding.Actions,
		actionPlan{Action: "confirm", Method: "POST", Path: "/book", Mappings: testMappings,
			TimeoutMs: 60000, RetryMax: 1, Status: "active"})

	srv := newRegistryServer(t, envelopeJSON(t, binding), envelopeJSON(t, upstreamRecord()))
	defer srv.Close()

	got, err := resolvePlan(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("ProviderRecord() returned an unexpected error: %v", err)
	}

	for _, want := range []struct {
		action, method, path string
	}{
		{"select", "GET", "/get-daily"},
		{"confirm", "POST", "/book"},
	} {
		call, served := got.Actions[want.action]
		if !served {
			t.Errorf("no call plan for %s", want.action)
			continue
		}
		if call.Method != want.method || call.Path != want.path {
			t.Errorf("%s call = %s %s, want %s %s", want.action, call.Method, call.Path, want.method, want.path)
		}
	}
}

// The registry may omit the call budget. Zero means "the caller applies its own
// default", and must not be mistaken for "no timeout and no retries".
func TestProviderRecordLeavesAnAbsentBudgetAtZero(t *testing.T) {
	t.Parallel()

	binding := bindingRecord()
	binding.Actions = []actionPlan{{Action: "select", Method: "GET", Path: "/get-daily",
		Mappings: testMappings, Status: "active"}}

	srv := newRegistryServer(t, envelopeJSON(t, binding), envelopeJSON(t, upstreamRecord()))
	defer srv.Close()

	got, err := resolvePlan(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("ProviderRecord() returned an unexpected error: %v", err)
	}
	call := got.Actions["select"]
	if call.TimeoutMs != 0 || call.RetryMax != 0 {
		t.Errorf("expected an absent budget to stay zero, got timeout=%d retry=%d", call.TimeoutMs, call.RetryMax)
	}
}

// The participant read is the one the binding names, not one parsed out of the
// binding key -- the registry owns that relationship, not the key format.
func TestProviderRecordReadsTheParticipantNamedByTheBinding(t *testing.T) {
	t.Parallel()

	var askedFor string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/"+DefaultEntity+"/") {
			var req searchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("failed to decode participant search: %v", err)
			}
			askedFor = req.Filters[fieldParticipantID].Eq
			fmt.Fprint(w, envelopeJSON(t, upstreamRecord()))
			return
		}
		fmt.Fprint(w, envelopeJSON(t, bindingRecord()))
	}))
	defer srv.Close()

	if _, err := resolvePlan(t, newTestClient(t, srv.URL, nil)); err != nil {
		t.Fatalf("ProviderRecord() returned an unexpected error: %v", err)
	}
	if askedFor != testProviderID {
		t.Errorf("participant searched for = %q, want %q", askedFor, testProviderID)
	}
}

// --- refusals --------------------------------------------------------------

// Every refusal means the same thing to a caller -- this capability cannot be
// served -- so all report ErrProviderRecordNotFound rather than an error a
// caller would have to string-match.
func TestProviderRecordRefusals(t *testing.T) {
	t.Parallel()

	inactiveBinding := bindingRecord()
	inactiveBinding.Status = "inactive"

	unknownStatusBinding := bindingRecord()
	unknownStatusBinding.Status = "draft"

	emptyStatusBinding := bindingRecord()
	emptyStatusBinding.Status = ""

	noParticipantBinding := bindingRecord()
	noParticipantBinding.ParticipantID = ""

	noActionsBinding := bindingRecord()
	noActionsBinding.Actions = nil

	inactiveActionBinding := bindingRecord()
	inactiveActionBinding.Actions = []actionPlan{{Action: "select", Method: "GET",
		Path: "/get-daily", Mappings: testMappings, Status: "inactive"}}

	unnamedActionBinding := bindingRecord()
	unnamedActionBinding.Actions = []actionPlan{{Method: "GET", Path: "/get-daily", Status: "active"}}

	inactiveUpstream := upstreamRecord()
	inactiveUpstream.Status = "inactive"

	emptyStatusUpstream := upstreamRecord()
	emptyStatusUpstream.Status = ""

	noBaseURL := upstreamRecord()
	noBaseURL.BaseURL = ""

	testCases := []struct {
		name         string
		bindings     string
		participants string
	}{
		{"no binding for the key", envelopeJSON[providerBinding](t), envelopeJSON(t, upstreamRecord())},
		{"an inactive binding", envelopeJSON(t, inactiveBinding), envelopeJSON(t, upstreamRecord())},
		{"a binding with an unrecognised status", envelopeJSON(t, unknownStatusBinding), envelopeJSON(t, upstreamRecord())},
		{"a binding with an empty status", envelopeJSON(t, emptyStatusBinding), envelopeJSON(t, upstreamRecord())},
		{"a binding naming no participant", envelopeJSON(t, noParticipantBinding), envelopeJSON(t, upstreamRecord())},
		{"a binding serving no action", envelopeJSON(t, noActionsBinding), envelopeJSON(t, upstreamRecord())},
		{"a binding whose only action is unnamed", envelopeJSON(t, unnamedActionBinding), envelopeJSON(t, upstreamRecord())},
		{"a binding whose only action is retired", envelopeJSON(t, inactiveActionBinding), envelopeJSON(t, upstreamRecord())},
		{"no participant owning the binding", envelopeJSON(t, bindingRecord()), envelopeJSON[participant](t)},
		{"an inactive participant", envelopeJSON(t, bindingRecord()), envelopeJSON(t, inactiveUpstream)},
		{"a participant with an empty status", envelopeJSON(t, bindingRecord()), envelopeJSON(t, emptyStatusUpstream)},
		{"a participant with no upstream url", envelopeJSON(t, bindingRecord()), envelopeJSON(t, noBaseURL)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newRegistryServer(t, tc.bindings, tc.participants)
			defer srv.Close()

			got, err := resolvePlan(t, newTestClient(t, srv.URL, nil))
			if !errors.Is(err, definition.ErrProviderRecordNotFound) {
				t.Errorf("expected ErrProviderRecordNotFound, got %v", err)
			}
			if got != nil {
				t.Errorf("expected no call plan alongside a refusal, got %+v", got)
			}
		})
	}
}

// An empty key cannot match anything, so it is refused without troubling the
// registry -- one fewer round trip on what is a caller bug.
func TestProviderRecordRefusesAnEmptyKeyWithoutCallingTheRegistry(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL, nil).ProviderRecord(context.Background(), "")
	if !errors.Is(err, definition.ErrProviderRecordNotFound) {
		t.Errorf("expected ErrProviderRecordNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("expected no call plan, got %+v", got)
	}
	if requests.Load() != 0 {
		t.Errorf("expected the registry not to be called, got %d request(s)", requests.Load())
	}
}

// --- failures that are not refusals ----------------------------------------

// A registry that could not be consulted is not a registry that answered "no".
// Collapsing the two would report an outage as a routine miss.
func TestProviderRecordDistinguishesFailureFromRefusal(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		body   string
		status int
	}{
		{"a registry error status", "", http.StatusInternalServerError},
		{"a not-found status", "", http.StatusNotFound},
		{"an undecodable body", `{"data":`, http.StatusOK},
		{"a body that is neither array nor envelope", `"not-a-record-set"`, http.StatusOK},
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

			got, err := resolvePlan(t, newTestClient(t, srv.URL, nil))
			if err == nil {
				t.Fatal("expected an error")
			}
			if errors.Is(err, definition.ErrProviderRecordNotFound) {
				t.Error("a registry that could not be consulted must not report not-found")
			}
			if got != nil {
				t.Errorf("expected no call plan, got %+v", got)
			}
		})
	}
}

// The binding resolves but the participant read fails: still a failure, not a
// refusal. The capability may well be fine.
func TestProviderRecordReportsAFailingParticipantRead(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/"+DefaultEntity+"/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, envelopeJSON(t, bindingRecord()))
	}))
	defer srv.Close()

	_, err := resolvePlan(t, newTestClient(t, srv.URL, nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, definition.ErrProviderRecordNotFound) {
		t.Error("a failing participant read must not report not-found")
	}
}

// --- both response envelopes ------------------------------------------------

func TestProviderRecordAcceptsEitherEnvelope(t *testing.T) {
	t.Parallel()

	testCases := []struct{ name, bindings, participants string }{
		{"data envelope", envelopeJSON(t, bindingRecord()), envelopeJSON(t, upstreamRecord())},
		{"bare array", arrayJSON(t, bindingRecord()), arrayJSON(t, upstreamRecord())},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newRegistryServer(t, tc.bindings, tc.participants)
			defer srv.Close()

			if _, err := resolvePlan(t, newTestClient(t, srv.URL, nil)); err != nil {
				t.Errorf("ProviderRecord() returned an unexpected error: %v", err)
			}
		})
	}
}

// --- caching ----------------------------------------------------------------

// storingCache round-trips what it is given, so a test can prove a second
// resolve is served without touching the registry. mockCache cannot: it is a
// spy whose Get always misses, which is right for asserting what was written
// but cannot demonstrate a hit.
type storingCache struct {
	mu       sync.Mutex
	entries  map[string]string
	getCalls int
	setCalls int
	setKey   string
}

func newStoringCache() *storingCache {
	return &storingCache{entries: make(map[string]string)}
}

func (c *storingCache) Get(ctx context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++
	value, found := c.entries[key]
	if !found {
		return "", errors.New("cache miss")
	}
	return value, nil
}

func (c *storingCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCalls++
	c.setKey = key
	c.entries[key] = value
	return nil
}

func (c *storingCache) Delete(ctx context.Context, key string) error { return nil }
func (c *storingCache) Clear(ctx context.Context) error              { return nil }

// countingRegistry serves both entities and counts every request, so a test can
// tell a cache hit from a second round trip.
func countingRegistry(t *testing.T, requests *atomic.Int32, bindings, participants string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, bodyForPath(t, r.URL.Path, bindings, participants))
	}))
}

func TestProviderRecordCachesAResolvedPlan(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	srv := countingRegistry(t, &requests, envelopeJSON(t, bindingRecord()), envelopeJSON(t, upstreamRecord()))
	defer srv.Close()

	client := newTestClient(t, srv.URL, newStoringCache(), func(c *Config) { c.CacheTTL = 30 * time.Second })

	if _, err := resolvePlan(t, client); err != nil {
		t.Fatalf("first ProviderRecord() returned an unexpected error: %v", err)
	}
	afterFirst := requests.Load()
	if afterFirst == 0 {
		t.Fatal("expected the first resolve to consult the registry")
	}

	if _, err := resolvePlan(t, client); err != nil {
		t.Fatalf("second ProviderRecord() returned an unexpected error: %v", err)
	}
	if requests.Load() != afterFirst {
		t.Errorf("expected the second resolve to be served from cache, registry was called %d more time(s)", requests.Load()-afterFirst)
	}
}

// A refusal must never be cached: it would keep a capability dark for the whole
// TTL after the operator reinstates it.
func TestProviderRecordDoesNotCacheARefusal(t *testing.T) {
	t.Parallel()

	cache := &mockCache{}
	srv := newRegistryServer(t, envelopeJSON[providerBinding](t), envelopeJSON(t, upstreamRecord()))
	defer srv.Close()

	client := newTestClient(t, srv.URL, cache, func(c *Config) { c.CacheTTL = 30 * time.Second })
	if _, err := resolvePlan(t, client); !errors.Is(err, definition.ErrProviderRecordNotFound) {
		t.Fatalf("expected ErrProviderRecordNotFound, got %v", err)
	}
	if cache.setCalls != 0 {
		t.Errorf("expected a refusal not to be cached, got %d write(s)", cache.setCalls)
	}
}

func TestProviderRecordSkipsTheCacheWhenDisabled(t *testing.T) {
	t.Parallel()

	cache := &mockCache{}
	srv := newRegistryServer(t, envelopeJSON(t, bindingRecord()), envelopeJSON(t, upstreamRecord()))
	defer srv.Close()

	// No CacheTTL is set: caching is opt-in, because the TTL is exactly how long
	// a withdrawn capability keeps being called.
	if _, err := resolvePlan(t, newTestClient(t, srv.URL, cache)); err != nil {
		t.Fatalf("ProviderRecord() returned an unexpected error: %v", err)
	}
	if cache.getCalls != 0 || cache.setCalls != 0 {
		t.Errorf("expected the cache to be untouched, got %d read(s) and %d write(s)", cache.getCalls, cache.setCalls)
	}
}

func TestProviderRecordDiscardsAnUnreadableCacheEntry(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	srv := countingRegistry(t, &requests, envelopeJSON(t, bindingRecord()), envelopeJSON(t, upstreamRecord()))
	defer srv.Close()

	cache := &mockCache{
		getFunc: func(ctx context.Context, key string) (string, error) { return "not-json", nil },
	}
	client := newTestClient(t, srv.URL, cache, func(c *Config) { c.CacheTTL = 30 * time.Second })

	got, err := resolvePlan(t, client)
	if err != nil {
		t.Fatalf("ProviderRecord() returned an unexpected error: %v", err)
	}
	if got.BaseURL != testBaseURL {
		t.Errorf("expected the registry's plan to be used, got base url %q", got.BaseURL)
	}
	if requests.Load() == 0 {
		t.Error("expected the unreadable entry to be discarded and the registry consulted")
	}
}

// Two capabilities of one provider must not share a cache entry, so the binding
// key has to appear in the key.
func TestProviderRecordCachesPerBindingKey(t *testing.T) {
	t.Parallel()

	cache := &mockCache{}
	srv := newRegistryServer(t, envelopeJSON(t, bindingRecord()), envelopeJSON(t, upstreamRecord()))
	defer srv.Close()

	client := newTestClient(t, srv.URL, cache, func(c *Config) { c.CacheTTL = 30 * time.Second })
	if _, err := resolvePlan(t, client); err != nil {
		t.Fatalf("ProviderRecord() returned an unexpected error: %v", err)
	}
	if !strings.Contains(cache.setKey, testBindingKey) {
		t.Errorf("cache key %q does not identify the binding", cache.setKey)
	}
}

// A cached plan must not collide with a cached signing key: different subjects,
// different lifetimes, one shared cache.
func TestProviderRecordCacheKeyIsDistinctFromTheKeyLookupCacheKey(t *testing.T) {
	t.Parallel()

	cache := &mockCache{}
	srv := newRegistryServer(t, envelopeJSON(t, bindingRecord()), envelopeJSON(t, upstreamRecord()))
	defer srv.Close()

	client := newTestClient(t, srv.URL, cache, func(c *Config) { c.CacheTTL = 30 * time.Second })
	if _, err := resolvePlan(t, client); err != nil {
		t.Fatalf("ProviderRecord() returned an unexpected error: %v", err)
	}
	if strings.HasPrefix(cache.setKey, "oan_lookup_") {
		t.Errorf("provider plan cache key %q shares the signing-key namespace", cache.setKey)
	}
}
