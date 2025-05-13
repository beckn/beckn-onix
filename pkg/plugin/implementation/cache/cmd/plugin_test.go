package main

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseConfig tests the configuration parsing logic of the plugin
func TestParseConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]string
		want    *Config
		wantErr bool
	}{
		{
			name:    "missing addr",
			config:  map[string]string{},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "empty addr",
			config:  map[string]string{"addr": ""},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "basic config",
			config:  map[string]string{"addr": "localhost:6379"},
			want:    &Config{Addr: "localhost:6379", DB: 0, Password: ""},
			wantErr: false,
		},
		{
			name:    "with db",
			config:  map[string]string{"addr": "localhost:6379", "db": "1"},
			want:    &Config{Addr: "localhost:6379", DB: 1, Password: ""},
			wantErr: false,
		},
		{
			name:    "with password",
			config:  map[string]string{"addr": "localhost:6379", "password": "secret"},
			want:    &Config{Addr: "localhost:6379", DB: 0, Password: "secret"},
			wantErr: false,
		},
		{
			name:    "invalid db",
			config:  map[string]string{"addr": "localhost:6379", "db": "invalid"},
			want:    &Config{Addr: "localhost:6379", DB: 0, Password: ""},
			wantErr: false, // Not an error, just defaults to 0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseConfig(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// TestConvertToRedisConfig tests the configuration conversion logic
func TestConvertToRedisConfig(t *testing.T) {
	cfg := &Config{
		Addr:     "localhost:6379",
		DB:       1,
		Password: "secret",
	}

	redisConfig := convertToRedisConfig(cfg)

	assert.NotNil(t, redisConfig)
	assert.Equal(t, cfg.Addr, redisConfig.Addr)
}

// TestProviderNew tests the New method of the cacheProvider
func TestProviderNew(t *testing.T) {
	provider := cacheProvider{}

	// Save original environment variable and restore it after test
	origPassword := os.Getenv("REDIS_PASSWORD")
	defer func() {
		if err := os.Setenv("REDIS_PASSWORD", origPassword); err != nil {
			t.Fatalf("Failed to restore REDIS_PASSWORD: %v", err)
		}
	}()
	// Set an empty password for testing
	if err := os.Setenv("REDIS_PASSWORD", ""); err != nil {
		t.Fatalf("Failed to set REDIS_PASSWORD: %v", err)
	}

	tests := []struct {
		name      string
		ctx       context.Context
		config    map[string]string
		expectErr bool
	}{
		{
			name:      "nil context",
			ctx:       nil,
			config:    map[string]string{"addr": "localhost:6379"},
			expectErr: true,
		},
		{
			name:      "invalid config",
			ctx:       context.Background(),
			config:    map[string]string{}, // Missing addr
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache, cleanup, err := provider.New(tt.ctx, tt.config)

			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, cache)
				assert.Nil(t, cleanup)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, cache)
				assert.NotNil(t, cleanup)
			}
		})
	}
}

// TestProviderIntegration tests the provider with a real Redis server
func TestProviderIntegration(t *testing.T) {
	// Skip this test if requested
	if os.Getenv("SKIP_REDIS_INTEGRATION_TEST") == "true" {
		t.Skip("Integration test skipped - SKIP_REDIS_INTEGRATION_TEST=true")
	}

	// Set an empty password for testing
	if err := os.Setenv("REDIS_PASSWORD", ""); err != nil {
		t.Fatalf("Failed to set REDIS_PASSWORD: %v", err)
	}
	
	// Ensure we clean up the environment variable at the end
	defer func() {
		if err := os.Unsetenv("REDIS_PASSWORD"); err != nil {
			t.Fatalf("Failed to unset REDIS_PASSWORD: %v", err)
		}
	}()

	// Create provider and test with real Redis
	provider := cacheProvider{}
	ctx := context.Background()
	config := map[string]string{
		"addr": "localhost:6379",
		"db":   "0",
	}

	cache, cleanup, err := provider.New(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("Failed to clean up Redis client: %v", err)
		}
	}()

	// Verify it works by setting and getting a value
	testKey := "provider_test_key"
	testValue := "provider_test_value"

	// Set a value
	err = cache.Set(ctx, testKey, testValue, 0)
	assert.NoError(t, err, "Set operation should not fail")

	// Get the value
	got, err := cache.Get(ctx, testKey)
	assert.NoError(t, err, "Get operation should not fail")
	assert.Equal(t, testValue, got, "Should get the value that was set")

	// Clean up
	err = cache.Delete(ctx, testKey)
	assert.NoError(t, err, "Delete operation should not fail")
}

// TestProviderVariable tests that the Provider variable is correctly initialized
func TestProviderVariable(t *testing.T) {
	assert.NotNil(t, Provider, "Provider should not be nil")
}
