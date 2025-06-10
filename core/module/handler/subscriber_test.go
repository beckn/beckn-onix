package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beckn/beckn-onix/core/module/client"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mocks
type mockKeyManager struct {
	keySet                *model.Keyset
	genErr                error
	storeErr              error
	signingPrivateKeyFunc func(ctx context.Context, keyID string) (string, string, error)
	GenerateErr           bool
	StoreFunc             func(ctx context.Context, messageID string, keys *model.Keyset) error
}

func (m *mockKeyManager) SigningPrivateKey(ctx context.Context, keyID string) (string, string, error) {
	return m.signingPrivateKeyFunc(ctx, keyID)
}

func (m *mockKeyManager) EncrPublicKey(ctx context.Context, messageID string, additionalParam string) (string, error) {
	// Mock implementation for EncrPublicKey
	return "mock-encrypted-public-key", nil
}

func (m *mockKeyManager) EncrPrivateKey(ctx context.Context, messageID string) (string, string, error) {
	// Mock implementation for EncrPrivateKey
	return "mock-encrypted-private-key", "mock-additional-value", nil
}

func (m *mockKeyManager) DeletePrivateKeys(ctx context.Context, messageID string) error {
	// Mock implementation for DeletePrivateKeys
	return nil
}
func (m *mockKeyManager) SigningPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error) {
	return "mockKey", nil
}

func (m *mockKeyManager) GenerateKeyPairs() (*model.Keyset, error) {
	if m.GenerateErr {
		return nil, errors.New("key generation failed")
	}
	return &model.Keyset{
		SigningPublic:  "c3VwZXItcHVibGljLXNpZ25pbmc=",
		SigningPrivate: "c3VwZXItcHJpdmF0ZS1zaWduaW5n",
		EncrPublic:     "ZW5jcnlwdC1wdWJsaWM=",
		EncrPrivate:    "ZW5jcnlwdC1wcml2YXRl",
	}, nil
}

func (m *mockKeyManager) StorePrivateKeys(ctx context.Context, keyID string, keys *model.Keyset) error {
	//return nil
	// return m.StoreFunc(ctx, keyID, keys)
	if m.storeErr != nil {
		return m.storeErr
	}
	return nil

}

type mockSigner struct {
	mock.Mock
	Fail      bool
	returnErr bool
	signErr   error
}

func (s *mockSigner) Sign(ctx context.Context, body []byte, privateKey string, createdAt, validTill int64) (string, error) {
	if s.Fail {
		return "", errors.New("signing failed")
	}
	return "signed", nil
}

// KeySet represents a set of keys with a unique identifier.
type KeySet struct {
	UniqueKeyID string
}

type mockRegistryClient struct {
	err        error
	response   map[string]interface{}
	ShouldFail bool
}

func (m *mockRegistryClient) RegistrySubscribe(ctx context.Context, endpoint string, reqBody []byte) (map[string]interface{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

var _ client.RegistryClientInterface = (*mockRegistryClient)(nil)

//////////////////////

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name           string
		body           interface{}
		keyManager     *mockKeyManager
		signer         *mockSigner
		registryClient *mockRegistryClient
		expectStatus   int
	}{

		{
			name:         "Missing SubscriberID",
			body:         model.Subscriber{Type: "bap", Domain: "retail", URL: "https://example.com"},
			keyManager:   &mockKeyManager{},
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "Missing Domain",
			body:         model.Subscriber{SubscriberID: "id", Type: "bap", URL: "https://example.com"},
			keyManager:   &mockKeyManager{},
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "Invalid type",
			body:         model.Subscriber{SubscriberID: "id", Type: "invalid", Domain: "retail", URL: "https://example.com"},
			keyManager:   &mockKeyManager{},
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "Missing Location",
			body:         model.Subscriber{SubscriberID: "id", Type: "bap", Domain: "retail", URL: "https://example.com"},
			keyManager:   &mockKeyManager{},
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "Missing URL",
			body:         model.Subscriber{SubscriberID: "id", Type: "bap", Domain: "retail", Location: map[string]interface{}{"latitude": 18.52, "longitude": 73.85}},
			keyManager:   &mockKeyManager{},
			expectStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqBody []byte
			var err error

			switch v := tt.body.(type) {
			case string:
				reqBody = []byte(v)
			default:
				reqBody, err = json.Marshal(v)
				assert.NoError(t, err)
			}

			req := httptest.NewRequest(http.MethodPost, "/subscribe", bytes.NewReader(reqBody))
			w := httptest.NewRecorder()

			handler := NewSubscribeHandler(context.Background(), tt.keyManager, tt.signer, &client.RegistryClient{}, "subscribe")
			handler.ServeHTTP(w, req)

			assert.Equal(t, tt.expectStatus, w.Code)

			// realClient := *client.NewRegisteryClient(&client.Config{RegisteryURL: "http://fake.url"})

			// registryClient := &testableRegistryClient{
			// 	RegistryClient: realClient,
			// }

			// handler := NewSubscribeHandler(
			// 	context.Background(),
			// 	tt.keyManager,
			// 	tt.signer,
			// 	registryClient.RegistryClient,
			// 	"subscribe",
			// )

			// req := httptest.NewRequest(http.MethodPost, "/subscribe", bytes.NewReader(reqBody))
			// w := httptest.NewRecorder()

			// handler.ServeHTTP(w, req)

			// assert.Equal(t, tt.expectStatus, w.Code)

		})
	}
}

