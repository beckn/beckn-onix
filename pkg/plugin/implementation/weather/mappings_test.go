package weather_test

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
	"slices"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/jsonmapper"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/weather"
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
// shippedBindingKey is the capability these tests exercise. Named here because
// the package has no default: it serves whatever a deployment configures.
const shippedBindingKey = "mausamgram|openagrinet:WeatherObservation"

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
		BindingKey:     shippedBindingKey,
		ParticipantID:  "mausamgram",
		CapabilityCode: "openagrinet:WeatherObservation",
		BaseURL:        upstream.URL,
		Actions: map[string]model.ActionPlan{
			"select": {Method: http.MethodGet, Path: "/get-daily",
				Mappings: mappings.URL + "/" + shippedMapping, TimeoutMs: 30000, RetryMax: 3},
		},
	}}

	step, closeStep, err := weather.New(context.Background(), registry, mapper,
		&weather.Config{BindingKeys: []string{shippedBindingKey}})
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
	// A mapping transforms a payload; it does not assert who anyone is. The two
	// Uri fields in particular are only whatever the caller sent -- in a
	// deployed stack a container-internal address -- so echoing them would
	// republish another party's routing details as ours. The adapter signs what
	// it answers with instead, and that signature is what carries identity.
	for _, field := range []string{"bapId", "bapUri", "bppId", "bppUri"} {
		if _, present := beckncontext[field]; present {
			t.Errorf("response context carries %q; a mapping must not assert identity", field)
		}
	}

	commitment := firstCommitment(t, answer)
	status, _ := commitment["status"].(map[string]any)
	descriptor, _ := status["descriptor"].(map[string]any)
	// DRAFT rather than QUOTED: the Beckn v2 status enum is DRAFT, ACTIVE and
	// CLOSED, so QUOTED was refused by base schema validation. A quote is a
	// draft commitment -- nothing is committed until init and confirm.
	if descriptor["code"] != "DRAFT" {
		t.Errorf("status = %v, want DRAFT -- QUOTED is not in the spec's enum", descriptor["code"])
	}
	if commitment["offer"] == nil {
		t.Error("the quoted commitment carries no offer")
	}

	// One resource per forecast day, each with its own id. That is what the
	// openagrinet:WeatherObservation pack describes -- every one of its examples
	// carries a single validity and a flat parameters array, so a period is a
	// resource and there is no form for several in one.
	//
	// The ids are new: the consumer selected an abstract point forecast and gets
	// back the concrete days that answer it. Which means the offer's references
	// have to be rewritten, because the offer is echoed from the request and its
	// resourceIds still name the id that was asked for. Leaving them was the
	// dangling reference this file used to carry.
	resources, _ := commitment["resources"].([]any)
	if len(resources) != 3 {
		t.Fatalf("got %d resources, want 3 -- one per day the provider answered with", len(resources))
	}

	returned := make([]string, 0, len(resources))
	for _, entry := range resources {
		resource, _ := entry.(map[string]any)
		id, _ := resource["id"].(string)
		if !strings.HasPrefix(id, "res:mausamgram:forecast:") {
			t.Errorf("resource id = %q, want one derived from the forecast date", id)
		}
		// Required by Commitment.resources in the spec even though the spec
		// defines no quantity property -- a consumer that validates refuses an
		// answer without it.
		if _, present := resource["quantity"]; !present {
			t.Errorf("resource %s carries no quantity; the spec requires one on every commitment resource", id)
		}
		returned = append(returned, id)
	}

	// The offer must reference the resources actually returned, not the one that
	// was asked for. This is the assertion that fails the moment the offer is
	// echoed unchanged.
	offer, _ := commitment["offer"].(map[string]any)
	referenced, _ := offer["resourceIds"].([]any)
	if len(referenced) != len(returned) {
		t.Fatalf("offer.resourceIds has %d entries, want %d -- one per resource returned",
			len(referenced), len(returned))
	}
	for _, reference := range referenced {
		if !slices.Contains(returned, reference.(string)) {
			t.Errorf("offer references %v, which is not among the resources returned", reference)
		}
	}
	// And the descriptor the request offered is still there: only the references
	// are rewritten, not the offer.
	if offer["id"] != "offer:mausamgram:open-data" {
		t.Errorf("offer id = %v, want the one the request offered", offer["id"])
	}

	// --- the WeatherObservation schema pack, Direct mode ---------------------
	// openagrinet:WeatherObservation v0.1 requires all five of these when
	// informationMode is Direct, and each resource is one Direct observation.
	first, _ := resources[0].(map[string]any)
	attributes, _ := first["resourceAttributes"].(map[string]any)
	for _, f := range []struct{ key, want string }{
		{"@type", "openagrinet:WeatherObservation"},
		{"informationMode", "Direct"},
		{"observationType", "Forecast"},
	} {
		if attributes[f.key] != f.want {
			t.Errorf("%s = %v, want %v", f.key, attributes[f.key], f.want)
		}
	}
	for _, required := range []string{"source", "location", "generatedAt", "validity", "parameters"} {
		if attributes[required] == nil {
			t.Errorf("resourceAttributes carries no %q", required)
		}
	}

	// GeoJSON order, and the provider's own echo of the point: the mapping reads
	// response.location rather than anything the step resolved.
	location, _ := attributes["location"].(map[string]any)
	coordinates, _ := location["coordinates"].([]any)
	if len(coordinates) != 2 || coordinates[0] != 73.7898 || coordinates[1] != 19.9975 {
		t.Errorf("coordinates = %v, want [73.7898, 19.9975] in GeoJSON order", coordinates)
	}

	// This resource covers one day, so its validity opens and closes on it.
	validity, _ := attributes["validity"].(map[string]any)
	if validity["startsAt"] != "2026-08-26" || validity["endsAt"] != "2026-08-26" {
		t.Errorf("validity = %v, want the single day this resource reports", validity)
	}

	parameters, _ := attributes["parameters"].([]any)
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
	third, _ := resources[2].(map[string]any)
	thirdAttributes, _ := third["resourceAttributes"].(map[string]any)
	thirdParameters, _ := thirdAttributes["parameters"].([]any)
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

