package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/beckn/beckn-onix/plugin"
	"github.com/beckn/beckn-onix/plugin/definition"
)

type MockPluginManager struct {
	mock.Mock
}

func (m *MockPluginManager) Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
	args := m.Called(ctx, cfg)
	return args.Get(0).(func(http.Handler) http.Handler), args.Error(1)
}

func (m *MockPluginManager) SignValidator(ctx context.Context, cfg *plugin.Config) (definition.SignValidator, error) {
	args := m.Called(ctx, cfg)
	return args.Get(0).(definition.SignValidator), args.Error(1)
}

func (m *MockPluginManager) Validator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error) {
	args := m.Called(ctx, cfg)
	return args.Get(0).(definition.SchemaValidator), args.Error(1)
}

func (m *MockPluginManager) Publisher(ctx context.Context, cfg *plugin.Config) (definition.Publisher, error) {
	args := m.Called(ctx, cfg)
	return args.Get(0).(definition.Publisher), args.Error(1)
}

func (m *MockPluginManager) Router(ctx context.Context, cfg *plugin.Config) (definition.Router, error) {
	args := m.Called(ctx, cfg)
	return args.Get(0).(definition.Router), args.Error(1)
}

func (m *MockPluginManager) Signer(ctx context.Context, cfg *plugin.Config) (definition.Signer, error) {
	args := m.Called(ctx, cfg)
	return args.Get(0).(definition.Signer), args.Error(1)
}

func (m *MockPluginManager) Step(ctx context.Context, cfg *plugin.Config) (definition.Step, error) {
	args := m.Called(ctx, cfg)
	return args.Get(0).(definition.Step), args.Error(1)
}

func TestNewStdHandler(t *testing.T) {
	ctx := context.Background()
	mockMgr := new(MockPluginManager)
	cfg := &Config{}

	mockMgr.On("SignValidator", ctx, mock.Anything).Return(nil, nil)
	mockMgr.On("Validator", ctx, mock.Anything).Return(nil, nil)

	tests := []struct {
		name    string
		cfg     *Config
		expects error
	}{
		{"Valid config", cfg, nil},
		{"Invalid config", nil, errors.New("failed to initialize plugins")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := NewStdHandler(ctx, mockMgr, tt.cfg)
			if tt.expects != nil {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, h)
			}
		})
	}
}

func TestServeHTTP(t *testing.T) {
	ctx := context.Background()
	mockMgr := new(MockPluginManager)
	cfg := &Config{}
	h, _ := NewStdHandler(ctx, mockMgr, cfg)
	r := httptest.NewRequest(http.MethodPost, "http://example.com", bytes.NewBuffer([]byte("test payload")))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSignStep(t *testing.T) {
	mockSigner := new(MockSigner)
	mockSigner.On("Sign", mock.Anything, mock.Anything, mock.Anything).Return("signature", nil)

	step := SignStep{Signer: mockSigner}
	ctx := &definition.StepContext{Request: httptest.NewRequest(http.MethodPost, "http://example.com", nil)}

	err := step.Run(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "signature", ctx.Request.Header.Get("Signature"))
}

type MockSigner struct {
	mock.Mock
}

func (m *MockSigner) Sign(ctx context.Context, body []byte, key string) (string, error) {
	args := m.Called(ctx, body, key)
	return args.String(0), args.Error(1)
}

type MockSignValidator struct {
	mock.Mock
}

func (m *MockSignValidator) Verify(ctx context.Context, body []byte, signature, key string) (bool, error) {
	args := m.Called(ctx, body, signature, key)
	return args.Bool(0), args.Error(1)
}

type MockSchemaValidator struct {
	mock.Mock
}

func (m *MockSchemaValidator) Validate(ctx context.Context, url *url.URL, body []byte) error {
	args := m.Called(ctx, url, body)
	return args.Error(0)
}

type MockRouter struct {
	mock.Mock
}

func (m *MockRouter) Route(ctx context.Context, url *url.URL, body []byte) (*definition.Route, error) {
	args := m.Called(ctx, url, body)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*definition.Route), args.Error(1)
}

func TestValidateSignStep(t *testing.T) {
	ctx := context.Background()
	mockValidator := new(MockSignValidator)
	step := &validateSignStep{validator: mockValidator}

	request := &http.Request{Header: make(http.Header)}
	request.Header.Set("Signature", "testSignature")
	stepCtx := &definition.StepContext{Context: ctx, Request: request, Body: []byte("test"), SigningKey: "key"}

	mockValidator.On("Verify", ctx, stepCtx.Body, "testSignature", "key").Return(true, nil)
	err := step.Run(stepCtx)
	assert.NoError(t, err)
	mockValidator.AssertExpectations(t)
}

func TestValidateSchemaStep(t *testing.T) {
	ctx := context.Background()
	mockValidator := new(MockSchemaValidator)
	step := &validateSchemaStep{validator: mockValidator}

	testURL, _ := url.Parse("http://example.com")
	stepCtx := &definition.StepContext{Context: ctx, Request: &http.Request{URL: testURL}, Body: []byte("test schema")}

	mockValidator.On("Validate", ctx, testURL, stepCtx.Body).Return(nil)
	err := step.Run(stepCtx)
	assert.NoError(t, err)
	mockValidator.AssertExpectations(t)
}

func TestAddRouteStep(t *testing.T) {
	ctx := context.Background()
	mockRouter := new(MockRouter)
	step := &addRouteStep{router: mockRouter}

	testURL, _ := url.Parse("http://example.com")
	stepCtx := &definition.StepContext{Context: ctx, Request: &http.Request{URL: testURL}, Body: []byte("test route")}
	route := &definition.Route{Type: "url", URL: testURL}
	mockRouter.On("Route", ctx, testURL, stepCtx.Body).Return(route, nil)

	err := step.Run(stepCtx)
	assert.NoError(t, err)
	assert.Equal(t, route, stepCtx.Route)
	mockRouter.AssertExpectations(t)
}