// Test Cases
func TestHandleSubscribe(t *testing.T) {
	tests := []struct {
		name          string
		mockRegistry  mockRegistryClient
		mockKeyMgr    mockKeyManager
		requestBody   model.Subscriber
		expectedCode  int
		expectedError string
		body          interface{}
	}{
		{
			name:         "Key generation failure",
			mockRegistry: mockRegistryClient{},
			mockKeyMgr: mockKeyManager{
				GenerateErr: true,
			},
			requestBody: model.Subscriber{
				SubscriberID: "testSubscriber",
				KeyID:        "testKeyID",
				Type:         "bpp",
				Domain:       "retail",
				URL:          "https://example.com",
				Location: map[string]interface{}{
					"latitude":  18.5204,
					"longitude": 73.8567,
					"address":   "Pune, MH, India",
				},
				KeyValidity: 3600,
			},
			expectedCode:  http.StatusInternalServerError,
			expectedError: "key generation failed",
		},
		{
			name: "Successful subscription",
			mockRegistry: mockRegistryClient{
				ShouldFail: true,
			},
			mockKeyMgr: mockKeyManager{},
			requestBody: model.Subscriber{
				SubscriberID: "validSubscriber",
				KeyID:        "validKeyID",
				Type:         "bpp",
				Domain:       "retail",
				URL:          "https://example.com",
				Location: map[string]interface{}{
					"latitude":  18.5204,
					"longitude": 73.8567,
					"address":   "Pune, MH, India",
				},
				KeyValidity: 86400,
			},
			expectedCode:  http.StatusOK,
			expectedError: "",
		},
		{
			name: "Storing keys fails",
			mockRegistry: mockRegistryClient{
				response: map[string]interface{}{
					"status":     "ACK",
					"message_id": "store-error",
				},
			},
			mockKeyMgr: mockKeyManager{
				storeErr: errors.New("storage failure"),
			},
			requestBody: model.Subscriber{
				SubscriberID: "store-failure",
				KeyID:        "store-keyid",
				Type:         "bpp",
				Domain:       "retail",
				URL:          "https://example.com",
				KeyValidity:  3600,
				Location: map[string]interface{}{
					"latitude":  10.0,
					"longitude": 20.0,
				},
			},
			expectedCode:  http.StatusInternalServerError,
			expectedError: "failed to store keys",
		},
		{
			name:         "Empty message ID",
			mockRegistry: mockRegistryClient{},
			mockKeyMgr:   mockKeyManager{},
			requestBody: model.Subscriber{
				SubscriberID: "empty-id-test",
				KeyID:        "",
				Type:         "bpp",
				Domain:       "retail",
				URL:          "https://example.com",
				KeyValidity:  3600,
				Location: map[string]interface{}{
					"latitude":  10.0,
					"longitude": 20.0,
				},
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: "message ID empty",
		},
		{
			name:          "Invalid JSON/request body",
			body:          `invalid-json`,
			mockKeyMgr:    mockKeyManager{},
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid request body",
		},
		{
			name: "Registry call fails",
			mockRegistry: mockRegistryClient{
				err: errors.New("registry unreachable"),
			},
			mockKeyMgr: mockKeyManager{},
			requestBody: model.Subscriber{
				SubscriberID: "registry-fail",
				KeyID:        "reg-fail-key",
				Type:         "bpp",
				Domain:       "retail",
				URL:          "https://example.com",
				KeyValidity:  3600,
				Location: map[string]interface{}{
					"latitude":  10.0,
					"longitude": 20.0,
				},
			},
			expectedCode:  http.StatusInternalServerError,
			expectedError: "failed to call registry",
		},
		{
			name: "Signing request fails",
			mockRegistry: mockRegistryClient{
				response: map[string]interface{}{
					"status":     "ACK",
					"message_id": "sign-failure",
				},
			},
			mockKeyMgr: mockKeyManager{},
			requestBody: model.Subscriber{
				SubscriberID: "sign-failure-subscriber",
				KeyID:        "sign-failure-key",
				Type:         "bpp",
				Domain:       "retail",
				URL:          "https://example.com",
				KeyValidity:  3600,
				Location: map[string]interface{}{
					"latitude":  10.0,
					"longitude": 20.0,
				},
			},
			expectedCode:  http.StatusInternalServerError,
			expectedError: "failed to sign request",
		},
	}

	// Run Tests
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			signer := &mockSigner{}
			if strings.Contains(tc.name, "Signing request fails") {
				signer.Fail = true
			}

			handler := &SubscribeHandler{
				km:             &tc.mockKeyMgr,
				registryClient: &tc.mockRegistry,
				RegistryURL:    "http://mock.registry/subscribe",
				signer:         signer,
			}

			body, _ := json.Marshal(tc.requestBody)
			if tc.body != nil {
				switch v := tc.body.(type) {
				case string:
					body = []byte(v)
				}
			}

			req := httptest.NewRequest(http.MethodPost, "/subscribe", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.HandleSubscribe(rec, req)

			result := rec.Result()
			defer result.Body.Close()

			assert.Equal(t, tc.expectedCode, result.StatusCode)
			if tc.expectedError != "" {
				assert.Contains(t, rec.Body.String(), tc.expectedError)
			}
		})
	}
}

