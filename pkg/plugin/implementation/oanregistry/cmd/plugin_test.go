package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/oanregistry"
)

func defaultConfig() *oanregistry.Config {
	return &oanregistry.Config{
		Entity:         defaultEntity,
		ProviderEntity: defaultProviderEntity,
		Timeout:        defaultTimeout,
		RetryMax:       defaultRetryMax,
		RetryWaitMin:   defaultRetryWaitMin,
		RetryWaitMax:   defaultRetryWaitMax,
	}
}

func TestParseConfig(t *testing.T) {
	t.Parallel()

	withDefaults := func(apply func(*oanregistry.Config)) *oanregistry.Config {
		cfg := defaultConfig()
		apply(cfg)
		return cfg
	}

	testCases := []struct {
		name        string
		config      map[string]string
		expected    *oanregistry.Config
		expectedErr string
	}{
		{
			name:     "applies defaults when only a URL is given",
			config:   map[string]string{"url": "http://registry:8081/api/v1"},
			expected: withDefaults(func(c *oanregistry.Config) { c.URL = "http://registry:8081/api/v1" }),
		},
		{
			name: "reads every supported setting",
			config: map[string]string{
				"url":            "http://registry:8081/api/v1",
				"entity":         "Subscriber",
				"cacheTTL":       "30s",
				"timeout":        "5",
				"retry_max":      "3",
				"retry_wait_min": "200ms",
				"retry_wait_max": "1s",
			},
			expected: &oanregistry.Config{
				URL:            "http://registry:8081/api/v1",
				Entity:         "Subscriber",
				ProviderEntity: defaultProviderEntity,
				CacheTTL:       30 * time.Second,
				Timeout:        5,
				RetryMax:       3,
				RetryWaitMin:   200 * time.Millisecond,
				RetryWaitMax:   time.Second,
			},
		},
		{
			name: "reads an overridden provider entity",
			config: map[string]string{
				"url":            "http://registry:8081",
				"providerEntity": "ProviderCapability",
			},
			expected: withDefaults(func(c *oanregistry.Config) {
				c.URL = "http://registry:8081"
				c.ProviderEntity = "ProviderCapability"
			}),
		},
		{
			name: "ignores an empty provider entity and keeps the default",
			config: map[string]string{
				"url":            "http://registry:8081",
				"providerEntity": "",
			},
			expected: withDefaults(func(c *oanregistry.Config) {
				c.URL = "http://registry:8081"
			}),
		},
		{
			// Caching is off unless asked for: the TTL is how long a suspended
			// participant keeps verifying.
			name:     "leaves caching disabled when no TTL is set",
			config:   map[string]string{"url": "http://registry:8081"},
			expected: withDefaults(func(c *oanregistry.Config) { c.URL = "http://registry:8081" }),
		},
		{
			// Distinct from "unset", which yields the default of 1.
			name:     "honours an explicit retry_max of zero",
			config:   map[string]string{"url": "http://registry:8081", "retry_max": "0"},
			expected: withDefaults(func(c *oanregistry.Config) { c.URL = "http://registry:8081"; c.RetryMax = 0 }),
		},
		{
			name:     "ignores empty values and keeps the defaults",
			config:   map[string]string{"url": "http://registry:8081", "entity": "", "timeout": ""},
			expected: withDefaults(func(c *oanregistry.Config) { c.URL = "http://registry:8081" }),
		},
		{
			name:        "rejects a non-numeric timeout",
			config:      map[string]string{"url": "http://registry:8081", "timeout": "soon"},
			expectedErr: "invalid timeout value 'soon'",
		},
		{
			name:        "rejects a non-positive timeout",
			config:      map[string]string{"url": "http://registry:8081", "timeout": "0"},
			expectedErr: "timeout must be positive, got 0",
		},
		{
			name:        "rejects a negative retry_max",
			config:      map[string]string{"url": "http://registry:8081", "retry_max": "-1"},
			expectedErr: "retry_max must be non-negative, got -1",
		},
		{
			name:        "rejects a malformed cacheTTL",
			config:      map[string]string{"url": "http://registry:8081", "cacheTTL": "3600"},
			expectedErr: "invalid cacheTTL value '3600'",
		},
		{
			name:        "rejects a malformed retry_wait_min",
			config:      map[string]string{"url": "http://registry:8081", "retry_wait_min": "quick"},
			expectedErr: "invalid retry_wait_min value 'quick'",
		},
		{
			name: "rejects a minimum backoff above the maximum",
			config: map[string]string{
				"url":            "http://registry:8081",
				"retry_wait_min": "2s",
				"retry_wait_max": "1s",
			},
			expectedErr: "retry_wait_min (2s) must not exceed retry_wait_max (1s)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := oanRegistryProvider{}.parseConfig(tc.config)

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
		_, _, err := oanRegistryProvider{}.New(nil, nil, map[string]string{"url": "http://registry:8081"})
		if err == nil {
			t.Fatal("expected an error for a nil context, got none")
		}
	})

	t.Run("rejects a missing URL", func(t *testing.T) {
		t.Parallel()

		_, _, err := oanRegistryProvider{}.New(context.Background(), nil, map[string]string{})
		if err == nil {
			t.Fatal("expected an error for a missing URL, got none")
		}
	})

	t.Run("rejects an unparseable config", func(t *testing.T) {
		t.Parallel()

		_, _, err := oanRegistryProvider{}.New(context.Background(), nil, map[string]string{
			"url":     "http://registry:8081",
			"timeout": "soon",
		})
		if err == nil {
			t.Fatal("expected an error for an invalid timeout, got none")
		}
	})

	t.Run("builds a client from a valid config", func(t *testing.T) {
		t.Parallel()

		client, closer, err := oanRegistryProvider{}.New(context.Background(), nil, map[string]string{
			"url": "http://registry:8081/api/v1",
		})
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if client == nil {
			t.Fatal("expected a client, got nil")
		}
		if closer == nil {
			t.Fatal("expected a closer, got nil")
		}
		if err := closer(); err != nil {
			t.Errorf("expected the closer to succeed, got: %v", err)
		}
	})

	// Deliberately NOT parallel: this swaps the package-level newOANRegistryFunc,
	// so running it alongside its parallel siblings would race on that variable.
	// Go never schedules a non-parallel subtest concurrently with parallel ones,
	// which is what makes this safe -- do not add t.Parallel() "for consistency".
	t.Run("propagates a client construction failure", func(t *testing.T) {
		original := newOANRegistryFunc
		t.Cleanup(func() { newOANRegistryFunc = original })

		wantErr := errors.New("boom")
		newOANRegistryFunc = func(context.Context, definition.Cache, *oanregistry.Config) (*oanregistry.Client, func() error, error) {
			return nil, nil, wantErr
		}

		_, _, err := oanRegistryProvider{}.New(context.Background(), nil, map[string]string{"url": "http://registry:8081"})
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected the underlying error to be propagated, got: %v", err)
		}
	})
}

// TestProviderSatisfiesTheInterface fails at compile time if the exported
// Provider ever stops matching what the plugin loader looks up.
func TestProviderSatisfiesTheInterface(t *testing.T) {
	t.Parallel()

	var _ definition.RegistryLookupProvider = Provider
}
