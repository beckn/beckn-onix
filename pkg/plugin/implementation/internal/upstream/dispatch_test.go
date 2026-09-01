package upstream_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/upstream"
)

// dispatchMappingRef stands in for the one reference an action carries. This
// test is about dispatch, so what is behind it never matters.
const dispatchMappingRef = "https://m.example.com/mausamgram/weather-observation.select.yaml"

// dispatchRequest names a provider and a capability, which is all dispatch reads.
// Kept minimal on purpose: this test is about which step claims a request, not
// about what any of them would do with it.
const dispatchRequest = `{
  "context": { "version": "2.0.0", "action": "select" },
  "message": { "contract": { "commitments": [ {
    "resources": [ { "resourceAttributes": { "@type": "openagrinet:WeatherObservation" } } ],
    "offer": { "provider": { "id": "mausamgram" } }
  } ] } }
}`

// stubRegistry answers with one call plan, whatever is asked. This file's own,
// because it is an external test: it exercises the package exactly as the
// adapter does, through the exported surface and nothing else.
type stubRegistry struct{ plan *model.ProviderRecord }

func (s *stubRegistry) ProviderRecord(context.Context, string) (*model.ProviderRecord, error) {
	return s.plan, nil
}

// fixedMapper returns canned results, so this test is about dispatch and
// nothing else.
type fixedMapper struct{ answer string }

func (m fixedMapper) Verify(context.Context, string, any) error { return nil }

func (m fixedMapper) Transform(_ context.Context, mappingRef string, _ definition.Direction, _ any) ([]byte, error) {
	if strings.Contains(mappingRef, "request") {
		return []byte(`{}`), nil
	}
	return []byte(m.answer), nil
}

// Two provider steps in one pipeline, as a second provider would be added.
// Each must serve its own capability and leave the other's alone -- that is the
// whole dispatch mechanism, so it is worth a test rather than an assumption.
func TestTwoProviderStepsDispatchByBindingKey(t *testing.T) {
	var calledA, calledB bool
	providerA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledA = true
		fmt.Fprint(w, `{"from":"A"}`)
	}))
	defer providerA.Close()
	providerB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledB = true
		fmt.Fprint(w, `{"from":"B"}`)
	}))
	defer providerB.Close()

	newProviderStep := func(t *testing.T, bindingKey, upstreamURL, answer string) definition.Step {
		t.Helper()
		plan := &model.ProviderRecord{
			BindingKey: bindingKey,
			BaseURL:    upstreamURL,
			Actions: map[string]model.ActionPlan{
				"select": {Method: http.MethodGet, Path: "/x", Mappings: dispatchMappingRef, RetryMax: 1},
			},
		}
		step, closer, err := upstream.New(context.Background(),
			&stubRegistry{plan: plan},
			fixedMapper{answer: answer},
			nil,
			&upstream.Config{BindingKeys: []string{bindingKey}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = closer() })
		return step
	}

	steps := []definition.Step{
		newProviderStep(t, "mausamgram|openagrinet:WeatherObservation", providerA.URL, `{"served":"A"}`),
		newProviderStep(t, "agmarknet|openagrinet:MarketPrice", providerB.URL, `{"served":"B"}`),
	}

	// A request for the FIRST capability, run through both steps in order, as a
	// pipeline would.
	ctx := &model.StepContext{Context: t.Context(), Body: []byte(dispatchRequest)}
	for i, step := range steps {
		if err := step.Run(ctx); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	if !calledA {
		t.Error("provider A was not called for its own capability")
	}
	if calledB {
		t.Error("provider B was called for a capability that is not its own")
	}
	if got := string(ctx.ResponseBody); !strings.Contains(got, `"A"`) {
		t.Errorf("answer = %s, want A's -- a later step overwrote it", got)
	}
}