func TestCreateRegistryRequest(t *testing.T) {
	handler := &SubscribeHandler{}

	// Define test cases
	testCases := []struct {
		name       string
		req        *model.Subscriber
		keySet     *model.Keyset
		messageID  string
		validFrom  time.Time
		validUntil time.Time
		expected   *model.RegistrySubscriptionRequest
	}{
		{
			name: "Valid input",
			req: &model.Subscriber{
				SubscriberID: "sub123",
				Type:         "typeA",
				Domain:       "domain.com",
				Location: map[string]interface{}{
					"latitude":  18.5204,
					"longitude": 73.8567,
					"address":   "Pune, MH, India",
				},
				URL: "https://example.com",
			},
			keySet:     &model.Keyset{UniqueKeyID: "key123"},
			messageID:  "msg123",
			validFrom:  time.Now(),
			validUntil: time.Now().Add(24 * time.Hour),
			expected: &model.RegistrySubscriptionRequest{
				SubscriberID: "sub123",
				Type:         "typeA",
				Domain:       "domain.com",
				Location: map[string]interface{}{
					"latitude":  18.5204,
					"longitude": 73.8567,
					"address":   "Pune, MH, India",
				},
				KeyID:      "key123",
				URL:        "https://example.com",
				ValidFrom:  time.Now().Format(time.RFC3339),
				ValidUntil: time.Now().Add(24 * time.Hour).Format(time.RFC3339),
				MessageID:  "msg123",
			},
		},
		{
			name: "Empty SubscriberID",
			req: &model.Subscriber{
				SubscriberID: "",
				Type:         "typeB",
				Domain:       "example.org",
				Location: map[string]interface{}{
					"latitude":  18.5204,
					"longitude": 73.8567,
					"address":   "Mumbai, MH, India",
				},
				URL: "https://example.org",
			},
			keySet:     &model.Keyset{UniqueKeyID: "key456"},
			messageID:  "msg456",
			validFrom:  time.Now(),
			validUntil: time.Now().Add(48 * time.Hour),
			expected: &model.RegistrySubscriptionRequest{
				SubscriberID: "",
				Type:         "typeB",
				Domain:       "example.org",
				Location: map[string]interface{}{
					"latitude":  18.5204,
					"longitude": 73.8567,
					"address":   "Mumbai, MH, India",
				},
				KeyID:      "key456",
				URL:        "https://example.org",
				ValidFrom:  time.Now().Format(time.RFC3339),
				ValidUntil: time.Now().Add(48 * time.Hour).Format(time.RFC3339),
				MessageID:  "msg456",
			},
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := handler.createRegistryRequest(tc.req, tc.keySet, tc.messageID, tc.validFrom, tc.validUntil)

			assert.Equal(t, tc.expected.SubscriberID, got.SubscriberID)
			assert.Equal(t, tc.expected.Type, got.Type)
			assert.Equal(t, tc.expected.Domain, got.Domain)
			assert.Equal(t, tc.expected.Location, got.Location)

			// Handle nil Keyset case gracefully
			expectedKeyID := ""
			if tc.keySet != nil {
				expectedKeyID = tc.keySet.UniqueKeyID
			}
			assert.Equal(t, expectedKeyID, got.KeyID)

			assert.Equal(t, tc.expected.URL, got.URL)
			assert.Equal(t, tc.expected.MessageID, got.MessageID)
		})
	}
}

func TestSignRequest(t *testing.T) {
	mockSigner := &mockSigner{}
	handler := &SubscribeHandler{signer: mockSigner}

	testCases := []struct {
		name        string
		req         *model.RegistrySubscriptionRequest
		keySet      *model.Keyset
		signerFails bool
		expectErr   bool
		setup       func()
	}{
		{
			name:        "Signer is nil",
			req:         &model.RegistrySubscriptionRequest{},
			keySet:      nil,
			signerFails: false,
			expectErr:   false,
		},
		{
			name:        "SubscriberID is empty",
			req:         &model.RegistrySubscriptionRequest{SubscriberID: ""},
			keySet:      nil,
			signerFails: false,
			expectErr:   false,
		},
		{
			name:        "Signing failure",
			req:         &model.RegistrySubscriptionRequest{SubscriberID: "123"},
			keySet:      &model.Keyset{SigningPrivate: "privateKey"},
			signerFails: true,
			expectErr:   true,
		},
		{
			name:        "Successful case",
			req:         &model.RegistrySubscriptionRequest{SubscriberID: "123"},
			keySet:      &model.Keyset{SigningPrivate: "privateKey"},
			signerFails: false,
			expectErr:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockSigner.Fail = tc.signerFails
			handler.signer = mockSigner

			req, err := handler.signRequest(context.Background(), tc.req, tc.keySet)

			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if req != nil {
				assert.Equal(t, tc.req, req)
			}
		})
	}
}

// ///////////////////////////
// Define mockStep struct
type mockStep struct {
	RunFunc func(ctx *model.StepContext) error
}

// Implement the Run method for mockStep to satisfy the definition.Step interface
func (m *mockStep) Run(ctx *model.StepContext) error {
	// if m.RunFunc != nil {
	// 	return m.RunFunc(ctx)
	// }
	// return nil
	return m.RunFunc(ctx)
}

type mockPluginManager struct {
	failPlugin bool
	failStep   bool
}

// Implement the Validator method to satisfy the PluginManager interface
func (m *mockPluginManager) Validator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error) {
	// Return a mock schema validator and no error
	return &mockSchemaValidator{}, nil
}

// Implement the Middleware method to satisfy the PluginManager interface
func (m *mockPluginManager) Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
	// Return a no-op middleware and no error
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}, nil
}

type mockCache struct{}

// Implement required methods of the definition.Cache interface for mockCache
func (m *mockCache) Clear(ctx context.Context) error {
	return nil
}

// Implement required methods of the definition.Cache interface for mockCache
func (m *mockCache) Get(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *mockCache) Set(ctx context.Context, key string, value string, duration time.Duration) error {
	return nil
}

func (m *mockCache) Delete(ctx context.Context, key string) error {
	return nil
}

func (m *mockPluginManager) Cache(ctx context.Context, cfg *plugin.Config) (definition.Cache, error) {
	if m.failPlugin {
		return nil, fmt.Errorf("cache plugin error")
	}
	return &mockCache{}, nil
}

