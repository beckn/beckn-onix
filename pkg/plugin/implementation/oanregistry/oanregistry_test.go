package oanregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	testParticipantID = "provider-a-001"
	testOSID          = "1-d0442000-a677-4cfc-bd8f-02696c6088b3"
	testPublicKey     = "MCowBQYDK2VwAyEA3fS8bYhWEfmM7Zjk9x0EhAmvQKp3fMHXqTiA5xL1Qmw="
)

// mockCache is a test double for definition.Cache that records what it was asked
// to do, so tests can assert the cache was (or was not) used.
type mockCache struct {
	getFunc func(ctx context.Context, key string) (string, error)

	getCalls int
	setCalls int
	setKey   string
	setVal   string
	setTTL   time.Duration
	setErr   error
}

func (m *mockCache) Get(ctx context.Context, key string) (string, error) {
	m.getCalls++
	if m.getFunc != nil {
		return m.getFunc(ctx, key)
	}
	return "", errors.New("cache miss")
}

func (m *mockCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	m.setCalls++
	m.setKey = key
	m.setVal = value
	m.setTTL = ttl
	return m.setErr
}

func (m *mockCache) Delete(ctx context.Context, key string) error { return nil }
func (m *mockCache) Clear(ctx context.Context) error              { return nil }

// recordJSON renders a registry search response containing the given records.
func recordJSON(t *testing.T, records ...participant) string {
	t.Helper()
	if records == nil {
		records = []participant{}
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("failed to marshal test records: %v", err)
	}
	return string(data)
}

// signingKey is a healthy signing key with an open validity window. Its value
// carries the registry's encoding label so every test that reaches
// toSubscription also exercises the prefix being stripped.
func signingKey() key {
	return key{
		OSID:       testOSID,
		KeyID:      "k1",
		Use:        useSign,
		Algorithm:  expectedAlgorithm,
		Value:      keyEncodingPrefix + testPublicKey,
		Status:     "active",
		ValidFrom:  time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
		ValidUntil: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
	}
}

// activeRecord is a healthy participant publishing one active signing key.
func activeRecord() participant {
	return participant{
		ParticipantID: testParticipantID,
		Type:          "node",
		Role:          "BPP",
		Status:        "active",
		BaseURL:       "https://providera.example.com/onix",
		Keys:          []key{signingKey()},
	}
}

// newTestClient builds a client pointed at srvURL, with retries effectively off
// and sub-millisecond backoff so tests stay fast.
func newTestClient(t *testing.T, srvURL string, cache definition.Cache, tweak ...func(*Config)) *Client {
	t.Helper()

	cfg := &Config{
		URL:          srvURL,
		Timeout:      DefaultTimeoutSeconds,
		RetryMax:     0,
		RetryWaitMin: time.Millisecond,
		RetryWaitMax: 2 * time.Millisecond,
	}
	for _, apply := range tweak {
		apply(cfg)
	}

	client, closer, err := New(context.Background(), cache, cfg)
	if err != nil {
		t.Fatalf("New() returned an unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = closer() })
	return client
}

func lookup(t *testing.T, c *Client) ([]model.Subscription, error) {
	t.Helper()
	return c.Lookup(context.Background(), &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: testParticipantID},
		KeyID:      testOSID,
	})
}

// --- configuration -------------------------------------------------------

func TestValidate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		config      *Config
		expectedErr string
	}{
		{
			name:        "should return error for nil config",
			config:      nil,
			expectedErr: "oan registry config cannot be nil",
		},
		{
			name:        "should return error for empty URL",
			config:      &Config{URL: ""},
			expectedErr: "oan registry URL cannot be empty",
		},
		{
			name:        "should succeed for valid config",
			config:      &Config{URL: "http://localhost:8081/api/v1"},
			expectedErr: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validate(tc.config)
			switch {
			case tc.expectedErr == "" && err != nil:
				t.Fatalf("expected no error, but got: %v", err)
			case tc.expectedErr != "" && err == nil:
				t.Fatalf("expected an error but got none")
			case tc.expectedErr != "" && err.Error() != tc.expectedErr:
				t.Errorf("expected error message %q, but got %q", tc.expectedErr, err.Error())
			}
		})
	}
}

// TestNewAlwaysBoundsTheTimeout guards the one deliberate difference from the
// sibling registry plugins. They apply the timeout only when it is configured,
// which leaves it infinite when it is not. Re-adding that guard here would look
// like harmless tidying, so it is asserted directly.
func TestNewAlwaysBoundsTheTimeout(t *testing.T) {
	t.Parallel()

	client, closer, err := New(context.Background(), nil, &Config{URL: "http://localhost:8081"})
	if err != nil {
		t.Fatalf("New() returned an unexpected error: %v", err)
	}
	defer func() { _ = closer() }()

	if got := client.client.HTTPClient.Timeout; got <= 0 {
		t.Fatalf("expected a bounded timeout when none is configured, got %v", got)
	}
}

// TestNewRejectsAnInvalidConfig: a bad URL must stop the adapter at startup
// rather than failing every lookup once traffic arrives.
func TestNewRejectsAnInvalidConfig(t *testing.T) {
	t.Parallel()

	for _, cfg := range []*Config{
		nil,
		{URL: ""},
		{URL: "registry:8081"},
	} {
		if _, _, err := New(context.Background(), nil, cfg); err == nil {
			t.Errorf("expected New to reject config %+v", cfg)
		}
	}
}

func TestNewBuildsSearchURL(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		config   *Config
		expected string
	}{
		{
			name:     "defaults to the Participant entity",
			config:   &Config{URL: "http://registry:8081/api/v1"},
			expected: "http://registry:8081/api/v1/Participant/search",
		},
		{
			name:     "honours a configured entity",
			config:   &Config{URL: "http://registry:8081/api/v1", Entity: "Subscriber"},
			expected: "http://registry:8081/api/v1/Subscriber/search",
		},
		{
			name:     "tolerates a trailing slash on the base URL",
			config:   &Config{URL: "http://registry:8081/api/v1/"},
			expected: "http://registry:8081/api/v1/Participant/search",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client, closer, err := New(context.Background(), nil, tc.config)
			if err != nil {
				t.Fatalf("New() returned an unexpected error: %v", err)
			}
			defer func() { _ = closer() }()

			if client.searchURL != tc.expected {
				t.Errorf("expected search URL %q, got %q", tc.expected, client.searchURL)
			}
		})
	}
}

// --- status mapping ------------------------------------------------------

