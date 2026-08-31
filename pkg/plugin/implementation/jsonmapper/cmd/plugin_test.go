package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/jsonmapper"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		config      map[string]string
		expected    *jsonmapper.Config
		expectedErr string
	}{
		{
			// Everything absent is left zero on purpose: jsonmapper.New applies
			// the defaults, so they are defined in exactly one place.
			name:     "leaves everything unset for New to default",
			config:   map[string]string{},
			expected: &jsonmapper.Config{},
		},
		{
			name: "reads every supported setting",
			config: map[string]string{
				"fetchTimeout":    "3s",
				"cacheTTL":        "30m",
				"negativeTTL":     "45s",
				"maxMappingBytes": "1024",
				"maxCacheEntries": "50",
			},
			expected: &jsonmapper.Config{
				FetchTimeout:    3 * time.Second,
				CacheTTL:        30 * time.Minute,
				NegativeTTL:     45 * time.Second,
				MaxMappingBytes: 1024,
				MaxCacheEntries: 50,
			},
		},
		{
			name:     "ignores empty values",
			config:   map[string]string{"fetchTimeout": "", "maxCacheEntries": ""},
			expected: &jsonmapper.Config{},
		},
		{
			name:        "rejects a malformed duration",
			config:      map[string]string{"fetchTimeout": "soon"},
			expectedErr: "invalid fetchTimeout value 'soon'",
		},
		{
			// Zero would mean "no timeout", which is the opposite of what an
			// operator writing 0 expects.
			name:        "rejects a non-positive duration",
			config:      map[string]string{"fetchTimeout": "0s"},
			expectedErr: "fetchTimeout must be positive",
		},
		{
			name:        "rejects a malformed byte cap",
			config:      map[string]string{"maxMappingBytes": "lots"},
			expectedErr: "invalid maxMappingBytes value 'lots'",
		},
		{
			name:        "rejects a non-positive byte cap",
			config:      map[string]string{"maxMappingBytes": "0"},
			expectedErr: "maxMappingBytes must be positive",
		},
		{
			name:        "rejects a malformed cache size",
			config:      map[string]string{"maxCacheEntries": "many"},
			expectedErr: "invalid maxCacheEntries value 'many'",
		},
		{
			name:        "rejects a non-positive cache size",
			config:      map[string]string{"maxCacheEntries": "0"},
			expectedErr: "maxCacheEntries must be positive",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := jsonMapperProvider{}.parseConfig(tc.config)

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

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("rejects a nil context", func(t *testing.T) {
		t.Parallel()

		//nolint:staticcheck // deliberately passing a nil context to assert the guard.
		_, _, err := jsonMapperProvider{}.New(nil, map[string]string{})
		if err == nil {
			t.Fatal("expected an error for a nil context, got none")
		}
	})

	t.Run("rejects an unparseable config", func(t *testing.T) {
		t.Parallel()

		_, _, err := jsonMapperProvider{}.New(context.Background(), map[string]string{"cacheTTL": "soon"})
		if err == nil {
			t.Fatal("expected an error for an invalid cacheTTL, got none")
		}
	})

	t.Run("builds a mapper from an empty config", func(t *testing.T) {
		t.Parallel()

		mapper, closer, err := jsonMapperProvider{}.New(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if mapper == nil {
			t.Fatal("expected a mapper, got nil")
		}
		if closer == nil {
			t.Fatal("expected a closer, got nil")
		}
		if err := closer(); err != nil {
			t.Errorf("expected the closer to succeed, got: %v", err)
		}
	})

	// Deliberately NOT parallel: this swaps the package-level newMapperFunc, so
	// running it alongside its parallel siblings would race on that variable.
	t.Run("propagates a construction failure", func(t *testing.T) {
		original := newMapperFunc
		t.Cleanup(func() { newMapperFunc = original })

		wantErr := errors.New("boom")
		newMapperFunc = func(context.Context, *jsonmapper.Config) (*jsonmapper.Mapper, func() error, error) {
			return nil, nil, wantErr
		}

		_, _, err := jsonMapperProvider{}.New(context.Background(), map[string]string{})
		if !errors.Is(err, wantErr) {
			t.Errorf("expected the construction error to propagate, got %v", err)
		}
	})
}