func (m *mockPluginManager) KeyManager(ctx context.Context, cache definition.Cache, lookup definition.RegistryLookup, cfg *plugin.Config) (definition.KeyManager, error) {
	return &mockKeyManager{}, nil
}

type mockValidator struct{}

// Implement required methods of the definition.SignValidator interface for mockValidator
func (m *mockValidator) ValidateSignature(ctx context.Context, payload, signature, key string) error {
	return nil
}

func (m *mockValidator) Validate(ctx context.Context, payload []byte, signature, key string) error {
	// Add mock validation logic here if needed
	return nil
}

func (m *mockPluginManager) SignValidator(ctx context.Context, cfg *plugin.Config) (definition.SignValidator, error) {
	if m.failPlugin {
		return nil, fmt.Errorf("signvalidator plugin error")
	}
	return &mockValidator{}, nil
}

type mockSchemaValidator struct {
	mock.Mock
}

// Implement required methods of the definition.SchemaValidator interface for mockSchemaValidator
func (m *mockSchemaValidator) Validate(ctx context.Context, schema *url.URL, payload []byte) error {
	// Add mock validation logic here if needed
	args := m.Called(ctx, schema, payload)
	return args.Error(0)
	//return nil
}

func (m *mockPluginManager) SchemaValidator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error) {
	if m.failPlugin {
		return nil, fmt.Errorf("schemavalidator plugin error")
	}
	return &mockSchemaValidator{}, nil
}

func (m *mockPluginManager) Router(ctx context.Context, cfg *plugin.Config) (definition.Router, error) {
	if m.failPlugin {
		return nil, fmt.Errorf("router plugin error")
	}
	return &mockRouter{}, nil
}

type mockRouter struct {
	mock.Mock
}

// Implement required methods of the definition.Router interface for mockRouter
func (m *mockRouter) Route(ctx context.Context, url *url.URL, body []byte) (*model.Route, error) {
	// Mock implementation for the updated signature
	return &model.Route{TargetType: "mock", URL: url}, nil
}

// Define mockPublisher type
type mockPublisher struct {
	publishFunc func(ctx *model.StepContext, publisherID string, body []byte) error
}

// Implement required methods of the definition.Publisher interface for mockPublisher
func (m *mockPublisher) Publish(ctx context.Context, topic string, message []byte) error {
	// Add mock publish logic here if needed
	return nil
}

func (m *mockPluginManager) Publisher(ctx context.Context, cfg *plugin.Config) (definition.Publisher, error) {
	if m.failPlugin {
		return nil, fmt.Errorf("publisher plugin error")
	}
	return &mockPublisher{}, nil

}

// Implement required methods of the definition.Signer interface for mockSigner
// Ensure this return statement is inside a properly defined function or method
func (m *mockPluginManager) MockStep(ctx context.Context) (*mockStep, error) {
	return &mockStep{RunFunc: func(ctx *model.StepContext) error { return nil }}, nil
}

func (m *mockPluginManager) Signer(ctx context.Context, cfg *plugin.Config) (definition.Signer, error) {
	if m.failPlugin {
		return nil, fmt.Errorf("signer plugin error")
	}
	return &mockSigner{}, nil
}

func (m *mockPluginManager) Step(ctx context.Context, cfg *plugin.Config) (definition.Step, error) {
	if m.failStep {
		return nil, fmt.Errorf("step init failed")
	}
	return &mockStep{RunFunc: func(ctx *model.StepContext) error { return nil }}, nil
}

func TestNewStdHandlerSuccess(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *Config
		mgr         PluginManager
		expectError bool
	}{
		{
			name: "All Plugins Loaded",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins:      PluginCfg{},
				Steps:        []string{}, // no steps to init
			},
			mgr:         &mockPluginManager{},
			expectError: false,
		},
		{
			name: "Custom Step From Plugin",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					Steps: []plugin.Config{{ID: "plugin-step"}},
				},
				Steps: []string{"plugin-step"},
			},
			mgr:         &mockPluginManager{},
			expectError: false,
		},
		{
			name: "KeyManager plugin loads successfully",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					KeyManager: &plugin.Config{ID: "keymanager-plugin"},
					Cache:      &plugin.Config{ID: "cache-plugin"},
				},
				Steps: []string{},
			},
			mgr:         &mockPluginManager{},
			expectError: false,
		},
		{
			name: "Cache plugin loads successfully",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					Cache: &plugin.Config{ID: "cache-plugin"},
				},
				Steps: []string{}, // No steps needed
			},
			mgr:         &mockPluginManager{},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewStdHandler(context.Background(), tc.mgr, tc.cfg)
			if (err != nil) != tc.expectError {
				t.Errorf("Expected error: %v, got: %v", tc.expectError, err)
			}
		})
	}
}