// TestResolveStatus is the security regression test for this plugin.
//
// model.IsKeyStatusUsable is a deny-list, so a status it does not recognise
// counts as usable. Passing the registry's own "inactive" through unchanged
// would let a suspended participant's signature verify, which is why every case
// below asserts usability rather than just the mapped string.
func TestResolveStatus(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	rfc := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }

	// active builds a usable signing key, so each case below varies only the one
	// thing it is about.
	active := func(mutate ...func(*key)) key {
		k := key{OSID: testOSID, Use: useSign, Value: testPublicKey, Status: "active"}
		for _, apply := range mutate {
			apply(&k)
		}
		return k
	}

	testCases := []struct {
		name              string
		participantStatus string
		key               key
		expectedStatus    string
		expectedReason    string
		expectUsable      bool
	}{
		{
			name:              "active within window is usable",
			participantStatus: "active",
			key:               active(func(k *key) { k.ValidFrom, k.ValidUntil = rfc(-time.Hour), rfc(time.Hour) }),
			expectedStatus:    statusSubscribed,
			expectedReason:    outcomeFound,
			expectUsable:      true,
		},
		{
			name:              "active with no window bounds is usable",
			participantStatus: "active",
			key:               active(),
			expectedStatus:    statusSubscribed,
			expectedReason:    outcomeFound,
			expectUsable:      true,
		},
		{
			name:              "participant status is matched case insensitively",
			participantStatus: "ACTIVE",
			key:               active(),
			expectedStatus:    statusSubscribed,
			expectedReason:    outcomeFound,
			expectUsable:      true,
		},
		{
			name:              "key status is matched case insensitively",
			participantStatus: "active",
			key:               active(func(k *key) { k.Status = "ACTIVE" }),
			expectedStatus:    statusSubscribed,
			expectedReason:    outcomeFound,
			expectUsable:      true,
		},
		{
			name:              "the encoding label is not mistaken for key material",
			participantStatus: "active",
			key:               active(func(k *key) { k.Value = keyEncodingPrefix + testPublicKey }),
			expectedStatus:    statusSubscribed,
			expectedReason:    outcomeFound,
			expectUsable:      true,
		},
		{
			name:              "an inactive participant is denied",
			participantStatus: "inactive",
			key:               active(),
			expectedStatus:    statusUnsubscribed,
			expectedReason:    outcomeInactive,
			expectUsable:      false,
		},
		{
			name:              "an unrecognised participant status is denied",
			participantStatus: "approved",
			key:               active(),
			expectedStatus:    statusUnsubscribed,
			expectedReason:    outcomeInactive,
			expectUsable:      false,
		},
		{
			name:              "an empty participant status is denied",
			participantStatus: "",
			key:               active(),
			expectedStatus:    statusUnsubscribed,
			expectedReason:    outcomeInactive,
			expectUsable:      false,
		},
		{
			// The reason per-key status exists: the participant is trading normally,
			// one of its keys has been retired, and that key alone must stop verifying.
			name:              "a retired key under an active participant is denied",
			participantStatus: "active",
			key:               active(func(k *key) { k.Status = "inactive" }),
			expectedStatus:    statusUnsubscribed,
			expectedReason:    outcomeKeyInactive,
			expectUsable:      false,
		},
		{
			name:              "an unrecognised key status is denied",
			participantStatus: "active",
			key:               active(func(k *key) { k.Status = "rotating" }),
			expectedStatus:    statusUnsubscribed,
			expectedReason:    outcomeKeyInactive,
			expectUsable:      false,
		},
		{
			name:              "an empty key status is denied",
			participantStatus: "active",
			key:               active(func(k *key) { k.Status = "" }),
			expectedStatus:    statusUnsubscribed,
			expectedReason:    outcomeKeyInactive,
			expectUsable:      false,
		},
		{
			name:              "an active key with no material is denied",
			participantStatus: "active",
			key:               active(func(k *key) { k.Value = "" }),
			expectedStatus:    statusUnsubscribed,
			expectedReason:    outcomeNoKey,
			expectUsable:      false,
		},
		{
			// A value that is nothing but the encoding label carries no material.
			name:              "a key that is only an encoding label is denied",
			participantStatus: "active",
			key:               active(func(k *key) { k.Value = keyEncodingPrefix }),
			expectedStatus:    statusUnsubscribed,
			expectedReason:    outcomeNoKey,
			expectUsable:      false,
		},
		{
			// The validity window is not enforced: participation is controlled
			// through `status` alone, so an expired key still verifies until the
			// Network Operator deactivates it.
			name:              "a window that has not opened yet is NOT enforced",
			participantStatus: "active",
			key:               active(func(k *key) { k.ValidFrom = rfc(time.Hour) }),
			expectedStatus:    statusSubscribed,
			expectedReason:    outcomeFound,
			expectUsable:      true,
		},
		{
			name:              "a window that has closed is NOT enforced",
			participantStatus: "active",
			key:               active(func(k *key) { k.ValidUntil = rfc(-time.Hour) }),
			expectedStatus:    statusSubscribed,
			expectedReason:    outcomeFound,
			expectUsable:      true,
		},
		{
			name:              "an unparseable window bound is treated as unbounded",
			participantStatus: "active",
			key:               active(func(k *key) { k.ValidUntil = "not-a-timestamp" }),
			expectedStatus:    statusSubscribed,
			expectedReason:    outcomeFound,
			expectUsable:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, reason := resolveStatus(participant{Status: tc.participantStatus}, tc.key)
			if status != tc.expectedStatus {
				t.Errorf("expected status %q, got %q", tc.expectedStatus, status)
			}
			if reason != tc.expectedReason {
				t.Errorf("expected outcome %q, got %q", tc.expectedReason, reason)
			}
			if usable := model.IsKeyStatusUsable(status); usable != tc.expectUsable {
				t.Errorf("expected IsKeyStatusUsable to be %v for status %q, got %v", tc.expectUsable, status, usable)
			}
		})
	}
}

