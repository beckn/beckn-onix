package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/weather"
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
		expected    *weather.Config
		expectedErr string
	}{
		{
			// Everything absent is left zero: weather.New defaults it, so the
			// rules are defined in exactly one place.
			name:     "leaves everything unset for New to default",
			config:   map[string]string{},
			expected: &weather.Config{},
		},
		{
			name: "reads every supported setting",
			config: map[string]string{
				"bindingKeys":      "other|capability",
				"authScheme":       "basic",
				"usernameEnv":      "U",
				"passwordEnv":      "P",
				"headerName":       "X-Key",
				"headerValueEnv":   "V",
				"maxResponseBytes": "2048",
			},
			expected: &weather.Config{
				BindingKeys:      []string{"other|capability"},
				AuthScheme:       "basic",
				UsernameEnv:      "U",
				PasswordEnv:      "P",
				HeaderName:       "X-Key",
				HeaderValueEnv:   "V",
				MaxResponseBytes: 2048,
			},
		},
		{
			name:        "rejects a malformed response cap",
			config:      map[string]string{"maxResponseBytes": "lots"},
			expectedErr: "invalid maxResponseBytes value 'lots'",
		},
		{
			name:        "rejects a non-positive response cap",
			config:      map[string]string{"maxResponseBytes": "0"},
			expectedErr: "maxResponseBytes must be positive",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := weatherProvider{}.parseConfig(tc.config)

			if tc.expectedErr != "" {
				if err == nil {
					t.Fatalf("expected error %q but got none", tc.expectedErr)
				}
				if !strings.Contains(err.Error(), tc.expectedErr) {
					t.Errorf("expected error containing %q, got %q", tc.expectedErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("expected config %+v, got %+v", tc.expected, got)
			}
		})
	}
}

// A plugin config is map[string]string, so a list arrives comma-separated --
// the convention reqpreprocessor and schemav2validator already use. Binding keys
// separate their own halves with a pipe, so a comma is unambiguous.
func TestParseConfigReadsSeveralBindingKeys(t *testing.T) {
	t.Parallel()

	cfg, err := weatherProvider{}.parseConfig(map[string]string{
		"bindingKeys": "a|openagrinet:One, b|openagrinet:Two ,,  ",
	})
	if err != nil {
		t.Fatalf("parseConfig() returned an unexpected error: %v", err)
	}
	want := []string{"a|openagrinet:One", "b|openagrinet:Two"}
	if len(cfg.BindingKeys) != len(want) {
		t.Fatalf("binding keys = %v, want %v -- blanks should be dropped and spaces trimmed", cfg.BindingKeys, want)
	}
	for i, key := range want {
		if cfg.BindingKeys[i] != key {
			t.Errorf("binding key %d = %q, want %q", i, cfg.BindingKeys[i], key)
		}
	}
}

// The override is two keys, both or neither. Absent leaves the step on the
// Beckn v2 convention, which is what every deployment should be running.
func TestParseConfigReadsTheBindingKeyOverride(t *testing.T) {
	t.Parallel()

	cfg, err := weatherProvider{}.parseConfig(map[string]string{
		"bindingKeys":      "a|openagrinet:One",
		"providerIdAt":     "who.provider",
		"capabilityCodeAt": "what[].type",
	})
	if err != nil {
		t.Fatalf("parseConfig() returned an unexpected error: %v", err)
	}
	if cfg.ProviderIDAt != "who.provider" || cfg.CapabilityCodeAt != "what[].type" {
		t.Errorf("override = %q / %q, want the configured paths", cfg.ProviderIDAt, cfg.CapabilityCodeAt)
	}
}

// Absent leaves them empty, and upstream reads that as "use the convention".
func TestParseConfigLeavesTheOverrideUnsetByDefault(t *testing.T) {
	t.Parallel()

	cfg, err := weatherProvider{}.parseConfig(map[string]string{"bindingKeys": "a|openagrinet:One"})
	if err != nil {
		t.Fatalf("parseConfig() returned an unexpected error: %v", err)
	}
	if cfg.ProviderIDAt != "" || cfg.CapabilityCodeAt != "" {
		t.Errorf("override = %q / %q, want both empty", cfg.ProviderIDAt, cfg.CapabilityCodeAt)
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("rejects a nil context", func(t *testing.T) {
		t.Parallel()

		//nolint:staticcheck // deliberately passing a nil context to assert the guard.
		_, _, err := weatherProvider{}.New(nil, stubRegistry{}, stubMapper{}, map[string]string{})
		if err == nil {
			t.Fatal("expected an error for a nil context, got none")
		}
	})

	t.Run("rejects an unparseable config", func(t *testing.T) {
		t.Parallel()

		_, _, err := weatherProvider{}.New(context.Background(), stubRegistry{}, stubMapper{},
			map[string]string{"maxResponseBytes": "lots"})
		if err == nil {
			t.Fatal("expected an error for an invalid cap, got none")
		}
	})

	t.Run("propagates an invalid auth scheme from New", func(t *testing.T) {
		t.Parallel()

		_, _, err := weatherProvider{}.New(context.Background(), stubRegistry{}, stubMapper{},
			map[string]string{"authScheme": "oauth"})
		if err == nil {
			t.Fatal("expected an unknown auth scheme to be refused")
		}
	})

	// A domain plugin serves a family of capabilities, so it cannot guess which
	// of them a deployment has providers for. Refused at startup rather than
	// answering to nothing.
	t.Run("refuses a config naming no capability", func(t *testing.T) {
		t.Parallel()

		if _, _, err := (weatherProvider{}).New(context.Background(), stubRegistry{}, stubMapper{},
			map[string]string{}); err == nil {
			t.Fatal("expected a config with no bindingKeys to be refused")
		}
	})

	t.Run("builds a step from the capabilities it is given", func(t *testing.T) {
		t.Parallel()

		step, closer, err := weatherProvider{}.New(context.Background(), stubRegistry{}, stubMapper{},
			map[string]string{"bindingKeys": "imd|openagrinet:WeatherObservation"})
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if step == nil {
			t.Fatal("expected a step, got nil")
		}
		if err := closer(); err != nil {
			t.Errorf("expected the closer to succeed, got: %v", err)
		}
	})

	// Deliberately NOT parallel: this swaps the package-level newStepFunc.
	t.Run("propagates a construction failure", func(t *testing.T) {
		original := newStepFunc
		t.Cleanup(func() { newStepFunc = original })

		wantErr := errors.New("boom")
		newStepFunc = func(context.Context, definition.ProviderRecordLookup, definition.Mapper, *weather.Config) (definition.Step, func() error, error) {
			return nil, nil, wantErr
		}

		_, _, err := weatherProvider{}.New(context.Background(), stubRegistry{}, stubMapper{}, map[string]string{})
		if !errors.Is(err, wantErr) {
			t.Errorf("expected the construction error to propagate, got %v", err)
		}
	})
}
