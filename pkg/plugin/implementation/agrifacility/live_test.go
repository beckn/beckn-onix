package agrifacility_test

// live_test.go runs the shipped mapping against the REAL POCRA API.
//
// Skipped unless POCRA_LIVE=1, because it needs the public internet and a
// government API that takes about eight seconds to answer. Everything it covers
// that can be covered offline is covered offline in mappings_test.go; what only
// this can prove is that the request this mapping builds is one POCRA actually
// accepts, and that the answer it actually returns still maps.
//
//	POCRA_LIVE=1 go test ./pkg/plugin/implementation/agrifacility/ -run TestLive -v
//
// It was worth writing. The captured sample in the design notes covered kvk
// alone, and running it against all four types is what found that a warehouse
// comes from a different BPP with no category tag on its items.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/agrifacility"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/jsonmapper"
)

const pocraBaseURL = "https://middleware-bap-client.mahapocra.gov.in"

// governedTypes is every facility type the pack defines. All four are exercised,
// because they do not all come from the same upstream BPP.
var governedTypes = []string{
	"KrishiVigyanKendra",
	"CustomHiringCentre",
	"SoilTestingFacility",
	"Warehouse",
}

// liveRequest builds a select for one facility type, with ids that have never
// been used before.
//
// The fresh transaction id matters: POCRA accumulates answers per transaction,
// so reusing one returns everything previously asked for under it as well.
func liveRequest(t *testing.T, facilityType string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(selectRequest), &payload); err != nil {
		t.Fatalf("the fixture is not JSON: %v", err)
	}
	attributes(t, payload)["supportedFacilityTypes"] = []any{facilityType}

	// UUIDs, not just unique strings. POCRA's schema constrains transaction_id
	// and message_id to the uuid format, and the mapping passes the Beckn ids
	// through verbatim -- so a caller sending anything else earns a 400 from
	// POCRA that names the field.
	beckncontext := payload["context"].(map[string]any)
	beckncontext["transactionId"] = uuid.NewString()
	beckncontext["messageId"] = uuid.NewString()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("could not build the request: %v", err)
	}
	return string(body)
}