// TestToSubscriptionMapsOptionalFields covers the registry carrying, and not
// carrying, the fields it may or may not populate.
func TestToSubscriptionMapsOptionalFields(t *testing.T) {
	t.Parallel()

	t.Run("maps optional fields when present", func(t *testing.T) {
		t.Parallel()

		record := activeRecord()
		record.Keys = append(record.Keys, key{
			OSID:   "1-abcdef00-0000-0000-0000-000000000000",
			Use:    useEncr,
			Value:  keyEncodingPrefix + "encryption-key",
			Status: "active",
		})

		got := toSubscription(record, record.Keys[0], statusSubscribed)

		if got.EncrPublicKey != "encryption-key" {
			t.Errorf("expected encryption key to be mapped, got %q", got.EncrPublicKey)
		}
		if got.Type != "BPP" {
			t.Errorf("expected type to be mapped, got %q", got.Type)
		}
		if got.SigningPublicKey != testPublicKey {
			t.Errorf("expected the encoding label to be stripped, got %q", got.SigningPublicKey)
		}
		if got.ValidFrom.IsZero() || got.ValidUntil.IsZero() {
			t.Error("expected the validity window to be parsed")
		}
	})

	t.Run("leaves optional fields empty when absent", func(t *testing.T) {
		t.Parallel()

		k := key{OSID: testOSID, Use: useSign, Value: testPublicKey, Status: "active"}
		record := participant{
			ParticipantID: testParticipantID,
			Keys:          []key{k},
		}
		got := toSubscription(record, k, statusSubscribed)

		// A node publishing only a signing key yields no encryption key, rather
		// than falling back to the signing one.
		if got.EncrPublicKey != "" {
			t.Errorf("expected an empty encryption key, got %q", got.EncrPublicKey)
		}
		if got.Type != "" {
			t.Errorf("expected an empty type, got %q", got.Type)
		}
		if got.SubscriberID != testParticipantID || got.KeyID != testOSID {
			t.Errorf("expected identifiers to be mapped, got subscriber=%q key=%q", got.SubscriberID, got.KeyID)
		}
	})

	t.Run("ignores a retired encryption key", func(t *testing.T) {
		t.Parallel()

		record := activeRecord()
		record.Keys = append(record.Keys, key{
			OSID:   "1-abcdef00-0000-0000-0000-000000000000",
			Use:    useEncr,
			Value:  keyEncodingPrefix + "retired-encryption-key",
			Status: "inactive",
		})

		got := toSubscription(record, record.Keys[0], statusSubscribed)

		if got.EncrPublicKey != "" {
			t.Errorf("a retired encryption key must not be published, got %q", got.EncrPublicKey)
		}
	})
}

// TestClassify pins the outcome vocabulary. It is a pure function, and the
// value of the split -- a dead registry and a malformed body landing in
// different series -- is entirely lost if a branch silently stops matching.
func TestClassify(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		err      error
		expected string
	}{
		{name: "deadline exceeded", err: context.DeadlineExceeded, expected: outcomeTimeout},
		{name: "cancelled", err: context.Canceled, expected: outcomeTimeout},
		{name: "wrapped deadline", err: fmt.Errorf("sending: %w", context.DeadlineExceeded), expected: outcomeTimeout},
		{name: "registry status", err: fmt.Errorf("%w: 503", errRegistryStatus), expected: outcomeRegistryError},
		{name: "decode failure", err: fmt.Errorf("%w: bad json", errDecodeResponse), expected: outcomeDecodeError},
		{name: "anything else is transport", err: errors.New("connection refused"), expected: outcomeTransportError},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classify(tc.err); got != tc.expected {
				t.Errorf("expected outcome %q, got %q", tc.expected, got)
			}
		})
	}
}

// --- lookup --------------------------------------------------------------

func TestLookupResolvesAnActiveParticipant(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, recordJSON(t, activeRecord()))
	}))
	defer srv.Close()

	results, err := lookup(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 result, got %d", len(results))
	}
	if results[0].SigningPublicKey != testPublicKey {
		t.Errorf("expected the signing key to be returned, got %q", results[0].SigningPublicKey)
	}
	if !model.IsKeyStatusUsable(results[0].Status) {
		t.Errorf("expected an active participant to be usable, got status %q", results[0].Status)
	}
}

// TestLookupSendsTheExpectedRequest pins the wire contract: both filters, and
// no Authorization header. The registry's search endpoint is public, and a
// malformed bearer is rejected before its permit rule is evaluated -- so
// accidentally sending one would break every lookup.
func TestLookupSendsTheExpectedRequest(t *testing.T) {
	t.Parallel()

	var gotPath, gotMethod, gotAuth string
	var gotBody searchRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		fmt.Fprint(w, recordJSON(t, activeRecord()))
	}))
	defer srv.Close()

	if _, err := lookup(t, newTestClient(t, srv.URL, nil)); err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected a POST, got %s", gotMethod)
	}
	if gotPath != "/Participant/search" {
		t.Errorf("expected path /Participant/search, got %s", gotPath)
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
	if got := gotBody.Filters[fieldParticipantID].Eq; got != testParticipantID {
		t.Errorf("expected participant_id filter %q, got %q", testParticipantID, got)
	}
	// osid must NOT be filtered on: it is not an indexed field, so an
	// Elasticsearch-backed registry matches nothing and every lookup becomes a
	// not-found. The key identity is checked client-side instead.
	if _, present := gotBody.Filters["osid"]; present {
		t.Error("expected osid NOT to be sent as a filter; it is not an indexed field")
	}
	if len(gotBody.Filters) != 1 {
		t.Errorf("expected exactly 1 filter, got %d: %v", len(gotBody.Filters), gotBody.Filters)
	}
}

// TestLookupRejectsEmptyIdentifiers: an empty key id would match any record
// whose OSID is absent, so it is refused before the registry is called.
func TestLookupRejectsEmptyIdentifiers(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		subscriberID string
		keyID        string
	}{
		{name: "no subscriber id", subscriberID: "", keyID: testOSID},
		{name: "no key id", subscriberID: testParticipantID, keyID: ""},
		{name: "neither", subscriberID: "", keyID: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				fmt.Fprint(w, recordJSON(t, activeRecord()))
			}))
			defer srv.Close()

			client := newTestClient(t, srv.URL, nil)
			results, err := client.Lookup(context.Background(), &model.Subscription{
				Subscriber: model.Subscriber{SubscriberID: tc.subscriberID},
				KeyID:      tc.keyID,
			})
			if err != nil {
				t.Fatalf("Lookup() returned an unexpected error: %v", err)
			}
			if len(results) != 0 {
				t.Errorf("expected no results, got %d", len(results))
			}
			if requests.Load() != 0 {
				t.Error("expected the registry not to be called for an empty identifier")
			}
		})
	}
}

