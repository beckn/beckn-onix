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

	got, err := From([]byte(realSelectPayload))
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

			if _, err := From([]byte(tc.body)); !errors.Is(err, ErrNoBinding) {
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
			wants: "2 providers",
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

			_, err := From([]byte(tc.body))
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

// Repetition is not ambiguity: several commitments naming the same provider and
// type describe one call, and must resolve rather than be refused.
func TestFromAcceptsRepetitionOfTheSameBinding(t *testing.T) {
	t.Parallel()

	body := `{"message":{"contract":{"commitments":[
		{"offer":{"provider":{"id":"p"}},"resources":[{"resourceAttributes":{"@type":"t"}}]},
		{"offer":{"provider":{"id":"p"}},"resources":[{"resourceAttributes":{"@type":"t"}}]}]}}}`

	got, err := From([]byte(body))
	if err != nil {
		t.Fatalf("From() returned an unexpected error: %v", err)
	}
	if got.Key() != "p|t" {
		t.Errorf("key = %q, want p|t", got.Key())
	}
}

func TestFromReportsAnUnreadablePayload(t *testing.T) {
	t.Parallel()

	_, err := From([]byte(`{"message":`))
	if err == nil {
		t.Fatal("expected unreadable JSON to be reported")
	}
	if errors.Is(err, ErrNoBinding) {
		t.Error("a broken payload is not an absent binding")
	}
}