// The shipped file's request half extracts what the provider is asked for. That
// is the point of it living in the mapping: when this provider wants another
// parameter -- a date range, say -- it is an edit here and nothing else.
func TestShippedMappingsExtractTheQueryFromThePayload(t *testing.T) {
	mappings := serveMappings(t)
	defer mappings.Close()

	mapper, closeMapper, err := jsonmapper.New(context.Background(), &jsonmapper.Config{})
	if err != nil {
		t.Fatalf("failed to build the mapper: %v", err)
	}
	defer closeMapper()

	var beckn any
	if err := json.Unmarshal([]byte(selectRequest), &beckn); err != nil {
		t.Fatalf("failed to decode the request: %v", err)
	}

	got, err := mapper.Transform(context.Background(), mappings.URL+"/"+shippedMapping,
		definition.DirectionRequest, map[string]any{"beckn": beckn})
	if err != nil {
		t.Fatalf("the request half returned an unexpected error: %v", err)
	}

	var query map[string]any
	if err := json.Unmarshal(got, &query); err != nil {
		t.Fatalf("the request half produced something that is not an object: %v", err)
	}

	// GeoJSON is [lon, lat]. Reading them the other way round yields a point in
	// the wrong hemisphere that is still a valid request.
	if query["lat"] != 19.9975 {
		t.Errorf("lat = %v, want 19.9975 taken from coordinates[1]", query["lat"])
	}
	if query["lon"] != 73.7898 {
		t.Errorf("lon = %v, want 73.7898 taken from coordinates[0]", query["lon"])
	}
}