// TestLookupWarnsOnAlgorithmMismatch: a record declaring an unexpected algorithm
// still resolves. The header's algorithm is validated upstream, so this cannot
// admit a bad signature -- it is surfaced as a warning, not a refusal.
func TestLookupWarnsOnAlgorithmMismatch(t *testing.T) {
	t.Parallel()

	record := activeRecord()
	record.Keys[0].Algorithm = "rsa-2048"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, recordJSON(t, record))
	}))
	defer srv.Close()

	results, err := lookup(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}
	if len(results) != 1 || !model.IsKeyStatusUsable(results[0].Status) {
		t.Fatal("expected an algorithm mismatch to warn, not to refuse the key")
	}
}

// TestLookupOnEmptyResult covers the registry answering "no such record". That
// is a legitimate answer, not a failure: the caller turns an empty slice into
// its own not-found error.
func TestLookupOnEmptyResult(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	results, err := lookup(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("expected no error for an empty result, got: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

// TestLookupAcceptsEitherResponseEnvelope: the registry answers with a bare
// array on some search backends and a {"data":[...]} envelope on others. Which
// one a deployment gets depends on its configured search provider, so both have
// to decode.
func TestLookupAcceptsEitherResponseEnvelope(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		body          string
		expectResults int
	}{
		{name: "bare array", body: recordJSON(t, activeRecord()), expectResults: 1},
		{name: "bare empty array", body: `[]`, expectResults: 0},
		{
			name:          "data envelope",
			body:          fmt.Sprintf(`{"totalCount":1,"data":%s}`, recordJSON(t, activeRecord())),
			expectResults: 1,
		},
		{name: "empty data envelope", body: `{"totalCount":0,"data":[]}`, expectResults: 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			results, err := lookup(t, newTestClient(t, srv.URL, nil))
			if err != nil {
				t.Fatalf("Lookup() returned an unexpected error: %v", err)
			}
			if len(results) != tc.expectResults {
				t.Fatalf("expected %d results, got %d", tc.expectResults, len(results))
			}
			if tc.expectResults == 1 && results[0].SigningPublicKey != testPublicKey {
				t.Errorf("expected the signing key to be returned, got %q", results[0].SigningPublicKey)
			}
		})
	}
}

// TestLookupOnSuspendedParticipant is the end-to-end counterpart to
// TestResolveStatus: a suspended participant must come back as a refusal the
// caller can distinguish from "unknown", not as an empty result.
func TestLookupOnSuspendedParticipant(t *testing.T) {
	t.Parallel()

	record := activeRecord()
	record.Status = "inactive"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, recordJSON(t, record))
	}))
	defer srv.Close()

	results, err := lookup(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected the record to be returned so the reason is reportable, got %d results", len(results))
	}
	if model.IsKeyStatusUsable(results[0].Status) {
		t.Fatalf("a suspended participant must not be usable, got status %q", results[0].Status)
	}
}

// TestLookupRejectsAKeyIdMismatch is the client-side replacement for the osid
// filter. osid is not an indexed field, so it cannot be filtered on server-side;
// the identity check has to happen here or a caller could present a valid
// participant id with someone else's key id.
func TestLookupRejectsAKeyIdMismatch(t *testing.T) {
	t.Parallel()

	record := activeRecord()
	record.Keys[0].OSID = "1-99999999-0000-0000-0000-000000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, recordJSON(t, record))
	}))
	defer srv.Close()

	results, err := lookup(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected a key id mismatch to resolve to not-found, got %d results", len(results))
	}
}

// TestSearchDistinguishesMismatchFromNotFound: both give the caller an empty
// result, but they are different facts. If the deployed key identity model is
// ever wrong, every lookup takes the mismatch path -- a total outage that would
// be invisible if it shared a metric with routine misses.
func TestSearchDistinguishesMismatchFromNotFound(t *testing.T) {
	t.Parallel()

	otherKey := activeRecord()
	otherKey.Keys[0].OSID = "1-99999999-0000-0000-0000-000000000000"

	testCases := []struct {
		name     string
		body     string
		expected string
	}{
		{name: "no such participant", body: `[]`, expected: outcomeNotFound},
		{name: "participant exists, key id does not match", body: recordJSON(t, otherKey), expected: outcomeKeyIDMismatch},
		{name: "participant and key id both match", body: recordJSON(t, activeRecord()), expected: outcomeFound},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			client := newTestClient(t, srv.URL, nil)
			tracer := otel.Tracer("test")

			_, _, outcome, err := client.search(context.Background(), tracer, testParticipantID, testOSID)
			if err != nil {
				t.Fatalf("search() returned an unexpected error: %v", err)
			}
			if outcome != tc.expected {
				t.Errorf("expected outcome %q, got %q", tc.expected, outcome)
			}
		})
	}
}

// TestLookupSelectsTheRecordCarryingTheKey covers the case the osid filter was
// originally meant to guard: more than one record sharing a participant_id, e.g.
// a soft-deleted one alongside the live record.
func TestLookupSelectsTheRecordCarryingTheKey(t *testing.T) {
	t.Parallel()

	stale := activeRecord()
	stale.Keys[0].OSID = "1-00000000-0000-0000-0000-000000000000"
	stale.Keys[0].Value = "stale-key"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, recordJSON(t, stale, activeRecord()))
	}))
	defer srv.Close()

	results, err := lookup(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].SigningPublicKey != testPublicKey {
		t.Fatalf("expected the record matching the requested key id to be chosen, got %+v", results)
	}
}

// TestLookupOnDuplicateRecords covers a registry integrity fault. osid is
// unique, so this cannot happen against a healthy registry -- but returning
// traffic-stopping errors on it would be worse than carrying on with the first
// record and logging loudly.
func TestLookupOnDuplicateRecords(t *testing.T) {
	t.Parallel()

	first, second := activeRecord(), activeRecord()
	second.Keys[0].Value = "a-different-key"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, recordJSON(t, first, second))
	}))
	defer srv.Close()

	results, err := lookup(t, newTestClient(t, srv.URL, nil))
	if err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 result, got %d", len(results))
	}
	if results[0].SigningPublicKey != testPublicKey {
		t.Errorf("expected the first record to be used, got key %q", results[0].SigningPublicKey)
	}
}

// --- transport failures --------------------------------------------------

func TestLookupOnMalformedResponses(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		body string
	}{
		{name: "not JSON at all", body: "this is not json"},
		{name: "an object with no data field", body: `{"participant_id":"provider-a-001"}`},
		{name: "a truncated array", body: `[{"participant_id":`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			if _, err := lookup(t, newTestClient(t, srv.URL, nil)); err == nil {
				t.Fatal("expected an error for a malformed response, got none")
			}
		})
	}
}

