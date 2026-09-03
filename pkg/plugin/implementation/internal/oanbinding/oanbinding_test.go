package oanbinding

import (
	"errors"
	"strings"
	"testing"
)

// realSelectPayload is a verbatim /select request captured from the OAN network
// on 29 Aug 2026. It is the reason this package reads through contract and
// commitments rather than off message directly: the design notes showed the
// shallower message.offer.provider.id, and the wire does not.
const realSelectPayload = `{
  "context": { "version": "2.0.0", "action": "select",
    "networkId": "da.gov.in/vistaar",
    "bapId": "seeker-network-vistaar.da.gov.in",
    "bppId": "provider-network-vistaar.da.gov.in",
    "transactionId": "9f2c1a8e-4b70-4d31-9c55-6f2e0b1d7a44",
    "messageId": "7d41b9e0-52a6-4c18-8b73-1e9f0a4c6d22",
    "timestamp": "2026-08-26T06:12:01.330Z" },
  "message": { "contract": { "commitments": [{
    "status": { "descriptor": { "code": "DRAFT", "name": "Draft" } },
    "resources": [{
      "id": "res:mausamgram:point-forecast",
      "resourceAttributes": {
        "@context": "https://schemas.openagrinet.global/schema/WeatherObservation/v0.1/context.jsonld",
        "@type": "openagrinet:WeatherObservation",
        "subjectCategories": ["Weather"],
        "location": { "type": "Point", "coordinates": [73.7898, 19.9975] },
        "validity": { "startsAt": "2026-08-26", "endsAt": "2026-08-30" }
      }
    }],
    "offer": {
      "id": "offer:mausamgram:open-data",
      "resourceIds": ["res:mausamgram:point-forecast"],
      "provider": { "id": "mausamgram",
                    "descriptor": { "code": "IMD-NWP-01", "name": "IMD Mausamgram NWP" } }
    }
  }] } }
}`

func TestFromReadsARealSelectPayload(t *testing.T) {
	t.Parallel()

	got, err := From(BecknV2, []byte(realSelectPayload))
	if err != nil {
		t.Fatalf("From() returned an unexpected error: %v", err)
	}
	if got.ParticipantID != "mausamgram" {
		t.Errorf("participant = %q, want mausamgram", got.ParticipantID)
	}
	if got.CapabilityCode != "openagrinet:WeatherObservation" {
		t.Errorf("capability = %q, want openagrinet:WeatherObservation", got.CapabilityCode)
	}
	if want := "mausamgram|openagrinet:WeatherObservation"; got.Key() != want {
		t.Errorf("key = %q, want %q", got.Key(), want)
	}
}

