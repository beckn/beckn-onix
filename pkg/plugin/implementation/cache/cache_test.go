package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	return redis.NewIntCmd(ctx, args.Int(0), args.Error(1))
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
	mockClient := new(MockRedisClient)
	ctx := context.Background()
	cache := &Cache{Client: mockClient}

	mockClient.On("Get", ctx, "my-key").Return("my-value", nil)

	value, err := cache.Get(ctx, "my-key")
	assert.NoError(t, err)
	assert.Equal(t, "my-value", value)
	mockClient.AssertExpectations(t)
}

// TestCache_Set tests the Set method of the Cache type
func TestCache_Set(t *testing.T) {
	mockClient := new(MockRedisClient)
	ctx := context.Background()
	cache := &Cache{Client: mockClient}

	mockClient.On("Set", ctx, "my-key", "my-value", time.Minute).Return("OK", nil)

	err := cache.Set(ctx, "my-key", "my-value", time.Minute)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestCache_Delete tests the Delete method of the Cache type
func TestCache_Delete(t *testing.T) {
	mockClient := new(MockRedisClient)
	ctx := context.Background()
	cache := &Cache{Client: mockClient}

	mockClient.On("Del", ctx, []string{"my-key"}).Return(1, nil)

	err := cache.Delete(ctx, "my-key")
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestCache_Clear tests the Clear method of the Cache type
func TestCache_Clear(t *testing.T) {
	mockClient := new(MockRedisClient)
	ctx := context.Background()
	cache := &Cache{Client: mockClient}

	mockClient.On("FlushDB", ctx).Return("OK", nil)

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
