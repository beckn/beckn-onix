package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/cache"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

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

// TestProviderVariable tests that the Provider variable is correctly initialized
func TestProviderVariable(t *testing.T) {
	assert.NotNil(t, Provider, "Provider should not be nil")
}

// mockRedisClient mocks the RedisClient interface from the cache package
type mockRedisClient struct {
	mock.Mock
}

func (m *mockRedisClient) Get(ctx context.Context, key string) *redis.StringCmd {
	args := m.Called(ctx, key)
	cmd := redis.NewStringCmd(ctx)
	cmd.SetVal(args.String(0))
	return cmd
}

func (m *mockRedisClient) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd {
	args := m.Called(ctx, key, value, ttl)
	cmd := redis.NewStatusCmd(ctx)
	cmd.SetVal(args.String(0))
	return cmd
}

func (m *mockRedisClient) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	args := m.Called(ctx, keys)
	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(int64(args.Int(0)))
	return cmd
}

func (m *mockRedisClient) FlushDB(ctx context.Context) *redis.StatusCmd {
	args := m.Called(ctx)
	cmd := redis.NewStatusCmd(ctx)
	cmd.SetVal(args.String(0))
	return cmd
}

func (m *mockRedisClient) Ping(ctx context.Context) *redis.StatusCmd {
	args := m.Called(ctx)
	cmd := redis.NewStatusCmd(ctx)
	cmd.SetVal(args.String(0))
	return cmd
}

func (m *mockRedisClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestProviderIntegration(t *testing.T) {
	// Save original RedisClientFunc and restore after test
	original := cache.RedisClientFunc
	defer func() { cache.RedisClientFunc = original }()

	// Create and assign mock
	mockClient := new(mockRedisClient)
	cache.RedisClientFunc = func(cfg *cache.Config) cache.RedisClient {
		return mockClient
	}

	ctx := context.Background()

	// Expectations for the mock
	mockClient.On("Ping", ctx).Return("PONG")
	mockClient.On("Close").Return(nil)

	// Create the config and convert it into a map[string]string
	config := &cache.Config{
		Addr: "localhost:6379",
	}
	// Convert the *cache.Config to map[string]string
	configMap := map[string]string{
		"addr": config.Addr,
	}

	// Call the plugin provider
	provider := Provider
	c, cleanup, err := provider.New(ctx, configMap)

	// Assertions
	assert.NoError(t, err)
	assert.NotNil(t, c)
	assert.NotNil(t, cleanup)

	// Call cleanup and assert
	err = cleanup()
	assert.NoError(t, err)

	// Verify expectations
	mockClient.AssertExpectations(t)
}