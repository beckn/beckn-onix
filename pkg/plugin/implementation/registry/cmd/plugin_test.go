package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/registry"
)

// mockRegistryClient is a mock implementation of the RegistryLookup interface
// for testing purposes.
type mockRegistryClient struct{}

func (m *mockRegistryClient) Subscribe(ctx context.Context, subscription interface{}) error {
	return nil
}
func (m *mockRegistryClient) Lookup(ctx context.Context, subscription interface{}) ([]interface{}, error) {
	return nil, nil
}

// TestRegistryProvider_ParseConfig tests the configuration parsing logic.
func TestRegistryProvider_ParseConfig(t *testing.T) {
	t.Parallel()
	provider := registryProvider{}

	testCases := []struct {
		name        string
		config      map[string]string
		expected    *registry.Config
		expectedErr string
	}{
		{
			name: "should parse a full, valid config",
			config: map[string]string{
				"url":            "http://test.com",
				"retry_max":      "5",
				"retry_wait_min": "100ms",
				"retry_wait_max": "2s",
			},
			expected: &registry.Config{
				URL:          "http://test.com",
				RetryMax:     5,
				RetryWaitMin: 100 * time.Millisecond,
				RetryWaitMax: 2 * time.Second,
			},
			expectedErr: "",
		},
		{
			name: "should handle missing optional values",
			config: map[string]string{
				"url": "http://test.com",
			},
			expected: &registry.Config{
				URL: "http://test.com",
			},
			expectedErr: "",
		},
		{
			name: "should return error for invalid retry_max",
			config: map[string]string{
				"url":       "http://test.com",
				"retry_max": "not-a-number",
			},
			expected:    nil,
			expectedErr: "invalid retry_max value 'not-a-number'",
		},
		{
			name: "should return error for invalid retry_wait_min",
			config: map[string]string{
				"url":            "http://test.com",
				"retry_wait_min": "bad-duration",
			},
			expected:    nil,
			expectedErr: "invalid retry_wait_min value 'bad-duration'",
		},
		{
			name: "should return error for invalid retry_wait_max",
			config: map[string]string{
				"url":            "http://test.com",
				"retry_wait_max": "30parsecs",
			},
			expected:    nil,
			expectedErr: "invalid retry_wait_max value '30parsecs'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parsedConfig, err := provider.parseConfig(tc.config)

			if tc.expectedErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing '%s' but got none", tc.expectedErr)
				}
				if e, a := tc.expectedErr, err.Error(); !(a == e || (len(a) > len(e) && a[:len(e)] == e)) {
					t.Errorf("expected error message to contain '%s', but got '%s'", e, a)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, but got: %v", err)
			}
			if parsedConfig.URL != tc.expected.URL {
				t.Errorf("expected URL '%s', got '%s'", tc.expected.URL, parsedConfig.URL)
			}
			if parsedConfig.RetryMax != tc.expected.RetryMax {
				t.Errorf("expected RetryMax %d, got %d", tc.expected.RetryMax, parsedConfig.RetryMax)
			}
			if parsedConfig.RetryWaitMin != tc.expected.RetryWaitMin {
				t.Errorf("expected RetryWaitMin %v, got %v", tc.expected.RetryWaitMin, parsedConfig.RetryWaitMin)
			}
			if parsedConfig.RetryWaitMax != tc.expected.RetryWaitMax {
				t.Errorf("expected RetryWaitMax %v, got %v", tc.expected.RetryWaitMax, parsedConfig.RetryWaitMax)
			}
		})
	}
}

// TestRegistryProvider_New tests the plugin's main constructor.
func TestRegistryProvider_New(t *testing.T) {
	t.Parallel()
	provider := registryProvider{}
	originalNewRegistryFunc := newRegistryFunc

	// Cleanup to restore the original function after the test
	t.Cleanup(func() {
		newRegistryFunc = originalNewRegistryFunc
	})

	t.Run("should return error if context is nil", func(t *testing.T) {
		_, _, err := provider.New(nil, map[string]string{})
		if err == nil {
			t.Fatal("expected an error for nil context but got none")
		}
		if err.Error() != "context cannot be nil" {
			t.Errorf("expected 'context cannot be nil' error, got '%s'", err.Error())
		}
	})

	t.Run("should return error if config parsing fails", func(t *testing.T) {
		config := map[string]string{"retry_max": "invalid"}
		_, _, err := provider.New(context.Background(), config)
		if err == nil {
			t.Fatal("expected an error for bad config but got none")
		}
	})

	t.Run("should return error if registry.New fails", func(t *testing.T) {
		// Mock the newRegistryFunc to return an error
		expectedErr := errors.New("registry creation failed")
		newRegistryFunc = func(ctx context.Context, cfg *registry.Config) (*registry.RegistryClient, func() error, error) {
			return nil, nil, expectedErr
		}

		config := map[string]string{"url": "http://test.com"}
		_, _, err := provider.New(context.Background(), config)
		if err == nil {
			t.Fatal("expected an error from registry.New but got none")
		}
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error '%v', got '%v'", expectedErr, err)
		}
	})

	t.Run("should succeed and return a valid instance", func(t *testing.T) {
		// Mock the newRegistryFunc for a successful case
		mockCloser := func() error { fmt.Println("closed"); return nil }
		newRegistryFunc = func(ctx context.Context, cfg *registry.Config) (*registry.RegistryClient, func() error, error) {
			// Return a non-nil client of th correct concrete type
			return new(registry.RegistryClient), mockCloser, nil
		}

		config := map[string]string{"url": "http://test.com"}
		instance, closer, err := provider.New(context.Background(), config)
		if err != nil {
			t.Fatalf("expected no error, but got: %v", err)
		}
		if instance == nil {
			t.Fatal("expected a non-nil instance")
		}
		if closer == nil {
			t.Fatal("expected a non-nil closer function")
		}
	})
}