// TestLookupRetryBehaviour pins which status codes are retried.
//
// A 4xx means the request itself was wrong, so retrying it just wastes the
// caller's budget. The exception is 429, which means "too fast, try later" --
// retryablehttp's default policy already draws exactly this line, which is why
// this plugin sets no custom retry policy.
func TestLookupRetryBehaviour(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		status           int
		retryMax         int
		expectedAttempts int32
	}{
		{name: "400 is not retried", status: http.StatusBadRequest, retryMax: 2, expectedAttempts: 1},
		{name: "404 is not retried", status: http.StatusNotFound, retryMax: 2, expectedAttempts: 1},
		{name: "429 is retried", status: http.StatusTooManyRequests, retryMax: 2, expectedAttempts: 3},
		{name: "503 is retried", status: http.StatusServiceUnavailable, retryMax: 2, expectedAttempts: 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			client := newTestClient(t, srv.URL, nil, func(c *Config) { c.RetryMax = tc.retryMax })
			if _, err := lookup(t, client); err == nil {
				t.Fatal("expected an error for a failing registry, got none")
			}
			if got := attempts.Load(); got != tc.expectedAttempts {
				t.Errorf("expected %d attempts, got %d", tc.expectedAttempts, got)
			}
		})
	}
}

// TestLookupClampsRetryAfter covers a hostile or misconfigured registry.
//
// retryablehttp's DefaultBackoff honours Retry-After on 429/503 and returns it
// without applying its own ceiling, so an hour-long header would park the
// request for an hour inside signature validation.
func TestLookupClampsRetryAfter(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, nil, func(c *Config) {
		c.RetryMax = 1
		c.RetryWaitMax = 50 * time.Millisecond
	})

	start := time.Now()
	if _, err := lookup(t, client); err == nil {
		t.Fatal("expected an error after retries were exhausted, got none")
	}

	// Generous bound: the point is that it is not honouring 3600s, not the
	// precise backoff.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Retry-After was not clamped: lookup took %v", elapsed)
	}
}

// TestLookupRejectsCacheEntryWithoutStatus: an empty Status is absent from
// IsKeyStatusUsable's deny-list, so a cache entry carrying one would be treated
// as verifiable. The cache is shared and outlives a deploy, so the construction
// invariant has to be re-checked on read.
func TestLookupRejectsCacheEntryWithoutStatus(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, recordJSON(t, activeRecord()))
	}))
	defer srv.Close()

	poisoned, err := json.Marshal([]model.Subscription{{
		Subscriber:       model.Subscriber{SubscriberID: testParticipantID},
		KeyID:            testOSID,
		SigningPublicKey: "attacker-supplied-key",
	}})
	if err != nil {
		t.Fatalf("failed to build the test cache entry: %v", err)
	}

	cache := &mockCache{
		getFunc: func(ctx context.Context, key string) (string, error) { return string(poisoned), nil },
	}
	client := newTestClient(t, srv.URL, cache, func(c *Config) { c.CacheTTL = 30 * time.Second })

	results, err := lookup(t, client)
	if err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}
	if requests.Load() != 1 {
		t.Error("expected the statusless cache entry to be discarded and the registry consulted")
	}
	if len(results) != 1 || results[0].SigningPublicKey != testPublicKey {
		t.Fatal("expected the registry's key to be returned, not the cached one")
	}
}

// TestLookupDoesNotHangOnAStalledRegistry is the dead-peer case: the registry
// accepts the connection and then never answers. Without a bounded client
// timeout this would hang the calling request, and with it the adapter.
func TestLookupDoesNotHangOnAStalledRegistry(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	client := newTestClient(t, srv.URL, nil, func(c *Config) { c.Timeout = 1 })

	done := make(chan error, 1)
	go func() {
		_, err := lookup(t, client)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error, got none")
		}
		// classify() must see this as a timeout, not fall through to
		// transport_error. It depends on errors.Is holding through
		// http.Client.Timeout -> *url.Error -> retryablehttp's wrapper, which is
		// exactly the sort of chain that regresses quietly on a dependency bump.
		if got := classify(err); got != outcomeTimeout {
			t.Errorf("expected a stalled registry to classify as %q, got %q (%v)", outcomeTimeout, got, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Lookup() did not return; the client timeout is not being applied")
	}
}

func TestLookupHonoursContextCancellation(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	// Timeout deliberately far longer than the assertion window: with the default
	// 2s the client timeout fires first and this test passes even if context
	// propagation is deleted, which makes it a green test protecting nothing.
	client := newTestClient(t, srv.URL, nil, func(c *Config) { c.Timeout = 30 })
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := client.Lookup(ctx, &model.Subscription{
			Subscriber: model.Subscriber{SubscriberID: testParticipantID},
			KeyID:      testOSID,
		})
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error after cancellation, got none")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected a context.Canceled error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Lookup() ignored context cancellation")
	}
}

func TestLookupOnUnreachableRegistry(t *testing.T) {
	t.Parallel()

	// A server that is closed immediately, so the port refuses connections.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	if _, err := lookup(t, newTestClient(t, url, nil)); err == nil {
		t.Fatal("expected an error for an unreachable registry, got none")
	}
}

// --- caching -------------------------------------------------------------

// TestLookupCachingDisabledByDefault matters because the TTL is exactly the
// window in which a suspended participant keeps verifying. Caching is therefore
// opt-in, and "off" must mean the cache is not touched at all.
func TestLookupCachingDisabledByDefault(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, recordJSON(t, activeRecord()))
	}))
	defer srv.Close()

	cache := &mockCache{}
	if _, err := lookup(t, newTestClient(t, srv.URL, cache)); err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}

	if cache.getCalls != 0 || cache.setCalls != 0 {
		t.Errorf("expected the cache to be untouched by default, got %d reads and %d writes", cache.getCalls, cache.setCalls)
	}
}

func TestLookupCachesUsableResults(t *testing.T) {
	t.Parallel()

	const ttl = 30 * time.Second
	var requests atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, recordJSON(t, activeRecord()))
	}))
	defer srv.Close()

	cache := &mockCache{}
	client := newTestClient(t, srv.URL, cache, func(c *Config) { c.CacheTTL = ttl })

	if _, err := lookup(t, client); err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}

	if cache.setCalls != 1 {
		t.Fatalf("expected exactly 1 cache write, got %d", cache.setCalls)
	}
	// The TTL must come from configuration, never from the record's own validity
	// window -- that window is typically a year, which would keep a suspended
	// participant verifying for a year.
	if cache.setTTL != ttl {
		t.Errorf("expected the configured TTL %v, got %v", ttl, cache.setTTL)
	}
	if expected := fmt.Sprintf("oan_lookup_%s_%s", testParticipantID, testOSID); cache.setKey != expected {
		t.Errorf("expected cache key %q, got %q", expected, cache.setKey)
	}

	// A second lookup should be served from the cache without another round trip.
	cached := cache.setVal
	cache.getFunc = func(ctx context.Context, key string) (string, error) { return cached, nil }

	if _, err := lookup(t, client); err != nil {
		t.Fatalf("Lookup() returned an unexpected error on the cached path: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("expected the second lookup to be served from cache, but the registry saw %d requests", got)
	}
}