// The shipped mapping's own preconditions, against the published file. This is
// where "which geometries does this capability serve" is now answered -- in
// configuration, not in Go.
func TestShippedMappingsPreconditions(t *testing.T) {
	mappings := serveMappings(t)
	defer mappings.Close()

	mapper, closeMapper, err := jsonmapper.New(context.Background(), &jsonmapper.Config{})
	if err != nil {
		t.Fatalf("failed to build the mapper: %v", err)
	}
	defer closeMapper()
	ref := mappings.URL + "/" + shippedMapping

	payload := func(t *testing.T, geometry string) map[string]any {
		t.Helper()
		location := ""
		if geometry != "" {
			location = `"location": ` + geometry + `,`
		}
		body := `{"context":{"action":"select"},"message":{"contract":{"commitments":[{"resources":[{"resourceAttributes":{` +
			location + `"@type":"openagrinet:WeatherObservation"}}]}]}}}`
		var beckn any
		if err := json.Unmarshal([]byte(body), &beckn); err != nil {
			t.Fatalf("failed to build the payload: %v", err)
		}
		return map[string]any{"beckn": beckn}
	}

	t.Run("a Point is served", func(t *testing.T) {
		if err := mapper.Verify(context.Background(), ref,
			payload(t, `{"type":"Point","coordinates":[73.7898,19.9975]}`)); err != nil {
			t.Errorf("a Point must be served: %v", err)
		}
	})

	for _, tc := range []struct{ name, geometry string }{
		{"a polygon", `{"type":"Polygon","coordinates":[[[73.0,19.0],[74.0,19.0],[74.0,20.0],[73.0,19.0]]]}`},
		{"a line string", `{"type":"LineString","coordinates":[[73.0,19.0],[74.0,20.0]]}`},
		{"several points", `{"type":"MultiPoint","coordinates":[[73.7898,19.9975]]}`},
		{"no location at all", ``},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			err := mapper.Verify(context.Background(), ref, payload(t, tc.geometry))
			if err == nil {
				t.Fatalf("expected %s to be refused", tc.name)
			}
			// The message is the mapping's, and has to name what is needed.
			if !strings.Contains(err.Error(), "Point") {
				t.Errorf("error %q should say a Point is what this capability needs", err)
			}
		})
	}
}

// How many days the provider answers with is the provider's business, not the
// mapping's. It returns fcstday1..fcstdayN and N is whatever the forecast ran
// to, so a mapping naming five would truncate a ten-day answer and pad a
// three-day one.
//
// The ordering matters as much as the count: the keys sort lexically as
// fcstday1, fcstday10, fcstday2, so the mapping sorts on the numeric suffix. A
// ten-day forecast delivered in that order would be wrong in a way nothing
// downstream could detect.
func TestShippedMappingsTakeHoweverManyDaysTheProviderSent(t *testing.T) {
	mappings := serveMappings(t)
	defer mappings.Close()

	mapper, closeMapper, err := jsonmapper.New(context.Background(), &jsonmapper.Config{})
	if err != nil {
		t.Fatalf("failed to build the mapper: %v", err)
	}
	defer closeMapper()

	var beckn any
	if err := json.Unmarshal([]byte(selectRequest), &beckn); err != nil {
		t.Fatalf("failed to decode the request: %v", err)
	}

	for _, days := range []int{1, 3, 10} {
		t.Run(fmt.Sprintf("%d days", days), func(t *testing.T) {
			provider := map[string]any{"location": map[string]any{"lat": 19.9975, "lon": 73.7898}}
			for i := 1; i <= days; i++ {
				provider[fmt.Sprintf("fcstday%d", i)] = map[string]any{
					"date": fmt.Sprintf("2026-09-%02d", i),
					"rain": float64(i),
				}
			}

			got, err := mapper.Transform(context.Background(), mappings.URL+"/"+shippedMapping,
				definition.DirectionResponse,
				map[string]any{"beckn": beckn, "response": provider})
			if err != nil {
				t.Fatalf("Transform() returned an unexpected error: %v", err)
			}

			var answer map[string]any
			if err := json.Unmarshal(got, &answer); err != nil {
				t.Fatalf("failed to decode the answer: %v", err)
			}
			commitment := firstCommitment(t, answer)
			resources, _ := commitment["resources"].([]any)

			if len(resources) != days {
				t.Fatalf("got %d resources, want %d -- the mapping is not reading the provider's own count",
					len(resources), days)
			}

			// In the provider's order, not the keys' lexical order.
			for i, entry := range resources {
				attributes := entry.(map[string]any)["resourceAttributes"].(map[string]any)
				validity := attributes["validity"].(map[string]any)
				want := fmt.Sprintf("2026-09-%02d", i+1)
				if validity["startsAt"] != want {
					t.Errorf("resource %d covers %v, want %s -- days are out of order",
						i, validity["startsAt"], want)
				}
			}

			// However many resources there are, the offer references all of them.
			offer, _ := commitment["offer"].(map[string]any)
			referenced, _ := offer["resourceIds"].([]any)
			if len(referenced) != days {
				t.Errorf("offer references %d resources, want %d", len(referenced), days)
			}
		})
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
