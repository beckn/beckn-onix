package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/agrifacility"
)

type stubRegistry struct{}

func (stubRegistry) ProviderRecord(context.Context, string) (*model.ProviderRecord, error) {
	return nil, nil
}

type stubMapper struct{}

func (stubMapper) Verify(context.Context, string, any) error { return nil }

func (stubMapper) Transform(context.Context, string, definition.Direction, any) ([]byte, error) {
	return nil, nil
}

func TestParseConfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		config      map[string]string
		expected    *agrifacility.Config
		expectedErr string
	}{
		{
			// Everything absent is left zero: agrifacility.New defaults it, so
			// the rules are defined in exactly one place.
			name:     "leaves everything unset for New to default",
			config:   map[string]string{},
			expected: &agrifacility.Config{},
		},
		{
			// Every key upstream.Config accepts, including the query scheme
			// added for Agmarknet. POCRA needs none of the credential pairs,
			// but parseConfig is the only thing that reads them and a dropped
			// field here is silent.
			name: "reads every supported setting",
			config: map[string]string{
				"bindingKeys":      "pocra|openagrinet:AgricultureFacility",
				"authScheme":       "none",
				"usernameEnv":      "U",
				"passwordEnv":      "P",
				"headerName":       "X-Key",
				"headerValueEnv":   "V",
				"queryName":        "token",
				"queryValueEnv":    "Q",
				"maxResponseBytes": "2048",
			},
			expected: &agrifacility.Config{
				BindingKeys:      []string{"pocra|openagrinet:AgricultureFacility"},
				AuthScheme:       "none",
				UsernameEnv:      "U",
				PasswordEnv:      "P",
				HeaderName:       "X-Key",
				HeaderValueEnv:   "V",
				QueryName:        "token",
				QueryValueEnv:    "Q",
				MaxResponseBytes: 2048,
			},
		},
		{
			name: "reads several binding keys and trims them",
			config: map[string]string{
				"bindingKeys": "pocra|openagrinet:AgricultureFacility, other|openagrinet:Thing ,",
			},
			expected: &agrifacility.Config{
				BindingKeys: []string{
					"pocra|openagrinet:AgricultureFacility",
					"other|openagrinet:Thing",
				},
			},
		},
		{
			name: "reads the binding key path override",
			config: map[string]string{
				"providerIdAt":     "who.provider",
				"capabilityCodeAt": "what[].type",
			},
			expected: &agrifacility.Config{
				ProviderIDAt:     "who.provider",
				CapabilityCodeAt: "what[].type",
			},
		},
		{
			name:        "refuses a non-numeric maxResponseBytes",
			config:      map[string]string{"maxResponseBytes": "big"},
			expectedErr: "invalid maxResponseBytes",
		},
		{
			name:        "refuses a non-positive maxResponseBytes",
			config:      map[string]string{"maxResponseBytes": "0"},
			expectedErr: "must be positive",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := agriFacilityProvider{}.parseConfig(tc.config)
			if tc.expectedErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.expectedErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConfig() returned an unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("config = %+v, want %+v", got, tc.expected)
			}
		})
	}
}

func TestNewRefusesANilContext(t *testing.T) {
	t.Parallel()

	step, closer, err := agriFacilityProvider{}.New(nil, stubRegistry{}, stubMapper{}, map[string]string{}) //nolint:staticcheck // passing nil is what this asserts is refused
	if err == nil {
		t.Fatal("expected a nil context to be refused")
	}
	if step != nil || closer != nil {
		t.Error("a refused construction must return neither a step nor a closer")
	}
}

func TestNewPropagatesAConstructionFailure(t *testing.T) {
	original := newStepFunc
	defer func() { newStepFunc = original }()
	newStepFunc = func(context.Context, definition.ProviderRecordLookup, definition.Mapper,
		*agrifacility.Config) (definition.Step, func() error, error) {
		return nil, nil, errors.New("boom")
	}

	_, _, err := agriFacilityProvider{}.New(context.Background(), stubRegistry{}, stubMapper{},
		map[string]string{"bindingKeys": "pocra|openagrinet:AgricultureFacility"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want it to carry the construction failure", err)
	}
}

// bindingKeys has no default, because a package serving a family cannot guess
// which of them a deployment has providers for. Absent must fail at startup
// rather than leave a step that answers nothing.
func TestNewRequiresBindingKeys(t *testing.T) {
	t.Parallel()

	_, _, err := agriFacilityProvider{}.New(context.Background(), stubRegistry{}, stubMapper{},
		map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "bindingKeys") {
		t.Fatalf("error = %v, want it to name bindingKeys", err)
	}
}

func TestNewBuildsAStep(t *testing.T) {
	t.Parallel()

	step, closer, err := agriFacilityProvider{}.New(context.Background(), stubRegistry{}, stubMapper{},
		map[string]string{"bindingKeys": "pocra|openagrinet:AgricultureFacility"})
	if err != nil {
		t.Fatalf("New() returned an unexpected error: %v", err)
	}
	if step == nil {
		t.Fatal("New() returned no step")
	}
	if closer == nil {
		t.Fatal("New() returned no closer")
	}
	if err := closer(); err != nil {
		t.Errorf("closer() returned %v", err)
	}
}