// TestLookupDoesNotCacheUnusableResults: caching a refusal would delay a
// reinstatement, and caching a miss would extend an outage.
func TestLookupDoesNotCacheUnusableResults(t *testing.T) {
	t.Parallel()

	suspended := activeRecord()
	suspended.Status = "inactive"

	testCases := []struct {
		name string
		body string
	}{
		{name: "a suspended participant", body: recordJSON(t, suspended)},
		{name: "a participant that does not exist", body: "[]"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			cache := &mockCache{}
			client := newTestClient(t, srv.URL, cache, func(c *Config) { c.CacheTTL = 30 * time.Second })

			if _, err := lookup(t, client); err != nil {
				t.Fatalf("Lookup() returned an unexpected error: %v", err)
			}
			if cache.setCalls != 0 {
				t.Errorf("expected nothing to be cached, got %d writes", cache.setCalls)
			}
		})
	}
}

// TestLookupSurvivesCacheFailures: the cache is a performance aid, so neither an
// unreadable entry nor a failing write may break verification.
func TestLookupSurvivesCacheFailures(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, recordJSON(t, activeRecord()))
	}))
	defer srv.Close()

	cache := &mockCache{
		getFunc: func(ctx context.Context, key string) (string, error) { return "{not-json", nil },
		setErr:  errors.New("cache is down"),
	}
	client := newTestClient(t, srv.URL, cache, func(c *Config) { c.CacheTTL = 30 * time.Second })

	results, err := lookup(t, client)
	if err != nil {
		t.Fatalf("expected cache failures to be survivable, got: %v", err)
	}
	if len(results) != 1 || results[0].SigningPublicKey != testPublicKey {
		t.Error("expected the lookup to fall back to the registry and return the key")
	}
}

// TestEmitMetricsSuccessPartition pins which outcomes count against
// onix_plugin_errors_total.
//
// The partition is an allow-list on purpose (see successOutcomes): an unlisted
// outcome must count as a failure, because the alternative -- a new success-like
// outcome silently falling through as success -- makes the error rate quietly
// untrue, and nothing else would catch it.
//
// This test must NOT call t.Parallel(). otel.SetMeterProvider is global and
// telemetry.GetMetrics caches instruments against the provider pointer, so
// running alongside the parallel tests would mix their measurements into this
// reader. Go never runs a non-parallel test concurrently with a parallel one, so
// the sequential phase gives this exclusive use of the global provider -- but
// that safety is invisible, hence this comment.
func TestEmitMetricsSuccessPartition(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name         string
		outcome      string
		expectErrors bool
	}{
		{name: "found is a success", outcome: outcomeFound, expectErrors: false},
		{name: "cache hit is a success", outcome: outcomeCacheHit, expectErrors: false},
		{name: "inactive counts as a failure", outcome: outcomeInactive, expectErrors: true},
		{name: "key id mismatch counts as a failure", outcome: outcomeKeyIDMismatch, expectErrors: true},
		{name: "an unlisted outcome counts as a failure", outcome: "some_future_outcome", expectErrors: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := otel.GetMeterProvider()
			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(mp)
			t.Cleanup(func() {
				otel.SetMeterProvider(previous)
				_ = mp.Shutdown(ctx)
			})

			(&Client{}).emitMetrics(ctx, time.Now(), operationLookup, tc.outcome)

			var rm metricdata.ResourceMetrics
			if err := reader.Collect(ctx, &rm); err != nil {
				t.Fatalf("failed to collect metrics: %v", err)
			}

			var sawDuration, sawErrors bool
			for _, scope := range rm.ScopeMetrics {
				for _, m := range scope.Metrics {
					switch m.Name {
					case "onix_plugin_execution_duration_seconds":
						sawDuration = true
						assertOutcomeAttribute(t, m, tc.outcome)
					case "onix_plugin_errors_total":
						sawErrors = true
						assertOutcomeAttribute(t, m, tc.outcome)
					}
				}
			}

			if !sawDuration {
				t.Error("expected the duration histogram to be recorded for every outcome")
			}
			if sawErrors != tc.expectErrors {
				t.Errorf("expected errors-counter recorded=%v for outcome %q, got %v", tc.expectErrors, tc.outcome, sawErrors)
			}
		})
	}
}

// assertOutcomeAttribute checks the four attributes every measurement carries.
func assertOutcomeAttribute(t *testing.T, m metricdata.Metrics, outcome string) {
	t.Helper()

	want := map[string]string{
		string(telemetry.AttrPluginID):   pluginID,
		string(telemetry.AttrPluginType): pluginType,
		string(telemetry.AttrOperation):  operationLookup,
		string(telemetry.AttrErrorType):  outcome,
	}

	var attrSets []attribute.Set
	switch data := m.Data.(type) {
	case metricdata.Histogram[float64]:
		for _, dp := range data.DataPoints {
			attrSets = append(attrSets, dp.Attributes)
		}
	case metricdata.Sum[int64]:
		for _, dp := range data.DataPoints {
			attrSets = append(attrSets, dp.Attributes)
		}
	default:
		t.Fatalf("unexpected metric data type for %s: %T", m.Name, m.Data)
	}

	if len(attrSets) == 0 {
		t.Fatalf("expected at least one data point for %s", m.Name)
	}
	for key, expected := range want {
		value, ok := attrSets[0].Value(attribute.Key(key))
		if !ok {
			t.Errorf("%s: missing attribute %q", m.Name, key)
			continue
		}
		if value.AsString() != expected {
			t.Errorf("%s: attribute %q = %q, want %q", m.Name, key, value.AsString(), expected)
		}
	}
}