// A payload that names no capability is the ordinary case for a request a
// provider step is not meant to serve, so it is reported as a sentinel a caller
// can recognise rather than as a fault.
func TestFromReportsAPayloadWithNoBinding(t *testing.T) {
	t.Parallel()

	testCases := []struct{ name, body string }{
		{"an empty object", `{}`},
		{"no message", `{"context":{"action":"select"}}`},
		{"no contract", `{"message":{}}`},
		{"no commitments", `{"message":{"contract":{}}}`},
		{"an empty commitments array", `{"message":{"contract":{"commitments":[]}}}`},
		{"a commitment naming no provider", `{"message":{"contract":{"commitments":[{"resources":[{"resourceAttributes":{"@type":"t"}}]}]}}}`},
		{"a commitment with no resources", `{"message":{"contract":{"commitments":[{"offer":{"provider":{"id":"p"}}}]}}}`},
		{"a resource with no type", `{"message":{"contract":{"commitments":[{"offer":{"provider":{"id":"p"}},"resources":[{"resourceAttributes":{}}]}]}}}`},
		{"an empty provider id", `{"message":{"contract":{"commitments":[{"offer":{"provider":{"id":""}},"resources":[{"resourceAttributes":{"@type":"t"}}]}]}}}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := From(BecknV2, []byte(tc.body)); !errors.Is(err, ErrNoBinding) {
				t.Errorf("expected ErrNoBinding, got %v", err)
			}
		})
	}
}

// Both levels are arrays, so a payload can carry several. One binding key
// describes one upstream call, so a request spanning more than one is refused
// rather than silently resolved to whichever came first.
func TestFromRefusesAnAmbiguousPayload(t *testing.T) {
	t.Parallel()

	testCases := []struct{ name, body, wants string }{
		{
			name: "two providers across commitments",
			body: `{"message":{"contract":{"commitments":[
				{"offer":{"provider":{"id":"one"}},"resources":[{"resourceAttributes":{"@type":"t"}}]},
				{"offer":{"provider":{"id":"two"}},"resources":[{"resourceAttributes":{"@type":"t"}}]}]}}}`,
			// Two providers means two commitments under the Beckn v2 paths, so
			// the commitment check refuses it first -- and its advice is the
			// more useful of the two. The provider count still guards a
			// deployment whose overridden path yields several within one.
			wants: "2 commitments",
		},
		{
			name: "two types within one commitment",
			body: `{"message":{"contract":{"commitments":[
				{"offer":{"provider":{"id":"one"}},"resources":[
					{"resourceAttributes":{"@type":"a"}},
					{"resourceAttributes":{"@type":"b"}}]}]}}}`,
			wants: "2 resource types",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := From(BecknV2, []byte(tc.body))
			if err == nil {
				t.Fatal("expected an ambiguous payload to be refused")
			}
			if errors.Is(err, ErrNoBinding) {
				t.Error("ambiguity is not absence: it must not report ErrNoBinding")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q should say %q", err, tc.wants)
			}
		})
	}
}

func TestFromReportsAnUnreadablePayload(t *testing.T) {
	t.Parallel()

	_, err := From(BecknV2, []byte(`{"message":`))
	if err == nil {
		t.Fatal("expected unreadable JSON to be reported")
	}
	if errors.Is(err, ErrNoBinding) {
		t.Error("a broken payload is not an absent binding")
	}
}

// --- where the binding key lives ---------------------------------------------
//
// The two halves of a binding key sit at a fixed place in a Beckn v2 payload.
// That is a NETWORK convention: every participant must agree, or two adapters
// disagree about what a binding key even is and requests silently fail to match.
//
// So it is a default, not a setting. BecknV2 is what every deployment uses. An
// override exists only so a spec change can be tracked without waiting for a
// release, and it has to be typed deliberately -- absent means correct.

func TestBecknV2IsTheDefault(t *testing.T) {
	t.Parallel()

	if BecknV2.ProviderID == "" || BecknV2.CapabilityCode == "" {
		t.Fatal("the default paths must be set")
	}
	got, err := From(BecknV2, []byte(realSelectPayload))
	if err != nil {
		t.Fatalf("From() returned an unexpected error: %v", err)
	}
	if got.ParticipantID != "mausamgram" || got.CapabilityCode != "openagrinet:WeatherObservation" {
		t.Errorf("binding = %+v, want the Beckn v2 convention's answer", got)
	}
}

// An override reads the halves from somewhere else entirely, which is what makes
// a spec change survivable without a build.
func TestFromReadsAnOverriddenPath(t *testing.T) {
	t.Parallel()

	body := `{"who":{"provider":"agmarknet"},"what":[{"type":"openagrinet:MandiPrice"}]}`
	got, err := From(Paths{
		ProviderID:     "who.provider",
		CapabilityCode: "what[].type",
	}, []byte(body))
	if err != nil {
		t.Fatalf("From() returned an unexpected error: %v", err)
	}
	if got.Key() != "agmarknet|openagrinet:MandiPrice" {
		t.Errorf("binding key = %q, want it read from the overridden paths", got.Key())
	}
}

// A path that matches nothing is a request this step is not meant to serve --
// the ordinary case, and the same answer the typed walk gave.
func TestFromReportsNoBindingWhenAPathMatchesNothing(t *testing.T) {
	t.Parallel()

	_, err := From(Paths{ProviderID: "nowhere.at.all", CapabilityCode: "what[].type"},
		[]byte(`{"what":[{"type":"x"}]}`))
	if !errors.Is(err, ErrNoBinding) {
		t.Errorf("expected ErrNoBinding, got %v", err)
	}
}

// Several distinct values stays a refusal whatever path found them: one request
// maps to one call, and guessing which would serve part of it silently.
func TestFromStillRefusesSeveralValuesUnderAnOverride(t *testing.T) {
	t.Parallel()

	_, err := From(Paths{ProviderID: "who[].provider", CapabilityCode: "what[].type"},
		[]byte(`{"who":[{"provider":"a"},{"provider":"b"}],"what":[{"type":"x"}]}`))
	if err == nil || errors.Is(err, ErrNoBinding) {
		t.Errorf("expected a refusal naming both providers, got %v", err)
	}
}

// The walk is deliberately small: dotted segments, and [] to flatten an array.
// No wildcards, no filters, no indices -- it is an escape hatch, not a query
// language, and every one of those would be a way to write something subtly
// wrong in config nobody reviews.
func TestPathWalk(t *testing.T) {
	t.Parallel()

	doc := map[string]any{
		"a": map[string]any{"b": "flat"},
		"list": []any{
			map[string]any{"v": "one"},
			map[string]any{"v": "two"},
		},
		"nested": []any{
			map[string]any{"inner": []any{map[string]any{"v": "deep"}}},
		},
		"number": 42,
	}

	testCases := []struct {
		name string
		path string
		want []string
	}{
		{"a flat field", "a.b", []string{"flat"}},
		{"through an array", "list[].v", []string{"one", "two"}},
		{"through two arrays", "nested[].inner[].v", []string{"deep"}},
		{"a path that is not there", "a.missing", nil},
		{"a value that is not a string", "number", nil},
		{"an array not marked", "list.v", nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := valuesAt(doc, tc.path)
			if len(got) != len(tc.want) {
				t.Fatalf("valuesAt(%q) = %v, want %v", tc.path, got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("valuesAt(%q)[%d] = %q, want %q", tc.path, i, got[i], want)
				}
			}
		})
	}
}

// An override that names no path at all would match nothing and make every
// request unservable, silently. Refused where it is configured instead.
func TestPathsValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		paths Paths
	}{
		{"no provider path", Paths{CapabilityCode: "a.b"}},
		{"no capability path", Paths{ProviderID: "a.b"}},
		{"a blank segment", Paths{ProviderID: "a..b", CapabilityCode: "a.b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := tc.paths.Validate(); err == nil {
				t.Error("expected an unusable path pair to be refused")
			}
		})
	}

	if err := BecknV2.Validate(); err != nil {
		t.Errorf("the default paths must validate: %v", err)
	}
}

// Several commitments naming the same provider and capability used to resolve
// to one binding key without complaint -- and then the mapping read
// commitments[0] and the rest were dropped, leaving the caller a confident,
// signed, spec-valid answer to part of what it asked. One request maps to one
// call, so it is refused.
func TestFromRefusesSeveralCommitments(t *testing.T) {
	t.Parallel()

	body := `{"message":{"contract":{"commitments":[
		{"offer":{"provider":{"id":"mausamgram"}},
		 "resources":[{"resourceAttributes":{"@type":"openagrinet:WeatherObservation"}}]},
		{"offer":{"provider":{"id":"mausamgram"}},
		 "resources":[{"resourceAttributes":{"@type":"openagrinet:WeatherObservation"}}]}
	]}}}`

	_, err := From(BecknV2, []byte(body))
	if err == nil {
		t.Fatal("expected two commitments to be refused rather than halved")
	}
	if errors.Is(err, ErrNoBinding) {
		t.Error("this is not an absent binding; it is one request asking for two calls")
	}
	if !strings.Contains(err.Error(), "2 commitments") {
		t.Errorf("error %q should say how many were sent", err)
	}

	// One commitment still resolves, obviously.
	single := `{"message":{"contract":{"commitments":[
		{"offer":{"provider":{"id":"mausamgram"}},
		 "resources":[{"resourceAttributes":{"@type":"openagrinet:WeatherObservation"}}]}
	]}}}`
	binding, err := From(BecknV2, []byte(single))
	if err != nil {
		t.Fatalf("one commitment must still resolve: %v", err)
	}
	if binding.Key() != "mausamgram|openagrinet:WeatherObservation" {
		t.Errorf("binding key = %q", binding.Key())
	}
}
