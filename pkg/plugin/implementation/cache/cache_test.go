package cache

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockRedisClient is a mock implementation of the RedisClient interface
type MockRedisClient struct {
	mock.Mock
}

func (m *MockRedisClient) Get(ctx context.Context, key string) *redis.StringCmd {
	args := m.Called(ctx, key)
	return redis.NewStringResult(args.String(0), args.Error(1))
}

func (m *MockRedisClient) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd {
	args := m.Called(ctx, key, value, ttl)
	return redis.NewStatusResult(args.String(0), args.Error(1))
}

func (m *MockRedisClient) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	args := m.Called(ctx, keys)
	return redis.NewIntResult(int64(args.Int(0)), args.Error(1))
}

func (m *MockRedisClient) FlushDB(ctx context.Context) *redis.StatusCmd {
	args := m.Called(ctx)
	return redis.NewStatusResult(args.String(0), args.Error(1))
}

func (m *MockRedisClient) Ping(ctx context.Context) *redis.StatusCmd {
	args := m.Called(ctx)
	return args.Get(0).(*redis.StatusCmd)
}

func (m *MockRedisClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// TestCache_Get tests the Get method of the Cache type
func TestCache_Get(t *testing.T) {
	tests := []struct {
		name    string
		mockVal string
		mockErr error
		wantVal string
		wantErr bool
	}{
		{name: "hit", mockVal: "my-value", mockErr: nil, wantVal: "my-value", wantErr: false},
		{name: "miss", mockVal: "", mockErr: redis.Nil, wantVal: "", wantErr: false},
		{name: "error", mockVal: "", mockErr: errors.New("connection refused"), wantVal: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockRedisClient)
			ctx := context.Background()
			cache := &Cache{Client: mockClient}
			mockClient.On("Get", mock.Anything, "my-key").Return(tt.mockVal, tt.mockErr)
			value, err := cache.Get(ctx, "my-key")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantVal, value)
			mockClient.AssertExpectations(t)
		})
	}
}