// TestLookupAgainstCapturedRegistryResponse runs the plugin against a verbatim
// response captured from the real OAN registry on 31 Aug 2026, reformatted for
// readability with field order and values untouched.
//
// It pins the deployed shape: the data envelope, one flat level with the keys
// array beside participantId rather than under a wrapper, the camelCase field
// names, the "base64:" encoding label, the osid the registry injects into every
// nested object, and the "active" status vocabulary at both levels. The capture
// it replaces described a record wrapped in a "node" object, and this is the
// test that said so.
func TestLookupAgainstCapturedRegistryResponse(t *testing.T) {
	t.Parallel()

	const (
		capturedParticipantID   = "provider.oan.local"
		capturedKeyOSID         = "1-d1a4a2b7-7bf5-42f5-bfc2-2c77119c4d64"
		capturedParticipantOSID = "1-19087a97-f886-4fe4-bf14-3875437dc6f8"
		capturedKey             = "w1wDdr/xnO2yQYxdR/88enTkg0B//vVeIkXOfreClUQ="
		capturedURL             = "https://provider.oan.local/beckn"
	)

	const captured = `{
    "totalCount": 1,
    "data": [
        {
            "osUpdatedAt": "2026-08-31T07:36:33.407Z",
            "role": "BPP",
            "osUpdatedBy": "89bf9fcb-c6f7-4f08-80f9-18f47ce7667d",
            "osid": "1-19087a97-f886-4fe4-bf14-3875437dc6f8",
            "type": "node",
            "osOwner": [
                "89bf9fcb-c6f7-4f08-80f9-18f47ce7667d"
            ],
            "keys": [
                {
                    "osUpdatedAt": "2026-08-31T07:36:33.407Z",
                    "osUpdatedBy": "89bf9fcb-c6f7-4f08-80f9-18f47ce7667d",
                    "use": "sign",
                    "keyId": "k1",
                    "osid": "1-d1a4a2b7-7bf5-42f5-bfc2-2c77119c4d64",
                    "validFrom": "2026-01-01T00:00:00Z",
                    "osCreatedAt": "2026-08-31T07:36:33.407Z",
                    "osCreatedBy": "89bf9fcb-c6f7-4f08-80f9-18f47ce7667d",
                    "validUntil": "2030-01-01T00:00:00Z",
                    "alg": "ed25519",
                    "key": "base64:w1wDdr/xnO2yQYxdR/88enTkg0B//vVeIkXOfreClUQ=",
                    "status": "active"
                }
            ],
            "participantId": "provider.oan.local",
            "baseUrl": "https://provider.oan.local/beckn",
            "osCreatedAt": "2026-08-31T07:36:33.407Z",
            "name": "OAN provider layer adapter",
            "osCreatedBy": "89bf9fcb-c6f7-4f08-80f9-18f47ce7667d",
            "status": "active"
        }
    ]
}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, captured)
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, nil)
	resolve := func(keyID string) ([]model.Subscription, error) {
		return client.Lookup(context.Background(), &model.Subscription{
			Subscriber: model.Subscriber{SubscriberID: capturedParticipantID},
			KeyID:      keyID,
		})
	}

	results, err := resolve(capturedKeyOSID)
	if err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 result, got %d", len(results))
	}

	got := results[0]
	if got.SigningPublicKey != capturedKey {
		t.Errorf("signing key = %q, want %q (the encoding label must be stripped)", got.SigningPublicKey, capturedKey)
	}
	if !model.IsKeyStatusUsable(got.Status) {
		t.Errorf("an active participant with an active key must be usable, got status %q", got.Status)
	}
	if got.SubscriberID != capturedParticipantID {
		t.Errorf("subscriber id = %q, want %q", got.SubscriberID, capturedParticipantID)
	}
	if got.KeyID != capturedKeyOSID {
		t.Errorf("key id = %q, want %q", got.KeyID, capturedKeyOSID)
	}
	if got.URL != capturedURL {
		t.Errorf("endpoint url = %q, want the captured baseUrl %q", got.URL, capturedURL)
	}
	if got.Type != "BPP" {
		t.Errorf("type = %q, want %q", got.Type, "BPP")
	}
	if got.EncrPublicKey != "" {
		t.Errorf("this record publishes no encryption key, got %q", got.EncrPublicKey)
	}
	if got.ValidFrom.IsZero() || got.ValidUntil.IsZero() {
		t.Error("expected the validity window to be parsed from the key's validFrom/validUntil")
	}

	// The record carries two osids -- the participant's and the key's -- and only
	// the key's identifies a signing key. Matching the participant's would
	// resolve the wrong thing, and would keep resolving it as soon as a second
	// key were published.
	for _, tc := range []struct{ name, keyID string }{
		{"participant osid", capturedParticipantOSID},
		{"an unrelated osid", "1-00000000-0000-0000-0000-000000000000"},
	} {
		mismatched, err := resolve(tc.keyID)
		if err != nil {
			t.Fatalf("Lookup() with the %s returned an unexpected error: %v", tc.name, err)
		}
		if len(mismatched) != 0 {
			t.Errorf("expected the %s not to resolve a signing key, got %d results", tc.name, len(mismatched))
		}
	}
}

// TestLookupAgainstCurrentRegistryResponse runs the plugin against a verbatim
// response captured from an OAN registry on 2 Sep 2026, after the Participant
// schema dropped three things from a published key.
//
// It pins the shape a registry writes TODAY, and every difference from the
// capture above is deliberate:
//
//	role       one of consumer, provider and network. The Beckn acronyms are
//	           gone; a role now says what a party does.
//	keyId      absent. Nothing could look one up: the registry assigns an
//	           osid on write, and that is what a sender names in the
//	           Authorization header, so the friendly id was decoration.
//	use        absent. alg carries the purpose -- ed25519 signs -- and the
//	           plugin already treats a missing use as "may sign".
//	key        bare base64, no "base64:" label. The label is still tolerated
//	           by the test above, because a row written before this change
//	           keeps it forever: the registry is append-only.
func TestLookupAgainstCurrentRegistryResponse(t *testing.T) {
	t.Parallel()

	const (
		capturedParticipantID = "provider.oan.dev"
		capturedKeyOSID       = "1-d5b6c5ee-206c-4529-bf9d-803138ff067a"
		capturedKey           = "Hcmx3AEVSeHT+1J3ggqhzlbTTtTYP0tQ2eUfotR5lUI="
		capturedURL           = "https://provider.oan.dev/beckn"
	)

	const captured = `{
    "totalCount": 1,
    "data": [
        {
            "osUpdatedAt": "2026-09-02T07:25:29.977Z",
            "role": "provider",
            "osUpdatedBy": "1e52cfea-50a1-4a64-814e-0d44aaa38c29",
            "osid": "1-686a5071-b300-4899-94e2-7f95155ca41d",
            "type": "node",
            "keys": [
                {
                    "osUpdatedAt": "2026-09-02T07:25:29.977Z",
                    "osCreatedAt": "2026-09-02T07:25:29.977Z",
                    "osUpdatedBy": "1e52cfea-50a1-4a64-814e-0d44aaa38c29",
                    "osCreatedBy": "1e52cfea-50a1-4a64-814e-0d44aaa38c29",
                    "validUntil": "2030-01-01T00:00:00Z",
                    "osid": "1-d5b6c5ee-206c-4529-bf9d-803138ff067a",
                    "validFrom": "2026-01-01T00:00:00Z",
                    "alg": "ed25519",
                    "key": "Hcmx3AEVSeHT+1J3ggqhzlbTTtTYP0tQ2eUfotR5lUI=",
                    "status": "active"
                }
            ],
            "osOwner": [
                "1e52cfea-50a1-4a64-814e-0d44aaa38c29"
            ],
            "participantId": "provider.oan.dev",
            "baseUrl": "https://provider.oan.dev/beckn",
            "osCreatedAt": "2026-09-02T07:25:29.977Z",
            "name": "OAN provider layer adapter",
            "osCreatedBy": "1e52cfea-50a1-4a64-814e-0d44aaa38c29",
            "status": "active"
        }
    ]
}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, captured)
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, nil)
	results, err := client.Lookup(context.Background(), &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: capturedParticipantID},
		KeyID:      capturedKeyOSID,
	})
	if err != nil {
		t.Fatalf("Lookup() returned an unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 result, got %d", len(results))
	}

	got := results[0]
	// The point of the whole test: a key with no encoding label arrives intact.
	// Trimming a prefix that is not there must not disturb the value, because
	// what reaches the verifier goes straight to a base64 decoder.
	if got.SigningPublicKey != capturedKey {
		t.Errorf("signing key = %q, want the bare base64 %q", got.SigningPublicKey, capturedKey)
	}
	// A key with no "use" still resolves. Were that treated as "unknown, so
	// refuse", every key this registry now writes would be unusable.
	if !model.IsKeyStatusUsable(got.Status) {
		t.Errorf("an active participant with an active key must be usable, got status %q", got.Status)
	}
	if got.KeyID != capturedKeyOSID {
		t.Errorf("key id = %q, want the key's osid %q", got.KeyID, capturedKeyOSID)
	}
	if got.SubscriberID != capturedParticipantID {
		t.Errorf("subscriber id = %q, want %q", got.SubscriberID, capturedParticipantID)
	}
	if got.URL != capturedURL {
		t.Errorf("endpoint url = %q, want the captured baseUrl %q", got.URL, capturedURL)
	}
	if got.Type != "provider" {
		t.Errorf("role = %q, want %q -- not a Beckn acronym", got.Type, "provider")
	}
	if got.ValidFrom.IsZero() || got.ValidUntil.IsZero() {
		t.Error("expected the validity window to be parsed from the key's validFrom/validUntil")
	}
}