func TestNewStdHandlerFailure(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *Config
		mgr         PluginManager
		expectError bool
	}{
		{
			name: "Cache Plugin Load",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					Cache: &plugin.Config{ID: "cache-plugin"},
				},
				Steps: []string{},
			},
			mgr:         &mockPluginManager{failPlugin: true},
			expectError: true,
		},
		{
			name: "KeyManager Plugin Load",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					KeyManager: &plugin.Config{ID: "keymanager-plugin"},
				},
				Steps: []string{},
			},
			mgr:         &mockPluginManager{failPlugin: true},
			expectError: true,
		},
		{
			name: "Publisher Plugin Load",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					Publisher: &plugin.Config{ID: "publisher-plugin"},
				},
				Steps: []string{},
			},
			mgr:         &mockPluginManager{failPlugin: true},
			expectError: true,
		},
		{
			name: " Signer Plugin Load",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					Signer: &plugin.Config{ID: "signer-plugin"},
				},
				Steps: []string{},
			},
			mgr:         &mockPluginManager{failPlugin: true},
			expectError: true,
		},
		{
			name: "Router Plugin Load",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					Router: &plugin.Config{ID: "router-plugin"},
				},
				Steps: []string{},
			},
			mgr:         &mockPluginManager{failPlugin: true},
			expectError: true,
		},
		{
			name: "SchemaValidator Plugin Load",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					SchemaValidator: &plugin.Config{ID: "schemavalidator-plugin"},
				},
				Steps: []string{},
			},
			mgr:         &mockPluginManager{failPlugin: true},
			expectError: true,
		},
		{
			name: "SignValidator Plugin Load",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					SignValidator: &plugin.Config{ID: "signvalidator-plugin"},
				},
				Steps: []string{},
			},
			mgr:         &mockPluginManager{failPlugin: true},
			expectError: true,
		},
		{
			name: "failed to initialize plugin step",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Plugins: PluginCfg{
					Steps: []plugin.Config{
						{ID: "fail-step"}, // required to trigger Step() method
					},
				},
				Steps: []string{"fail-step"},
			},
			mgr:         &mockPluginManager{failStep: true},
			expectError: true,
		},
		{
			name: "unrecognized step",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Steps:        []string{"unknown-step"},
			},
			mgr:         &mockPluginManager{},
			expectError: true,
		},
		{
			name: " Built-in Sign Step Error (missing signer)",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Steps:        []string{"sign"},
			},
			mgr:         &mockPluginManager{},
			expectError: true,
		},
		{
			name: "Built-in ValidateSign Step Error (missing signValidator)",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Steps:        []string{"validateSign"},
			},
			mgr:         &mockPluginManager{},
			expectError: true,
		},
		{
			name: " Built-in ValidateSchema Step Error (missing schemaValidator)",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Steps:        []string{"validateSchema"},
			},
			mgr:         &mockPluginManager{},
			expectError: true,
		},
		{
			name: "Built-in AddRoute Step Error (missing router)",
			cfg: &Config{
				SubscriberID: "test-sub",
				Role:         model.RoleGateway,
				Steps:        []string{"addRoute"},
			},
			mgr:         &mockPluginManager{},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewStdHandler(context.Background(), tc.mgr, tc.cfg)
			if (err != nil) != tc.expectError {
				t.Errorf("Expected error: %v, got: %v", tc.expectError, err)
			}
		})
	}
}

func TestRouteSuccess(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	parsedURL, _ := url.Parse(mockServer.URL)

	// Mock publisher that succeeds
	successPublisher := &mockPublisher{
		publishFunc: func(ctx *model.StepContext, publisherID string, body []byte) error {
			return nil
		},
	}

	tests := []struct {
		name        string
		ctx         *model.StepContext
		publisher   definition.Publisher
		expectError bool
		expectAck   bool // new: to check if SendAck expected
	}{
		{
			name: "Proxying to URL",
			ctx: &model.StepContext{
				Route: &model.Route{
					TargetType: "url",
					URL:        parsedURL,
				},
				Context: context.Background(),
			},
			expectError: false,
			expectAck:   false, // because returns after proxyFunc; SendAck not reached
		},
		{
			name: "Successful publisher send",
			ctx: &model.StepContext{
				Route: &model.Route{
					TargetType:  "publisher",
					PublisherID: "test-pub",
				},
				Context: context.Background(),
				Body:    []byte("test message"),
			},
			publisher:   successPublisher,
			expectError: false,
			expectAck:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("test body")))

			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Test panicked: %v", r)
				}
			}()

			route(tc.ctx, req, recorder, tc.publisher)

			if (recorder.Code >= 400) != tc.expectError {
				t.Errorf("Unexpected response code: %d", recorder.Code)
			}

			if tc.expectAck {
				// Assuming SendAck writes "ACK" to response body — adjust if different
				if !strings.Contains(recorder.Body.String(), "ACK") {
					t.Errorf("Expected ACK in response body but got: %s", recorder.Body.String())
				}
			}
		})
	}
}

func TestRouteFailure(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	parsedURL, _ := url.Parse(mockServer.URL)

	tests := []struct {
		name        string
		ctx         *model.StepContext
		publisher   definition.Publisher
		expectError bool
	}{
		{
			name: "proxying to URL (targetType=url)",
			ctx: &model.StepContext{
				Route: &model.Route{
					TargetType: "url",
					URL:        parsedURL,
				},
			},
			expectError: false,
		},
		{
			name: "publisher plugin not configured",
			ctx: &model.StepContext{
				Route: &model.Route{
					TargetType:  "publisher",
					PublisherID: "test-pub",
				},
				Context: context.Background(), // <- Add this line
				Body:    []byte("test message"),
			},
			publisher:   nil,
			expectError: true,
		},
		{
			name: "unknown route type: invalid-type",
			ctx: &model.StepContext{
				Route: &model.Route{
					TargetType: "invalid-type",
				},
				Context: context.Background(),
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("test body")))

			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Test panicked: %v", r)
				}
			}()

			route(tc.ctx, req, recorder, tc.publisher)

			if tc.expectError && recorder.Code < 400 {
				t.Errorf("Expected error response but got code: %d", recorder.Code)
			}
		})
	}
}

func TestStepCtxSuccess(t *testing.T) {
	tests := []struct {
		name        string
		handler     *stdHandler
		request     *http.Request
		setupCtx    func(r *http.Request) *http.Request
		expectError bool
	}{
		{
			name:    "Valid Request",
			handler: &stdHandler{}, // not using fallback SubscriberID
			request: httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("test body"))),
			setupCtx: func(r *http.Request) *http.Request {
				return r.WithContext(context.WithValue(r.Context(), model.ContextKeySubscriberID, "test-sub"))
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.request
			if tc.setupCtx != nil {
				req = tc.setupCtx(req)
			}

			_, err := tc.handler.stepCtx(req, http.Header{})
			if (err != nil) != tc.expectError {
				t.Errorf("Expected error: %v, got: %v", tc.expectError, err)
			}
		})
	}
}