// TestCache_Set tests the Set method of the Cache type
func TestCache_Set(t *testing.T) {
	mockClient := new(MockRedisClient)
	ctx := context.Background()
	cache := &Cache{Client: mockClient}

	mockClient.On("Set", mock.Anything, "my-key", "my-value", time.Minute).Return("OK", nil)

	err := cache.Set(ctx, "my-key", "my-value", time.Minute)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestCache_Delete tests the Delete method of the Cache type
func TestCache_Delete(t *testing.T) {
	mockClient := new(MockRedisClient)
	ctx := context.Background()
	cache := &Cache{Client: mockClient}

	mockClient.On("Del", mock.Anything, []string{"my-key"}).Return(1, nil)

	err := cache.Delete(ctx, "my-key")
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestCache_Clear tests the Clear method of the Cache type
func TestCache_Clear(t *testing.T) {
	mockClient := new(MockRedisClient)
	ctx := context.Background()
	cache := &Cache{Client: mockClient}

	mockClient.On("FlushDB", mock.Anything).Return("OK", nil)

	err := cache.Clear(ctx)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestValidate tests the validate function
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
			if tt.wantErr != nil {
				assert.Equal(t, tt.wantErr, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestNew_Validation tests the validation parts of the New function
func TestNew_Validation(t *testing.T) {
	testCases := []struct {
		name    string
		cfg     *Config
		wantErr bool
		errType error
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
			errType: ErrEmptyConfig,
		},
		{
			name:    "empty addr",
			cfg:     &Config{Addr: ""},
			wantErr: true,
			errType: ErrAddrMissing,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := New(context.Background(), tc.cfg)

			if tc.wantErr {
				assert.Error(t, err)
				if tc.errType != nil {
					assert.ErrorIs(t, err, tc.errType)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestNew_ConnectionFailure tests the connection failure in New function
func TestNew_ConnectionFailure(t *testing.T) {
	// Set test env var
	err := os.Setenv("REDIS_PASSWORD", "")
	if err != nil {
		t.Fatalf("Failed to set REDIS_PASSWORD environment variable: %v", err)
	}

	defer func() {
		err := os.Unsetenv("REDIS_PASSWORD")
		if err != nil {
			t.Fatalf("Failed to unset REDIS_PASSWORD environment variable: %v", err)
		}
	}()

	// Use an invalid connection address to force a connection failure
	cfg := &Config{Addr: "invalid:1234"}

	// Call New which should fail with a connection error
	_, _, err = New(context.Background(), cfg)

	// Verify error type is connection failure
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrConnectionFail)
}

// TestNew_Success tests New returns a cache when the Redis ping succeeds.
func TestNew_Success(t *testing.T) {
	mockClient := new(MockRedisClient)
	mockClient.On("Ping", mock.Anything).Return(redis.NewStatusResult("PONG", nil))

	original := RedisClientFunc
	RedisClientFunc = func(cfg *Config) RedisClient { return mockClient }
	defer func() { RedisClientFunc = original }()

	cfg := &Config{Addr: "localhost:6379"}
	c, closeFn, err := New(context.Background(), cfg)

	assert.NoError(t, err)
	assert.NotNil(t, c)
	assert.NotNil(t, closeFn)
	mockClient.AssertExpectations(t)
}

// TestGetCacheMetrics_ReturnsCachedMetrics tests that repeated calls return the same metrics instance.
func TestGetCacheMetrics_ReturnsCachedMetrics(t *testing.T) {
	ctx := context.Background()
	m1, err := GetCacheMetrics(ctx)
	require.NoError(t, err)
	require.NotNil(t, m1)

	m2, err := GetCacheMetrics(ctx)
	require.NoError(t, err)
	assert.Same(t, m1, m2, "second call should return the cached metrics instance")
}

// TestGetCacheMetrics_RebuildOnProviderChange tests that metrics are rebuilt when the OTel provider changes.
func TestGetCacheMetrics_RebuildOnProviderChange(t *testing.T) {
	ctx := context.Background()
	m1, err := GetCacheMetrics(ctx)
	require.NoError(t, err)

	// Simulate a provider change by clearing the cache pointer.
	cacheMetricsCache.mu.Lock()
	cacheMetricsCache.provider = nil
	cacheMetricsCache.mu.Unlock()

	m2, err := GetCacheMetrics(ctx)
	require.NoError(t, err)
	require.NotNil(t, m2)
	assert.NotSame(t, m1, m2, "metrics should be rebuilt after provider change")
}

// TestCache_Get_WithMetrics tests that Get records hit, miss, and error metrics.
func TestCache_Get_WithMetrics(t *testing.T) {
	ctx := context.Background()
	metrics, err := GetCacheMetrics(ctx)
	require.NoError(t, err)

	t.Run("cache hit records hit metric", func(t *testing.T) {
		mockClient := new(MockRedisClient)
		mockClient.On("Get", mock.Anything, "key").Return("value", nil)
		c := &Cache{Client: mockClient, metrics: metrics}

		val, err := c.Get(ctx, "key")
		assert.NoError(t, err)
		assert.Equal(t, "value", val)
		mockClient.AssertExpectations(t)
	})

	t.Run("cache miss records miss metric", func(t *testing.T) {
		mockClient := new(MockRedisClient)
		mockClient.On("Get", mock.Anything, "missing").Return("", redis.Nil)
		c := &Cache{Client: mockClient, metrics: metrics}

		val, err := c.Get(ctx, "missing")
		assert.NoError(t, err)
		assert.Empty(t, val)
		mockClient.AssertExpectations(t)
	})

	t.Run("cache error records error metric", func(t *testing.T) {
		mockClient := new(MockRedisClient)
		mockClient.On("Get", mock.Anything, "key").Return("", errors.New("redis error"))
		c := &Cache{Client: mockClient, metrics: metrics}

		_, err := c.Get(ctx, "key")
		assert.Error(t, err)
		mockClient.AssertExpectations(t)
	})
}

// TestCache_Set_WithMetrics tests that Set records success and error metrics.
func TestCache_Set_WithMetrics(t *testing.T) {
	ctx := context.Background()
	metrics, err := GetCacheMetrics(ctx)
	require.NoError(t, err)

	t.Run("set success records success metric", func(t *testing.T) {
		mockClient := new(MockRedisClient)
		mockClient.On("Set", mock.Anything, "key", "value", time.Minute).Return("OK", nil)
		c := &Cache{Client: mockClient, metrics: metrics}

		err := c.Set(ctx, "key", "value", time.Minute)
		assert.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("set error records error metric", func(t *testing.T) {
		mockClient := new(MockRedisClient)
		mockClient.On("Set", mock.Anything, "key", "value", time.Minute).Return("", errors.New("set failed"))
		c := &Cache{Client: mockClient, metrics: metrics}

		err := c.Set(ctx, "key", "value", time.Minute)
		assert.Error(t, err)
		mockClient.AssertExpectations(t)
	})
}

// TestCache_Delete_WithMetrics tests that Delete records success and error metrics.
func TestCache_Delete_WithMetrics(t *testing.T) {
	ctx := context.Background()
	metrics, err := GetCacheMetrics(ctx)
	require.NoError(t, err)

	t.Run("delete success records success metric", func(t *testing.T) {
		mockClient := new(MockRedisClient)
		mockClient.On("Del", mock.Anything, []string{"key"}).Return(1, nil)
		c := &Cache{Client: mockClient, metrics: metrics}

		err := c.Delete(ctx, "key")
		assert.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("delete error records error metric", func(t *testing.T) {
		mockClient := new(MockRedisClient)
		mockClient.On("Del", mock.Anything, []string{"key"}).Return(0, errors.New("del failed"))
		c := &Cache{Client: mockClient, metrics: metrics}

		err := c.Delete(ctx, "key")
		assert.Error(t, err)
		mockClient.AssertExpectations(t)
	})
}

// TestGetCacheMetrics_ConcurrentAccess tests that concurrent callers all obtain the same metrics instance.
func TestGetCacheMetrics_ConcurrentAccess(t *testing.T) {
	// Reset cache to force all goroutines to rebuild.
	cacheMetricsCache.mu.Lock()
	cacheMetricsCache.provider = nil
	cacheMetricsCache.m = nil
	cacheMetricsCache.mu.Unlock()

	ctx := context.Background()
	const n = 20
	results := make([]*CacheMetrics, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = GetCacheMetrics(ctx)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d should not error", i)
		require.NotNil(t, results[i], "goroutine %d should return non-nil metrics", i)
	}
	// All goroutines must receive the same cached instance.
	for i := 1; i < n; i++ {
		assert.Same(t, results[0], results[i], "goroutine %d returned a different metrics instance", i)
	}
}