// --- cache write and metrics edge cases -----------------------------------

// TestCacheResultSkipsWhenDisabled: cacheTTL of 0 means the cache is not
// touched at all, rather than written with a zero TTL whose meaning would
// depend on the cache implementation.
func TestCacheResultSkipsWhenDisabled(t *testing.T) {
	t.Parallel()

	cache := &mockCache{}
	c := &Client{cache: cache, cacheTTL: 0}
	c.cacheResult(context.Background(), "some-key", []model.Subscription{{Status: statusSubscribed}})

	if cache.setCalls != 0 {
		t.Errorf("expected no cache write when disabled, got %d", cache.setCalls)
	}
}

// TestCacheResultSkipsWithoutACache covers the cache plugin being absent
// entirely, which is legal -- the adapter may run without one.
func TestCacheResultSkipsWithoutACache(t *testing.T) {
	t.Parallel()

	// Nil cache with a positive TTL: must not panic.
	c := &Client{cache: nil, cacheTTL: 30 * time.Second}
	c.cacheResult(context.Background(), "some-key", []model.Subscription{{Status: statusSubscribed}})
}

// TestCacheResultSurvivesAFailingWrite: the cache is a performance aid, so a
// write failure is logged and swallowed rather than propagated.
func TestCacheResultSurvivesAFailingWrite(t *testing.T) {
	t.Parallel()

	cache := &mockCache{setErr: errors.New("cache is down")}
	c := &Client{cache: cache, cacheTTL: 30 * time.Second}
	c.cacheResult(context.Background(), "some-key", []model.Subscription{{Status: statusSubscribed}})

	if cache.setCalls != 1 {
		t.Errorf("expected the write to be attempted once, got %d", cache.setCalls)
	}
}

// TestCachedSkipsWhenDisabled: with caching off the cache must not even be
// read, so a stale entry from a previous run cannot be served.
func TestCachedSkipsWhenDisabled(t *testing.T) {
	t.Parallel()

	cache := &mockCache{
		getFunc: func(ctx context.Context, key string) (string, error) {
			t.Error("cache was read despite being disabled")
			return "", nil
		},
	}
	c := &Client{cache: cache, cacheTTL: 0}

	if _, ok := c.cached(context.Background(), otel.Tracer("test"), "some-key"); ok {
		t.Error("expected no cache hit when caching is disabled")
	}
}

// TestParseTimeRejectsMalformedValues: a bad timestamp yields "absent" rather
// than an error, since these values are informational and must not fail a
// lookup.
func TestParseTimeRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "not-a-timestamp", "2026-08-19", "19/08/2026"} {
		if _, ok := parseTime(value); ok {
			t.Errorf("expected %q to be rejected as a timestamp", value)
		}
	}
	if _, ok := parseTime("2026-08-19T00:00:00Z"); !ok {
		t.Error("expected a valid RFC3339 timestamp to parse")
	}
}

// TestValidateRejectsAMalformedURL covers url.Parse itself failing, which it
// only does for genuinely broken input such as a control character.
func TestValidateRejectsAMalformedURL(t *testing.T) {
	t.Parallel()

	if err := validate(&Config{URL: "http://registry:8081/\x7f"}); err == nil {
		t.Error("expected a malformed URL to be rejected")
	}
	for _, u := range []string{"registry:8081", "/api/v1", "registry.example.com"} {
		if err := validate(&Config{URL: u}); err == nil {
			t.Errorf("expected %q to be rejected for missing scheme or host", u)
		}
	}
}