func TestStepCtxFailure(t *testing.T) {
	tests := []struct {
		name        string
		handler     *stdHandler
		request     *http.Request
		setupCtx    func(r *http.Request) *http.Request
		expectError bool
	}{
		{
			name:        "Subscriber ID Missing",
			handler:     &stdHandler{}, // ensure no fallback
			request:     httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("test body"))),
			setupCtx:    func(r *http.Request) *http.Request { return r }, // no context value
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.request
			if tc.setupCtx != nil {
				req = tc.setupCtx(req)
			}

			_, err := tc.handler.stepCtx(req, http.Header{})
			if (err != nil) != tc.expectError {
				t.Errorf("Expected error: %v, got: %v", tc.expectError, err)
			}
		})
	}
}

func TestServeHTTPSuccess(t *testing.T) {
	tests := []struct {
		name           string
		handler        *stdHandler
		requestBody    string
		injectSubID    bool
		expectStatus   int
		expectedHeader string
	}{
		{
			name: "No route (ACK)",
			handler: &stdHandler{
				SubscriberID: "test-sub",
				steps: []definition.Step{
					&mockStep{
						RunFunc: func(ctx *model.StepContext) error {
							return nil
						},
					},
				},
			},
			requestBody:  `{"test":"value"}`,
			injectSubID:  true,
			expectStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tc.requestBody))

			// Only inject subscriber ID if needed for this test
			if tc.injectSubID {
				ctx := context.WithValue(req.Context(), model.ContextKeySubscriberID, "test-sub")
				req = req.WithContext(ctx)
			}

			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, req)

			if rec.Code != tc.expectStatus {
				t.Errorf("expected status %d, got %d", tc.expectStatus, rec.Code)
			}
		})
	}
}
func TestServeHTTPFailure(t *testing.T) {
	tests := []struct {
		name         string
		handler      *stdHandler
		requestBody  string
		injectSubID  bool
		expectStatus int
	}{
		{
			name: "Failure - step returns error",
			handler: &stdHandler{
				SubscriberID: "test-sub",
				steps: []definition.Step{
					&mockStep{
						RunFunc: func(ctx *model.StepContext) error {
							return fmt.Errorf("step failed")
						},
					},
				},
			},
			requestBody:  `{"test":"value"}`,
			injectSubID:  true,
			expectStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tc.requestBody))
			if tc.injectSubID {
				ctx := context.WithValue(req.Context(), model.ContextKeySubscriberID, "test-sub")
				req = req.WithContext(ctx)
			}
			rec := httptest.NewRecorder()

			tc.handler.ServeHTTP(rec, req)

			if rec.Code != tc.expectStatus {
				t.Errorf("expected status %d, got %d", tc.expectStatus, rec.Code)
			}

			// Relaxed assertion on error body since response.SendNack returns generic message
			body := rec.Body.String()
			if !strings.Contains(body, `"NACK"`) && !strings.Contains(body, "Internal Server Error") {
				t.Errorf("expected body to contain NACK or Internal Server Error, got: %s", body)
			}
		})
	}
}

type mockSignValidator struct {
	mock.Mock
}

func (m *mockSignValidator) Validate(ctx context.Context, body []byte, header string, key string) error {
	args := m.Called(ctx, body, header, key)
	return args.Error(0)
}

// func TestValidateSignStepSuccess(t *testing.T) {
// 	tests := []struct {
// 		name          string
// 		setupMocks    func(validator *mockSignValidator, km *mockKeyManager)
// 		gatewayHdr    string
// 		subscriberHdr string
// 		expectErr     bool
// 		headerValue   string
// 	}{
// 		{
// 			name: "Successful validation of gateway and subscriber",
// 			setupMocks: func(validator *mockSignValidator, km *mockKeyManager) {
// 				// KeyManager returns valid key
// 				km.On("SigningPublicKey", mock.Anything, "sub123", "key1").
// 					Return("mockKey", nil).Twice()

// 				// Validator.Validate must be called with correct key
// 				validator.On("Validate", mock.AnythingOfType("*model.StepContext"), mock.Anything,
// 					"Signature realm=\"sub123\"|key1|signature|extra", "mockKey").
// 					Return(nil).Twice()
// 			},
// 			gatewayHdr:    `Signature realm="sub123"|key1|signature|extra`,
// 			subscriberHdr: `Signature realm="sub123"|key1|signature|extra`,
// 			expectErr:     false,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			validator := new(mockSignValidator)
// 			km := new(mockKeyManager)
// 			tt.setupMocks(validator, km)

// 			step, err := newValidateSignStep(validator, km)
// 			assert.NoError(t, err)

// 			req, _ := http.NewRequest("POST", "http://test.com", nil)
// 			req.Header.Set(model.AuthHeaderGateway, tt.gatewayHdr)
// 			req.Header.Set(model.AuthHeaderSubscriber, tt.subscriberHdr)

// 			ctx := &model.StepContext{
// 				SubID:      "sub123",
// 				Request:    req,
// 				Body:       []byte("testBody"),
// 				RespHeader: http.Header{}, // Initialize this map before use
// 			}

// 			err = step.Run(ctx)

