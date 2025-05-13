package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// TestValidate tests the validation function for Cache configurations
func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr error
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: ErrEmptyConfig,
		},
		{
			name:    "empty addr",
			cfg:     &Config{Addr: ""},
			wantErr: ErrAddrMissing,
		},
		{
			name:    "valid config",
			cfg:     &Config{Addr: "localhost:6379"},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.cfg)
			assert.Equal(t, tt.wantErr, err)
		})
	}
}

// TestNew tests the validation behavior of the constructor
func TestNew(t *testing.T) {
	// Save original env and restore after test
	origPassword := os.Getenv("REDIS_PASSWORD")
	defer os.Setenv("REDIS_PASSWORD", origPassword)

	// Test validation errors directly
	tests := []struct {
		name          string
		cfg           *Config
		envPassword   string
		expectErr     bool
		errorContains string
	}{
		{
			name:          "nil config",
			cfg:           nil,
			envPassword:   "password",
			expectErr:     true,
			errorContains: "empty config",
		},
		{
			name:          "empty address",
			cfg:           &Config{Addr: ""},
			envPassword:   "password",
			expectErr:     true,
			errorContains: "missing required field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment for this test
			os.Setenv("REDIS_PASSWORD", tt.envPassword)

			ctx := context.Background()
			cache, cleanup, err := New(ctx, tt.cfg)

			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, cache)
				assert.Nil(t, cleanup)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, cache)
				assert.NotNil(t, cleanup)
			}
		})
	}
}

// TestCache_Get tests the Get method of the Cache type
func TestCache_Get(t *testing.T) {
	// Skip for now as we need to refactor to inject our mocks
	t.Skip("Cache.Get test skipped - cannot inject mocks at this time")
}

// TestCache_Set tests the Set method of the Cache type
func TestCache_Set(t *testing.T) {
	// Skip for now as we need to refactor to inject our mocks
	t.Skip("Cache.Set test skipped - cannot inject mocks at this time")
}

// TestCache_Delete tests the Delete method of the Cache type
func TestCache_Delete(t *testing.T) {
	// Skip for now as we need to refactor to inject our mocks
	t.Skip("Cache.Delete test skipped - cannot inject mocks at this time")
}

// TestCache_Clear tests the Clear method of the Cache type
func TestCache_Clear(t *testing.T) {
	// Skip for now as we need to refactor to inject our mocks
	t.Skip("Cache.Clear test skipped - cannot inject mocks at this time")
}

// Integration test that tests all Redis operations with a real Redis server
func TestCacheIntegration(t *testing.T) {
	// Run this test by default since we have a Redis server available
	// To skip, set SKIP_REDIS_INTEGRATION_TEST=true
	if os.Getenv("SKIP_REDIS_INTEGRATION_TEST") == "true" {
		t.Skip("Integration test skipped - SKIP_REDIS_INTEGRATION_TEST=true")
	}

	// Set up test environment
	ctx := context.Background()
	cfg := &Config{
		Addr: "localhost:6379",
	}

	// Set empty password for local testing
	if err := os.Setenv("REDIS_PASSWORD", ""); err != nil {
		t.Fatalf("Failed to set environment variable: %v", err)
	}
	// Create a new cache
	cache, cleanup, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	defer cleanup()

	// Test Set and Get
	key := "test_key"
	value := "test_value"
	ttl := time.Minute

	err = cache.Set(ctx, key, value, ttl)
	assert.NoError(t, err, "Set should not return an error")
	got, err := cache.Get(ctx, key)
	assert.NoError(t, err, "Get should not return an error")
	assert.Equal(t, value, got, "Get should return the set value")

	// Test Delete
	err = cache.Delete(ctx, key)
	assert.NoError(t, err, "Delete should not return an error")

	// Verify key is gone
	_, err = cache.Get(ctx, key)
	assert.Equal(t, redis.Nil, err, "Get should return redis.Nil after deletion")

	// Test Clear
	// First set multiple keys
	key1 := "test_key1"
	value1 := "test_value1"
	key2 := "test_key2"
	value2 := "test_value2"

	err = cache.Set(ctx, key1, value1, ttl)
	assert.NoError(t, err, "Set should not return an error")

	err = cache.Set(ctx, key2, value2, ttl)
	assert.NoError(t, err, "Set should not return an error")

	// Clear all keys
	err = cache.Clear(ctx)
	assert.NoError(t, err, "Clear should not return an error")

	// Verify keys are gone
	_, err = cache.Get(ctx, key1)
	assert.Equal(t, redis.Nil, err, "Get should return redis.Nil after clear")

	_, err = cache.Get(ctx, key2)
	assert.Equal(t, redis.Nil, err, "Get should return redis.Nil after clear")
}