// recordingProxy forwards to POCRA and keeps a copy of what came back.
//
// It exists so one upstream call answers two questions at once: what POCRA
// actually returned, and what the mapping made of it. Without it a zero-facility
// answer is ambiguous -- POCRA having nothing to say and the mapping dropping
// everything look identical from the outside, and only one of those is a bug.
//
// POCRA takes about eight seconds per call, so asking it twice to disambiguate
// would double an already slow test.
func recordingProxy(t *testing.T, captured *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("could not read the request bound for POCRA: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		request, err := http.NewRequestWithContext(r.Context(), r.Method,
			pocraBaseURL+r.URL.Path, bytes.NewReader(sent))
		if err != nil {
			t.Errorf("could not build the request to POCRA: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		request.Header.Set("Content-Type", "application/json")

		response, err := (&http.Client{Timeout: 60 * time.Second}).Do(request)
		if err != nil {
			// POCRA times out often enough that this is a normal outcome
			// rather than a test failure. Passing the status through lets the
			// step report it the way it would in production.
			t.Logf("POCRA did not answer: %v", err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer response.Body.Close()

		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Errorf("could not read POCRA's answer: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		*captured = body
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
	}))
}

// pocraItemCount counts the facilities POCRA actually returned, across every
// responses[] entry, before the mapping touches them.
func pocraItemCount(t *testing.T, body []byte) int {
	t.Helper()
	if len(body) == 0 {
		return 0
	}
	var answer struct {
		Responses []struct {
			Message struct {
				Catalog struct {
					Providers []struct {
						Items []json.RawMessage `json:"items"`
					} `json:"providers"`
				} `json:"catalog"`
			} `json:"message"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("POCRA answered with something that is not JSON: %v", err)
	}
	count := 0
	for _, response := range answer.Responses {
		for _, provider := range response.Message.Catalog.Providers {
			count += len(provider.Items)
		}
	}
	return count
}

func TestLiveAgainstPocra(t *testing.T) {
	if os.Getenv("POCRA_LIVE") != "1" {
		t.Skip("set POCRA_LIVE=1 to run this against the real POCRA API")
	}

	mappings := serveMappings(t)
	defer mappings.Close()

	mapper, closeMapper, err := jsonmapper.New(context.Background(), &jsonmapper.Config{})
	if err != nil {
		t.Fatalf("failed to build the mapper: %v", err)
	}
	defer closeMapper()

	var captured []byte
	proxy := recordingProxy(t, &captured)
	defer proxy.Close()

	registry := &stubRegistry{plan: &model.ProviderRecord{
		BindingKey:     shippedBindingKey,
		ParticipantID:  "pocra",
		CapabilityCode: shippedCapability,
		BaseURL:        proxy.URL,
		Actions: map[string]model.ActionPlan{
			// The same budget the registry row publishes. POCRA answers in
			// about eight seconds, so 30s is the right order of magnitude and
			// 15s would be uncomfortably close.
			"select": {Method: http.MethodPost, Path: "/search",
				Mappings: mappings.URL + "/" + shippedMapping, TimeoutMs: 30000, RetryMax: 2},
		},
	}}

	step, closeStep, err := agrifacility.New(context.Background(), registry, mapper,
		&agrifacility.Config{BindingKeys: []string{shippedBindingKey}})
	if err != nil {
		t.Fatalf("failed to build the step: %v", err)
	}
	defer closeStep()

	for _, facilityType := range governedTypes {
		t.Run(facilityType, func(t *testing.T) {
			captured = nil
			stepCtx := &model.StepContext{Context: t.Context(), Body: []byte(liveRequest(t, facilityType))}
			started := time.Now()
			if err := step.Run(stepCtx); err != nil {
				t.Fatalf("Run() returned an unexpected error: %v", err)
			}
			elapsed := time.Since(started)

			var answer map[string]any
			if err := json.Unmarshal(stepCtx.ResponseBody, &answer); err != nil {
				t.Fatalf("the answer is not JSON: %v", err)
			}
			resources := answerResources(t, answer)
			offered := pocraItemCount(t, captured)
			t.Logf("%s: POCRA returned %d items -> %d facilities in %s, %d bytes",
				facilityType, offered, len(resources), elapsed.Round(time.Millisecond),
				len(stepCtx.ResponseBody))

			// An empty answer is only a bug when POCRA had something to say.
			// Its BPPs answer with responses[] empty often enough that failing
			// on it would make this test red for reasons nothing here controls
			// -- chc did exactly that, minutes after returning five facilities.
			if offered == 0 {
				if len(resources) != 0 {
					t.Errorf("POCRA returned nothing but the mapping produced %d facilities",
						len(resources))
				}
				t.Skipf("POCRA returned no %s facilities for this point", facilityType)
			}
			if len(resources) == 0 {
				t.Fatalf("POCRA returned %d items and the mapping produced no facilities", offered)
			}
			// Deduplication only ever removes.
			if len(resources) > offered {
				t.Errorf("got %d facilities from %d items; the mapping invented some",
					len(resources), offered)
			}

			for _, entry := range resources {
				id, _ := dig(entry, "id").(string)
				attrs, ok := dig(entry, "resourceAttributes").(map[string]any)
				if !ok {
					t.Fatalf("resource %s carries no resourceAttributes", id)
				}
				// Direct mode's required set, which is what a consumer
				// validating against the pack will refuse an answer for.
				if attrs["facilityType"] != facilityType {
					t.Errorf("%s: facilityType = %v, want %s", id, attrs["facilityType"], facilityType)
				}
				if attrs["informationMode"] != "Direct" {
					t.Errorf("%s: informationMode = %v, want Direct", id, attrs["informationMode"])
				}
				if attrs["source"] == nil {
					t.Errorf("%s: Direct mode requires source", id)
				}
				if attrs["address"] == nil && attrs["location"] == nil {
					t.Errorf("%s: Direct mode requires one of address or location", id)
				}
				if _, present := attrs["location"]; present {
					t.Errorf("%s: carries location; POCRA publishes no verified facility geometry", id)
				}
			}

			// Nothing POCRA uses as a placeholder may reach the network.
			raw, err := json.Marshal(answer)
			if err != nil {
				t.Fatalf("could not re-encode the answer: %v", err)
			}
			for _, placeholder := range []string{"Unknown", "N/A", "000000", `"-"`} {
				if strings.Contains(string(raw), placeholder) {
					t.Errorf("the answer carries the placeholder %s", placeholder)
				}
			}
		})
	}
}

// TestLiveCategoryLeakIsFiltered provokes the real hazard and checks the mapping
// holds against it.
//
// POCRA caches per message_id for PT10M and accumulates across searches sharing
// one, so two searches with the same messageId and different categories make the
// second answer carry both. Beckn requires a fresh messageId per message, but an
// adapter cannot assume every caller obeys that -- a retry that reuses one is the
// obvious way in -- so the mapping filters to the requested type rather than
// trusting the upstream to have done it.
//
// This is the only test that proves the filter against POCRA's actual behaviour
// rather than a fixture built from it.
func TestLiveCategoryLeakIsFiltered(t *testing.T) {
	if os.Getenv("POCRA_LIVE") != "1" {
		t.Skip("set POCRA_LIVE=1 to run this against the real POCRA API")
	}

	mappings := serveMappings(t)
	defer mappings.Close()

	mapper, closeMapper, err := jsonmapper.New(context.Background(), &jsonmapper.Config{})
	if err != nil {
		t.Fatalf("failed to build the mapper: %v", err)
	}
	defer closeMapper()

	var captured []byte
	proxy := recordingProxy(t, &captured)
	defer proxy.Close()

	registry := &stubRegistry{plan: &model.ProviderRecord{
		BindingKey: shippedBindingKey, BaseURL: proxy.URL,
		Actions: map[string]model.ActionPlan{
			"select": {Method: http.MethodPost, Path: "/search",
				Mappings: mappings.URL + "/" + shippedMapping, TimeoutMs: 30000},
		},
	}}

	step, closeStep, err := agrifacility.New(context.Background(), registry, mapper,
		&agrifacility.Config{BindingKeys: []string{shippedBindingKey}})
	if err != nil {
		t.Fatalf("failed to build the step: %v", err)
	}
	defer closeStep()

	// One messageId, deliberately shared. Different transaction ids, to show it
	// is the messageId the upstream keys its cache on.
	shared := uuid.NewString()

	search := func(facilityType string) (raw []byte, answer map[string]any) {
		t.Helper()
		var payload map[string]any
		if err := json.Unmarshal([]byte(selectRequest), &payload); err != nil {
			t.Fatalf("the fixture is not JSON: %v", err)
		}
		attributes(t, payload)["supportedFacilityTypes"] = []any{facilityType}
		beckncontext := payload["context"].(map[string]any)
		beckncontext["messageId"] = shared
		beckncontext["transactionId"] = uuid.NewString()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("could not build the request: %v", err)
		}

		captured = nil
		stepCtx := &model.StepContext{Context: t.Context(), Body: body}
		if err := step.Run(stepCtx); err != nil {
			t.Fatalf("%s: Run() returned an unexpected error: %v", facilityType, err)
		}
		if err := json.Unmarshal(stepCtx.ResponseBody, &answer); err != nil {
			t.Fatalf("%s: the answer is not JSON: %v", facilityType, err)
		}
		return captured, answer
	}

	search("KrishiVigyanKendra")
	raw, answer := search("CustomHiringCentre")

	// Did the hazard actually happen? If POCRA has stopped accumulating there is
	// nothing here to filter, and passing would say more than it knows.
	if !bytes.Contains(raw, []byte(`"kvk"`)) {
		t.Skip("POCRA did not leak kvk into the chc search; nothing to filter this run")
	}
	t.Log("POCRA leaked kvk facilities into a chc search, as expected")

	resources := answerResources(t, answer)
	if len(resources) == 0 {
		t.Fatal("the mapping filtered everything away")
	}
	for _, entry := range resources {
		id, _ := dig(entry, "id").(string)
		attrs, ok := dig(entry, "resourceAttributes").(map[string]any)
		if !ok {
			t.Fatalf("resource %s carries no resourceAttributes", id)
		}
		if attrs["facilityType"] != "CustomHiringCentre" {
			t.Errorf("%s: facilityType = %v, want CustomHiringCentre -- a leaked facility reached the answer",
				id, attrs["facilityType"])
		}
	}
	t.Logf("kept %d facilities, all CustomHiringCentre", len(resources))
}