// 			if tt.expectErr {
// 				assert.Error(t, err)
// 			} else {
// 				assert.NoError(t, err)
// 			}
// 		})
// 	}
// }
// func TestValidateSignStepFailure(t *testing.T) {
// 	tests := []struct {
// 		name          string
// 		setupMocks    func(validator *mockSignValidator, km *mockKeyManager)
// 		gatewayHdr    string
// 		subscriberHdr string
// 		expectErr     bool
// 		headerValue   string
// 	}{
// 		{
// 			name:          "Missing Gateway Header",
// 			setupMocks:    func(validator *mockSignValidator, km *mockKeyManager) {},
// 			gatewayHdr:    "",
// 			subscriberHdr: "valid_subscriber_header",
// 			expectErr:     true,
// 		},
// 		{
// 			name: "Invalid Gateway Header",
// 			setupMocks: func(validator *mockSignValidator, km *mockKeyManager) {
// 				validator.On("Validate", mock.Anything, mock.Anything, "invalid_gateway_header", mock.Anything).
// 					Return(errors.New("invalid signature"))
// 			},
// 			gatewayHdr:    "invalid_gateway_header",
// 			subscriberHdr: "valid_subscriber_header",
// 			expectErr:     true,
// 		},
// 		{
// 			name:          "Missing Subscriber Header",
// 			setupMocks:    func(validator *mockSignValidator, km *mockKeyManager) {},
// 			gatewayHdr:    "valid_gateway_header",
// 			subscriberHdr: "",
// 			expectErr:     true,
// 		},
// 		{
// 			name: "Invalid Subscriber Header",
// 			setupMocks: func(validator *mockSignValidator, km *mockKeyManager) {
// 				validator.On("Validate", mock.Anything, mock.Anything, "invalid_subscriber_header", mock.Anything).
// 					Return(errors.New("invalid signature"))
// 			},
// 			gatewayHdr:    "valid_gateway_header",
// 			subscriberHdr: "invalid_subscriber_header",
// 			expectErr:     true,
// 		},
// 		{
// 			name:        "Malformed Signature Header",
// 			headerValue: `Signature keyId="bad|header"`, // Missing required fields
// 			setupMocks:  func(validator *mockSignValidator, km *mockKeyManager) {},
// 			expectErr:   true,
// 		},
// 		{
// 			name: "Validator.Validate returns error",
// 			setupMocks: func(validator *mockSignValidator, km *mockKeyManager) {
// 				km.On("SigningPublicKey", mock.Anything, "sub123", "key1").
// 					Return("mockKey", nil)
// 				validator.On("Validate", mock.Anything, mock.Anything, mock.Anything, "mockKey").
// 					Return(errors.New("validation failed"))
// 			},
// 			gatewayHdr:    `Signature realm="sub123"|key1|signature|extra`,
// 			subscriberHdr: `Signature realm="sub123"|key1|signature|extra`,
// 			expectErr:     true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			validator := new(mockSignValidator)
// 			km := new(mockKeyManager)
// 			tt.setupMocks(validator, km)

// 			step, err := newValidateSignStep(validator, km)
// 			assert.NoError(t, err)

// 			req, _ := http.NewRequest("POST", "http://test.com", nil)
// 			req.Header.Set(model.AuthHeaderGateway, tt.gatewayHdr)
// 			req.Header.Set(model.AuthHeaderSubscriber, tt.subscriberHdr)

// 			ctx := &model.StepContext{
// 				SubID:      "sub123",
// 				Request:    req,
// 				Body:       []byte("testBody"),
// 				RespHeader: http.Header{}, // Initialize this map before use
// 			}

// 			err = step.Run(ctx)

// 			if tt.expectErr {
// 				assert.Error(t, err)
// 			} else {
// 				assert.NoError(t, err)
// 			}
// 		})
// 	}
// }

