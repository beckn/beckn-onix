package mausamgram_test

// mappings_test.go runs the shipped mapping files through the real mapper and
// the real provider step. It is the only test that proves the three pieces fit:
// a mapping is JSONata inside YAML fetched over HTTP, and nothing but running
// it establishes that what is published actually produces valid Beckn.
//
// An external test package on purpose -- it uses the plugins exactly as the
// adapter does, through their exported surface and nothing else.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/jsonmapper"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/mausamgram"
)

// mappingsDir is where the shipped mappings live, relative to this package.
const mappingsDir = "../../../../config/mappings/mausamgram"

// selectedResourceID is the resource the request selects, and therefore the one
// the answer quotes. It is the same string in both directions on purpose.
const selectedResourceID = "res:mausamgram:point-forecast"

// shippedMapping is the file this binding-action publishes: one file, both
// directions. The registry carries its full URL; the action segment of the name
// must match the action that registry entry declares -- a mismatch would apply a
// correct mapping to the wrong call, silently.
const shippedMapping = "weather-observation.select.yaml"

// selectRequest is the verbatim /select captured from the OAN network.
const selectRequest = `{
  "context": { "version": "2.0.0", "action": "select",
    "networkId": "da.gov.in/vistaar",
    "bapId": "seeker-network-vistaar.da.gov.in",
    "bapUri": "https://seeker-network-vistaar.da.gov.in/beckn",
    "bppId": "provider-network-vistaar.da.gov.in",
    "bppUri": "https://provider-network-vistaar.da.gov.in/beckn",
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

// providerResponse is Mausamgram's own shape, with the field names the old
// per-provider service read: fcstdayN carrying date, rain, tmin, tmax, rhmin,
// rhmax, wspd and a warning. Three days, not five, so the mapping is exercised
// against a provider that returned fewer than the maximum.
const providerResponse = `{
  "location": { "lat": 19.9975, "lon": 73.7898 },
  "fcstday1": { "date": "2026-08-26", "rain": 12.4, "tmin": 22.1, "tmax": 30.6,
                "rhmin": 55, "rhmax": 92, "wspd": 4.2,
                "weather_warning": "Heavy rainfall warning" },
  "fcstday2": { "date": "2026-08-27", "rain": 3.1, "tmin": 23.0, "tmax": 31.2,
                "rhmin": 50, "rhmax": 88, "wspd": 3.4,
                "cloud_message": "Partly cloudy" },
  "fcstday3": { "date": "2026-08-28", "tmin": 23.4, "tmax": 32.0 }
}`

// serveMappings publishes the shipped mapping files over HTTP, the way the
// registry's references point at them.
func serveMappings(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := os.ReadFile(filepath.Join(mappingsDir, filepath.Base(r.URL.Path)))
		if err != nil {
			t.Errorf("could not read the mapping %q: %v", r.URL.Path, err)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, string(body))
	}))
}

// stubRegistry answers with the call plan the live registry holds for this
// capability.
type stubRegistry struct{ plan *model.ProviderRecord }

func (s *stubRegistry) ProviderRecord(context.Context, string) (*model.ProviderRecord, error) {
	return s.plan, nil
}

// TestShippedMappingsServeARealSelect runs the published mappings end to end.
func TestShippedMappingsServeARealSelect(t *testing.T) {
	mappings := serveMappings(t)
	defer mappings.Close()

	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, providerResponse)
	}))
	defer upstream.Close()

	mapper, closeMapper, err := jsonmapper.New(context.Background(), &jsonmapper.Config{})
	if err != nil {
		t.Fatalf("failed to build the mapper: %v", err)
	}
	defer closeMapper()

	registry := &stubRegistry{plan: &model.ProviderRecord{
		BindingKey:     mausamgram.DefaultBindingKey,
		ParticipantID:  "mausamgram",
		CapabilityCode: "openagrinet:WeatherObservation",
		BaseURL:        upstream.URL,
		Actions: map[string]model.ActionPlan{
			"select": {Method: http.MethodGet, Path: "/get-daily",
				Mappings: mappings.URL + "/" + shippedMapping, TimeoutMs: 30000, RetryMax: 3},
		},
	}}

	step, closeStep, err := mausamgram.New(context.Background(), registry, mapper, &mausamgram.Config{})
	if err != nil {
		t.Fatalf("failed to build the step: %v", err)
	}
	defer closeStep()

	stepCtx := &model.StepContext{Context: t.Context(), Body: []byte(selectRequest)}
	if err := step.Run(stepCtx); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}

	// --- the request reached the provider correctly -------------------------
	// The request half is empty, so these parameters are
	// the point the step resolved, not something a mapping produced.
	for _, want := range []string{"lat=19.9975", "lon=73.7898"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("upstream query %q is missing %q", gotQuery, want)
		}
	}

	// --- the response mapping produced Beckn --------------------------------
	if len(stepCtx.ResponseBody) == 0 {
		t.Fatal("the step produced no answer")
	}
	var answer map[string]any
	if err := json.Unmarshal(stepCtx.ResponseBody, &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, stepCtx.ResponseBody)
	}

	beckncontext, _ := answer["context"].(map[string]any)
	if beckncontext["action"] != "on_select" {
		t.Errorf("action = %v, want on_select", beckncontext["action"])
	}
	// The transaction has to survive the round trip, or the caller cannot match
	// the answer to what it asked.
	if beckncontext["transactionId"] != "9f2c1a8e-4b70-4d31-9c55-6f2e0b1d7a44" {
		t.Errorf("transactionId = %v, want the one from the request", beckncontext["transactionId"])
	}
	if beckncontext["bppId"] != "provider-network-vistaar.da.gov.in" {
		t.Errorf("bppId = %v, want the one from the request", beckncontext["bppId"])
	}

	commitment := firstCommitment(t, answer)
	status, _ := commitment["status"].(map[string]any)
	descriptor, _ := status["descriptor"].(map[string]any)
	if descriptor["code"] != "QUOTED" {
		t.Errorf("status = %v, want QUOTED", descriptor["code"])
	}
	if commitment["offer"] == nil {
		t.Error("the quoted commitment carries no offer")
	}

	// One resource, carrying the id the request selected. The consumer asked for
	// a quote on one resource, so that is what is quoted -- the forecast days are
	// content of it, not resources of their own.
	//
	// Minting a resource per day would leave offer.resourceIds pointing at an id
	// that appears nowhere in the answer, because the offer is echoed from the
	// request. Keeping the id is what makes that reference stay true.
	resources, _ := commitment["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1 -- the one the request selected", len(resources))
	}

	only, _ := resources[0].(map[string]any)
	if only["id"] != selectedResourceID {
		t.Errorf("resource id = %v, want the requested %q", only["id"], selectedResourceID)
	}

	// The offer's references must resolve against the resources actually
	// returned. This is the assertion the previous shape could not satisfy.
	offer, _ := commitment["offer"].(map[string]any)
	referenced, _ := offer["resourceIds"].([]any)
	if len(referenced) != 1 || referenced[0] != selectedResourceID {
		t.Errorf("offer.resourceIds = %v, want exactly [%q]", referenced, selectedResourceID)
	}

	// --- the WeatherObservation schema pack, Direct mode ---------------------
	// openagrinet:WeatherObservation v0.1 requires all five of these when
	// informationMode is Direct. Two of them were missing before the pack was
	// read: generatedAt, and a validity for the resource as a whole.
	attributes, _ := only["resourceAttributes"].(map[string]any)
	for _, f := range []struct{ key, want string }{
		{"@type", "openagrinet:WeatherObservation"},
		{"informationMode", "Direct"},
		{"observationType", "Forecast"},
	} {
		if attributes[f.key] != f.want {
			t.Errorf("%s = %v, want %v", f.key, attributes[f.key], f.want)
		}
	}
	for _, required := range []string{"source", "location", "generatedAt", "validity", "observations"} {
		if attributes[required] == nil {
			t.Errorf("resourceAttributes carries no %q", required)
		}
	}

	// The point, the source and the observation type are the same for every day,
	// so they sit once at the top rather than being repeated per day.
	//
	// GeoJSON order, and the provider's own echo of the point: the mapping reads
	// response.location rather than anything the step resolved.
	location, _ := attributes["location"].(map[string]any)
	coordinates, _ := location["coordinates"].([]any)
	if len(coordinates) != 2 || coordinates[0] != 73.7898 || coordinates[1] != 19.9975 {
		t.Errorf("coordinates = %v, want [73.7898, 19.9975] in GeoJSON order", coordinates)
	}

	// The resource-level validity spans the whole forecast, first day to last.
	validity, _ := attributes["validity"].(map[string]any)
	if validity["startsAt"] != "2026-08-26" || validity["endsAt"] != "2026-08-28" {
		t.Errorf("validity = %v, want the span of the days the provider answered with", validity)
	}

	// --- the days ------------------------------------------------------------
	// observations is NOT a pack field. The pack carries one validity and one
	// flat parameters array per resource, so it cannot express a multi-day
	// forecast in the resource the request selected. Keeping the selected id
	// matters more, so the days go in an extra field -- which validates, because
	// the pack sets no additionalProperties, but is not governed by it.
	observations, _ := attributes["observations"].([]any)
	if len(observations) != 3 {
		t.Fatalf("got %d observations, want 3 -- one per day the provider answered with", len(observations))
	}

	first, _ := observations[0].(map[string]any)
	dayValidity, _ := first["validity"].(map[string]any)
	if dayValidity["startsAt"] != "2026-08-26" {
		t.Errorf("first observation starts at %v, want the provider's first forecast date", dayValidity["startsAt"])
	}

	parameters, _ := first["parameters"].([]any)
	if len(parameters) != 7 {
		t.Errorf("got %d parameters, want 7 for a fully-reported day with a warning", len(parameters))
	}
	assertParameter(t, parameters, "Rainfall", "Total", "mm", 12.4)
	assertParameter(t, parameters, "Temperature", "Minimum", "Cel", 22.1)
	assertParameter(t, parameters, "WindSpeed", "Average", "m/s", 4.2)

	// A warning is a parameter, not a field of its own: the pack has no advisory
	// property but does have an Alert parameter, and unit "1" is what it
	// prescribes for a value that has no unit.
	assertAlert(t, parameters, "Heavy rainfall warning")

	// --- a day the provider reported only partially --------------------------
	// Readings it did not take are absent, not present and empty: a consumer
	// must be able to tell "no rainfall recorded" from "zero rainfall". A day
	// with no warning carries no Alert parameter at all.
	third, _ := observations[2].(map[string]any)
	thirdParameters, _ := third["parameters"].([]any)
	if len(thirdParameters) != 2 {
		t.Errorf("got %d parameters for a partly-reported day, want only the 2 taken", len(thirdParameters))
	}
	for _, entry := range thirdParameters {
		if p, _ := entry.(map[string]any); p["parameter"] == "Alert" {
			t.Error("a day the provider gave no warning for must carry no Alert parameter")
		}
	}
}

// assertAlert finds the Alert parameter and checks its value and unit.
func assertAlert(t *testing.T, parameters []any, want string) {
	t.Helper()
	for _, entry := range parameters {
		p, _ := entry.(map[string]any)
		if p["parameter"] != "Alert" {
			continue
		}
		if p["value"] != want {
			t.Errorf("Alert value = %v, want %q", p["value"], want)
		}
		if p["unit"] != "1" {
			t.Errorf("Alert unit = %v, want \"1\" -- the pack's code for a unitless value", p["unit"])
		}
		return
	}
	t.Errorf("no Alert parameter; want one carrying %q", want)
}

// The shipped file's request half is deliberately empty: this provider takes its
// parameters in the query string. That has to produce nothing rather than an
// empty document, so the step builds the query itself.
func TestShippedMappingsProduceNoRequestDocument(t *testing.T) {
	mappings := serveMappings(t)
	defer mappings.Close()

	mapper, closeMapper, err := jsonmapper.New(context.Background(), &jsonmapper.Config{})
	if err != nil {
		t.Fatalf("failed to build the mapper: %v", err)
	}
	defer closeMapper()

	ref := mappings.URL + "/" + shippedMapping
	input := map[string]any{"beckn": map[string]any{"context": map[string]any{"action": "select"}}}

	got, err := mapper.Transform(context.Background(), ref, definition.DirectionRequest, input)
	if err != nil {
		t.Errorf("the empty request half must not be an error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("the request half produced %q, want nothing", got)
	}
}

func firstCommitment(t *testing.T, answer map[string]any) map[string]any {
	t.Helper()
	message, _ := answer["message"].(map[string]any)
	contract, _ := message["contract"].(map[string]any)
	commitments, _ := contract["commitments"].([]any)
	if len(commitments) == 0 {
		t.Fatalf("the answer carries no commitments: %v", answer)
	}
	commitment, _ := commitments[0].(map[string]any)
	return commitment
}

func assertParameter(t *testing.T, parameters []any, name, aggregation, unit string, value float64) {
	t.Helper()
	for _, raw := range parameters {
		parameter, _ := raw.(map[string]any)
		if parameter["parameter"] == name && parameter["aggregation"] == aggregation {
			if parameter["unit"] != unit {
				t.Errorf("%s/%s unit = %v, want %v", name, aggregation, parameter["unit"], unit)
			}
			if parameter["value"] != value {
				t.Errorf("%s/%s value = %v, want %v", name, aggregation, parameter["value"], value)
			}
			return
		}
	}
	t.Errorf("no %s/%s parameter in %v", name, aggregation, parameters)
}