func TestNewValidateSchemaStepSuccess(t *testing.T) {
	tests := []struct {
		name          string
		validator     definition.SchemaValidator
		expectErr     bool
		expectedError string
	}{
		{
			name:      "SchemaValidator Plugin Configured",
			validator: new(mockSchemaValidator), // Mock validator initialized
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, err := newValidateSchemaStep(tt.validator)

			if tt.expectErr {
				assert.Nil(t, step)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NotNil(t, step)
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewValidateSchemaStepFailure(t *testing.T) {
	tests := []struct {
		name          string
		validator     definition.SchemaValidator
		expectErr     bool
		expectedError string
	}{
		{
			name:          "SchemaValidator Plugin Not Configured",
			validator:     nil, // This should trigger an error
			expectErr:     true,
			expectedError: "invalid config: SchemaValidator plugin not configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, err := newValidateSchemaStep(tt.validator)

			if tt.expectErr {
				assert.Nil(t, step)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NotNil(t, step)
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSchemaStepRunSuccess(t *testing.T) {
	tests := []struct {
		name         string
		setupMocks   func(validator *mockSchemaValidator)
		expectErr    bool
		errorMessage string
	}{
		{
			name: "Schema Validation Passed",
			setupMocks: func(validator *mockSchemaValidator) {
				validator.On("Validate", mock.Anything, mock.Anything, mock.Anything).
					Return(nil)
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := new(mockSchemaValidator)
			tt.setupMocks(validator)

			step := &validateSchemaStep{validator: validator}

			ctx := &model.StepContext{
				Request: &http.Request{URL: &url.URL{Path: "test_url"}},
				Body:    []byte("testBody"),
			}

			err := step.Run(ctx)

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMessage)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSchemaStepRunFailure(t *testing.T) {
	tests := []struct {
		name         string
		setupMocks   func(validator *mockSchemaValidator)
		expectErr    bool
		errorMessage string
	}{
		{
			name: "Schema Validation Error",
			setupMocks: func(validator *mockSchemaValidator) {
				validator.On("Validate", mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("schema validation failed"))
			},
			expectErr:    true,
			errorMessage: "schema validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := new(mockSchemaValidator)
			tt.setupMocks(validator)

			step := &validateSchemaStep{validator: validator}

			ctx := &model.StepContext{
				Request: &http.Request{URL: &url.URL{Path: "test_url"}},
				Body:    []byte("testBody"),
			}

			err := step.Run(ctx)

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMessage)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewAddRouteStepSuccess(t *testing.T) {
	tests := []struct {
		name      string
		router    definition.Router
		expectErr bool
	}{
		{
			name:      "Router Plugin Configured",
			router:    new(mockRouter),
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, err := newAddRouteStep(tt.router)

			if tt.expectErr {
				assert.Nil(t, step)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "Router plugin not configured")
			} else {
				assert.NotNil(t, step)
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewAddRouteStepFailure(t *testing.T) {
	tests := []struct {
		name      string
		router    definition.Router
		expectErr bool
	}{
		{
			name:      "Router Plugin Not Configured",
			router:    nil,
			expectErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, err := newAddRouteStep(tt.router)

			if tt.expectErr {
				assert.Nil(t, step)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "Router plugin not configured")
			} else {
				assert.NotNil(t, step)
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewSignStepSuccess(t *testing.T) {
	tests := []struct {
		name        string
		signer      definition.Signer
		km          definition.KeyManager
		expectError bool
	}{
		{
			name:        "SignerAndKeyManagerPresent",
			signer:      &mockSigner{},
			km:          &mockKeyManager{},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step, err := newSignStep(tc.signer, tc.km)
			if tc.expectError && err == nil {
				t.Errorf("expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.expectError && step == nil {
				t.Errorf("expected step to be non-nil")
			}
		})
	}
}

func TestNewSignStepFailure(t *testing.T) {
	tests := []struct {
		name        string
		signer      definition.Signer
		km          definition.KeyManager
		expectError bool
	}{
		{
			name:        "Invalid config: Signer plugin not configured",
			signer:      nil,
			km:          &mockKeyManager{},
			expectError: true,
		},
		{
			name:        "Invalid config: KeyManager plugin not configured",
			signer:      &mockSigner{},
			km:          nil,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step, err := newSignStep(tc.signer, tc.km)
			if tc.expectError && err == nil {
				t.Errorf("expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.expectError && step == nil {
				t.Errorf("expected step to be non-nil")
			}
		})
	}
}

func TestSignStepRunSuccess(t *testing.T) {
	tests := []struct {
		name           string
		role           model.Role
		keyManagerErr  bool
		signerErr      bool
		expectError    bool
		expectedHeader string
	}{
		{
			name:           "Gateway role",
			role:           model.RoleGateway,
			keyManagerErr:  false,
			signerErr:      false,
			expectError:    false,
			expectedHeader: model.AuthHeaderGateway,
		},
		{
			name:           "Subscriber role",
			role:           "subscriber",
			keyManagerErr:  false,
			signerErr:      false,
			expectError:    false,
			expectedHeader: model.AuthHeaderSubscriber,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			km := &mockKeyManager{
				signingPrivateKeyFunc: func(ctx context.Context, subID string) (string, string, error) {
					if tc.keyManagerErr {
						return "", "", fmt.Errorf("mock key manager error")
					}
					return "mock-key-id", "mock-key", nil
				},
			}

			signer := &mockSigner{
				returnErr: tc.signerErr,
			}

			step := &signStep{
				km:     km,
				signer: signer,
			}

			ctx := &model.StepContext{
				Context: context.Background(),
				Body:    []byte(`{"test":"data"}`),
				SubID:   "test-sub",
				Role:    tc.role,
				Request: &http.Request{Header: http.Header{}},
			}

			err := step.Run(ctx)
			if tc.expectError && err == nil {
				t.Errorf("expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("expected no error but got %v", err)
			}

			if !tc.expectError {
				authHeader := ctx.Request.Header.Get(tc.expectedHeader)
				if authHeader == "" {
					t.Errorf("expected header %s to be set", tc.expectedHeader)
				}
				if !strings.Contains(authHeader, "Signature keyId=") {
					t.Errorf("auth header format incorrect: %s", authHeader)
				}
			}
		})
	}
}

func TestSignStepRunFailure(t *testing.T) {
	tests := []struct {
		name           string
		role           model.Role
		keyManagerErr  bool
		signerErr      bool
		expectError    bool
		expectedHeader string
	}{
		{
			name:          "Failed to get signing key",
			role:          model.RoleGateway,
			keyManagerErr: true,
			signerErr:     false,
			expectError:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			km := &mockKeyManager{
				signingPrivateKeyFunc: func(ctx context.Context, subID string) (string, string, error) {
					if tc.keyManagerErr {
						return "", "", fmt.Errorf("mock key manager error")
					}
					return "mock-key-id", "mock-key", nil
				},
			}

			signer := &mockSigner{
				returnErr: tc.signerErr,
			}

			step := &signStep{
				km:     km,
				signer: signer,
			}

			ctx := &model.StepContext{
				Context: context.Background(),
				Body:    []byte(`{"test":"data"}`),
				SubID:   "test-sub",
				Role:    tc.role,
				Request: &http.Request{Header: http.Header{}},
			}

			err := step.Run(ctx)
			if tc.expectError && err == nil {
				t.Errorf("expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("expected no error but got %v", err)
			}

			if !tc.expectError {
				authHeader := ctx.Request.Header.Get(tc.expectedHeader)
				if authHeader == "" {
					t.Errorf("expected header %s to be set", tc.expectedHeader)
				}
				if !strings.Contains(authHeader, "Signature keyId=") {
					t.Errorf("auth header format incorrect: %s", authHeader)
				}
			}
		})
	}
}
