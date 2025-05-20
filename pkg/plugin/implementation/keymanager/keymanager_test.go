package keymanager

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/google/uuid"
	"github.com/hashicorp/vault/api"
	vault "github.com/hashicorp/vault/api"
)

type mockRegistry struct {
	LookupFunc func(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error)
}

func (m *mockRegistry) Lookup(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
	if m.LookupFunc != nil {
		return m.LookupFunc(ctx, sub)
	}
	return []model.Subscription{
		{
			Subscriber: model.Subscriber{
				SubscriberID: sub.SubscriberID,
				URL:          "https://mock.registry/subscriber",
				Type:         "BPP",
				Domain:       "retail",
			},
			KeyID:            sub.KeyID,
			SigningPublicKey: "mock-signing-public-key",
			EncrPublicKey:    "mock-encryption-public-key",
			ValidFrom:        time.Now().Add(-time.Hour),
			ValidUntil:       time.Now().Add(time.Hour),
			Status:           "SUBSCRIBED",
			Created:          time.Now().Add(-2 * time.Hour),
			Updated:          time.Now(),
			Nonce:            "mock-nonce",
		},
	}, nil
}

type mockCache struct{}

func (m *mockCache) Get(ctx context.Context, key string) (string, error) {
	return "", nil
}
func (m *mockCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return nil
}
func (m *mockCache) Clear(ctx context.Context) error {
	return nil
}

func (m *mockCache) Delete(ctx context.Context, key string) error {
	return nil
}

func TestValidateCfgSuccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		wantKV string
	}{
		{
			name:   "valid config with v1",
			cfg:    &Config{VaultAddr: "http://localhost:8200", KVVersion: "v1"},
			wantKV: "v1",
		},
		{
			name:   "valid config with v2",
			cfg:    &Config{VaultAddr: "http://localhost:8200", KVVersion: "v2"},
			wantKV: "v2",
		},
		{
			name:   "default KV version applied",
			cfg:    &Config{VaultAddr: "http://localhost:8200"},
			wantKV: "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCfg(tt.cfg)
			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
			if tt.cfg.KVVersion != tt.wantKV {
				t.Errorf("expected KVVersion %s, got %s", tt.wantKV, tt.cfg.KVVersion)
			}
		})
	}
}

func TestValidateCfgFailure(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
	}{
		{
			name: "missing Vault address",
			cfg:  &Config{VaultAddr: "", KVVersion: "v1"},
		},
		{
			name: "invalid KV version",
			cfg:  &Config{VaultAddr: "http://localhost:8200", KVVersion: "v3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCfg(tt.cfg)
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestGenerateKeyPairs(t *testing.T) {
	originalEd25519 := ed25519KeyGenFunc
	originalX25519 := x25519KeyGenFunc
	originalUUID := uuidGenFunc

	defer func() {
		ed25519KeyGenFunc = originalEd25519
		x25519KeyGenFunc = originalX25519
		uuidGenFunc = originalUUID
	}()

	tests := []struct {
		name           string
		mockEd25519Err error
		mockX25519Err  error
		mockUUIDErr    error
		expectErr      bool
	}{
		{
			name:      "success case",
			expectErr: false,
		},
		{
			name:           "ed25519 key generation failure",
			mockEd25519Err: errors.New("mock ed25519 failure"),
			expectErr:      true,
		},
		{
			name:          "x25519 key generation failure",
			mockX25519Err: errors.New("mock x25519 failure"),
			expectErr:     true,
		},
		{
			name:        "UUID generation failure",
			mockUUIDErr: errors.New("mock uuid failure"),
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockEd25519Err != nil {
				ed25519KeyGenFunc = func(_ io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
					return nil, nil, tt.mockEd25519Err
				}
			} else {
				ed25519KeyGenFunc = ed25519.GenerateKey
			}

			if tt.mockX25519Err != nil {
				x25519KeyGenFunc = func(_ io.Reader) (*ecdh.PrivateKey, error) {
					return nil, tt.mockX25519Err
				}
			} else {
				x25519KeyGenFunc = ecdh.X25519().GenerateKey
			}

			if tt.mockUUIDErr != nil {
				uuidGenFunc = func() (uuid.UUID, error) {
					return uuid.Nil, tt.mockUUIDErr
				}
			} else {
				uuidGenFunc = uuid.NewRandom
			}

			km := &KeyMgr{}
			keyset, err := km.GenerateKeyPairs()

			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				if keyset != nil {
					t.Errorf("expected nil keyset, got non-nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if keyset == nil {
					t.Fatal("expected keyset, got nil")
				}
				if keyset.SigningPrivate == "" || keyset.SigningPublic == "" || keyset.EncrPrivate == "" || keyset.EncrPublic == "" {
					t.Error("expected all keys to be populated and base64-encoded")
				}
				if keyset.UniqueKeyID == "" {
					t.Error("expected UniqueKeyID to be non-empty")
				}
			}
		})
	}
}

func TestGetVaultClient_Failures(t *testing.T) {
	originalNewVaultClient := NewVaultClient
	defer func() { NewVaultClient = originalNewVaultClient }()

	ctx := context.Background()

	tests := []struct {
		name        string
		roleID      string
		secretID    string
		setupServer func(t *testing.T) *httptest.Server
		expectErr   string
	}{
		{
			name:      "missing credentials",
			roleID:    "",
			secretID:  "",
			expectErr: "VAULT_ROLE_ID or VAULT_SECRET_ID is not set",
		},
		{
			name:     "vault client creation failure",
			roleID:   "test-role",
			secretID: "test-secret",
			setupServer: func(t *testing.T) *httptest.Server {
				NewVaultClient = func(cfg *vault.Config) (*vault.Client, error) {
					return nil, errors.New("mock client creation error")
				}
				return nil
			},
			expectErr: "failed to create Vault client: mock client creation error",
		},
		{
			name:     "AppRole login failure",
			roleID:   "test-role",
			secretID: "test-secret",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "login failed", http.StatusBadRequest)
				}))
			},
			expectErr: "failed to login with AppRole: Error making API request",
		},
		{
			name:     "AppRole login returns nil auth",
			roleID:   "test-role",
			secretID: "test-secret",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Header().Set("Content-Type", "application/json")
					if _, err := io.WriteString(w, `{ "auth": null }`); err != nil {
						t.Fatalf("failed to write response: %v", err)
					}
				}))
			},
			expectErr: "AppRole login failed: no auth info returned",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("VAULT_ROLE_ID", tt.roleID)
			os.Setenv("VAULT_SECRET_ID", tt.secretID)

			var server *httptest.Server
			if tt.setupServer != nil {
				server = tt.setupServer(t)
				if server != nil {
					NewVaultClient = func(cfg *vault.Config) (*vault.Client, error) {
						cfg.Address = server.URL
						return vault.NewClient(cfg)
					}
					defer server.Close()
				}
			}

			client, err := GetVaultClient(ctx, "http://ignored")
			if err == nil || !strings.Contains(err.Error(), tt.expectErr) {
				t.Errorf("expected error to contain '%s', got: %v", tt.expectErr, err)
			}
			if client != nil {
				t.Error("expected client to be nil on failure")
			}
		})
	}
}

func TestGetVaultClient_Success(t *testing.T) {
	originalNewVaultClient := NewVaultClient
	defer func() { NewVaultClient = originalNewVaultClient }()

	ctx := context.Background()

	os.Setenv("VAULT_ROLE_ID", "test-role")
	os.Setenv("VAULT_SECRET_ID", "test-secret")

	// Mock Vault server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/auth/approle/login") {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{
			"auth": {
				"client_token": "mock-token"
			}
		}`); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	NewVaultClient = func(cfg *vault.Config) (*vault.Client, error) {
		cfg.Address = server.URL
		return vault.NewClient(cfg)
	}

	client, err := GetVaultClient(ctx, "http://ignored")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if token := client.Token(); token != "mock-token" {
		t.Errorf("expected token to be 'mock-token', got: %s", token)
	}
}

type mockRegistryLookup struct{}

func (m *mockRegistryLookup) Lookup(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
	return []model.Subscription{
		{
			Subscriber: model.Subscriber{
				SubscriberID: sub.SubscriberID,
				Type:         sub.Type,
			},
			KeyID:            "mock-key-id",
			SigningPublicKey: "mock-signing-pubkey",
			EncrPublicKey:    "mock-encryption-pubkey",
			ValidFrom:        time.Now().Add(-time.Hour),
			ValidUntil:       time.Now().Add(time.Hour),
			Status:           "SUBSCRIBED",
			Created:          time.Now(),
			Updated:          time.Now(),
			Nonce:            "mock-nonce",
		},
	}, nil
}

func TestNewSuccess(t *testing.T) {
	tests := []struct {
		name            string
		cfg             *Config
		cache           definition.Cache
		registry        definition.RegistryLookup
		mockVaultStatus int
		mockVaultBody   string
	}{
		{
			name: "valid config",
			cfg: &Config{
				VaultAddr: "http://dummy",
				KVVersion: "v2",
			},
			cache:           &mockCache{},
			registry:        &mockRegistryLookup{},
			mockVaultStatus: http.StatusOK,
			mockVaultBody:   `{}`,
		},
	}

	originalGetVaultClient := getVaultClient
	defer func() { getVaultClient = originalGetVaultClient }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.mockVaultStatus)
				fmt.Fprint(w, tt.mockVaultBody)
			}))
			defer vaultServer.Close()

			tt.cfg.VaultAddr = vaultServer.URL

			getVaultClient = func(ctx context.Context, addr string) (*vault.Client, error) {
				cfg := vault.DefaultConfig()
				cfg.Address = addr
				return vault.NewClient(cfg)
			}

			ctx := context.Background()
			km, cleanup, err := New(ctx, tt.cache, tt.registry, tt.cfg)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if km == nil {
				t.Fatalf("expected KeyMgr instance, got nil")
			}
			if cleanup == nil {
				t.Fatalf("expected cleanup function, got nil")
			}
			_ = cleanup()
		})
	}
}

func TestNewFailure(t *testing.T) {
	tests := []struct {
		name            string
		cfg             *Config
		cache           definition.Cache
		registry        definition.RegistryLookup
		mockVaultStatus int
		mockVaultBody   string
	}{
		{
			name: "nil cache",
			cfg: &Config{
				VaultAddr: "http://dummy",
				KVVersion: "v2",
			},
			cache:           nil,
			registry:        &mockRegistryLookup{},
			mockVaultStatus: http.StatusOK,
			mockVaultBody:   `{}`,
		},
		{
			name: "nil registry",
			cfg: &Config{
				VaultAddr: "http://dummy",
				KVVersion: "v2",
			},
			cache:           &mockCache{},
			registry:        nil,
			mockVaultStatus: http.StatusOK,
			mockVaultBody:   `{}`,
		},
		{
			name: "invalid config",
			cfg: &Config{
				VaultAddr: "",   // Invalid
				KVVersion: "v3", // Unsupported
			},
			cache:           &mockCache{},
			registry:        &mockRegistryLookup{},
			mockVaultStatus: http.StatusOK,
			mockVaultBody:   `{}`,
		},
		{
			name: "vault client creation failure",
			cfg: &Config{
				VaultAddr: "http://dummy",
				KVVersion: "v2",
			},
			cache:           &mockCache{},
			registry:        &mockRegistryLookup{},
			mockVaultStatus: http.StatusOK,
			mockVaultBody:   `{}`,
		},
	}

	originalGetVaultClient := getVaultClient
	defer func() { getVaultClient = originalGetVaultClient }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.mockVaultStatus)
				fmt.Fprint(w, tt.mockVaultBody)
			}))
			defer vaultServer.Close()

			if tt.cfg != nil {
				tt.cfg.VaultAddr = vaultServer.URL
			}

			getVaultClient = func(ctx context.Context, addr string) (*vault.Client, error) {
				if tt.name == "vault client creation failure" {
					return nil, errors.New("simulated vault client creation error")
				}
				cfg := vault.DefaultConfig()
				cfg.Address = addr
				return vault.NewClient(cfg)
			}

			ctx := context.Background()
			km, cleanup, err := New(ctx, tt.cache, tt.registry, tt.cfg)

			if err == nil {
				t.Error("expected error, got nil")
			}
			if km != nil {
				t.Error("expected KeyMgr to be nil, got non-nil")
			}
			if cleanup != nil {
				t.Error("expected cleanup to be nil, got non-nil")
			}
		})
	}

}

func TestStorePrivateKeysSuccess(t *testing.T) {
	ctx := context.Background()

	keys := &model.Keyset{
		UniqueKeyID:    "uuid",
		SigningPublic:  "signPub",
		SigningPrivate: "signPriv",
		EncrPublic:     "encrPub",
		EncrPrivate:    "encrPriv",
	}

	tests := []struct {
		name      string
		kvVersion string
		keyID     string
		keys      *model.Keyset
	}{
		{
			name:      "success kv v1",
			kvVersion: "v1",
			keyID:     "mykeyid",
			keys:      keys,
		},
		{
			name:      "success kv v2",
			kvVersion: "v2",
			keyID:     "mykeyid",
			keys:      keys,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expectedPath := ""
				if tt.kvVersion == "v2" {
					expectedPath = "/v1/secret/data/keys/" + tt.keyID
				} else {
					expectedPath = "/v1/secret/keys/" + tt.keyID
				}

				if r.URL.Path != expectedPath {
					t.Errorf("unexpected request path: got %s, want %s", r.URL.Path, expectedPath)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				fmt.Fprintln(w, `{"data":{}}`)
			}))
			defer server.Close()

			config := api.DefaultConfig()
			config.Address = server.URL
			client, err := api.NewClient(config)
			if err != nil {
				t.Fatalf("failed to create Vault client: %v", err)
			}

			km := &KeyMgr{
				VaultClient: client,
				KvVersion:   tt.kvVersion,
			}

			err = km.StorePrivateKeys(ctx, tt.keyID, tt.keys)
			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

func TestStorePrivateKeysFailure(t *testing.T) {
	ctx := context.Background()

	keys := &model.Keyset{
		UniqueKeyID:    "uuid",
		SigningPublic:  "signPub",
		SigningPrivate: "signPriv",
		EncrPublic:     "encrPub",
		EncrPrivate:    "encrPriv",
	}

	tests := []struct {
		name        string
		kvVersion   string
		keyID       string
		keys        *model.Keyset
		statusCode  int // for HTTP error simulation
		expectedErr string
	}{
		{
			name:        "empty keyID",
			keyID:       "",
			keys:        keys,
			expectedErr: ErrEmptyKeyID.Error(),
		},
		{
			name:        "nil keys",
			keyID:       "mykeyid",
			keys:        nil,
			expectedErr: ErrNilKeySet.Error(),
		},
		{
			name:        "vault write error",
			kvVersion:   "v1",
			keyID:       "mykeyid",
			keys:        keys,
			statusCode:  500,
			expectedErr: "failed to store secret in Vault: Error making API request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server *httptest.Server
			if tt.statusCode != 0 {
				// Setup test HTTP server to simulate Vault error
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "internal error", tt.statusCode)
				}))
				defer server.Close()
			}

			var client *api.Client
			var err error
			if server != nil {
				config := api.DefaultConfig()
				config.Address = server.URL
				client, err = api.NewClient(config)
				if err != nil {
					t.Fatalf("failed to create Vault client: %v", err)
				}
			} else {
				client = nil
			}

			km := &KeyMgr{
				VaultClient: client,
				KvVersion:   tt.kvVersion,
			}

			err = km.StorePrivateKeys(ctx, tt.keyID, tt.keys)

			if err == nil {
				t.Fatalf("expected error %q but got nil", tt.expectedErr)
			}
			if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("expected error containing %q, got %v", tt.expectedErr, err)
			}
		})
	}
}

func TestDeletePrivateKeys(t *testing.T) {
	tests := []struct {
		name      string
		kvVersion string
		keyID     string
		wantPath  string
		wantErr   error
	}{
		{
			name:      "empty keyID",
			kvVersion: "v1",
			keyID:     "",
			wantErr:   ErrEmptyKeyID,
		},
		{
			name:      "v1 delete",
			kvVersion: "v1",
			keyID:     "key123",
			wantPath:  "/v1/secret/private_keys/key123/data/key123",
			wantErr:   nil,
		},
		{
			name:      "v2 delete",
			kvVersion: "v2",
			keyID:     "key123",
			wantPath:  "/v1/secret/data/private_keys/key123/data/key123",
			wantErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// If empty keyID, no Vault calls, just check error
			if tt.keyID == "" {
				km := &KeyMgr{
					KvVersion:   tt.kvVersion,
					VaultClient: nil,
				}
				err := km.DeletePrivateKeys(context.Background(), tt.keyID)
				if err != tt.wantErr {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					t.Errorf("Expected DELETE method, got %s", r.Method)
				}
				if r.URL.Path != tt.wantPath {
					t.Errorf("Expected path %s, got %s", tt.wantPath, r.URL.Path)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer ts.Close()

			vaultClient, err := NewVaultClient(&vault.Config{Address: ts.URL})
			if err != nil {
				t.Fatalf("failed to create vault client: %v", err)
			}

			km := &KeyMgr{
				KvVersion:   tt.kvVersion,
				VaultClient: vaultClient,
			}

			err = km.DeletePrivateKeys(context.Background(), tt.keyID)
			if err != tt.wantErr {
				t.Errorf("DeletePrivateKeys() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func setupMockVaultServer(t *testing.T, kvVersion, keyID string, success bool) *httptest.Server {
	t.Helper()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that request path is expected
		expectedPathV1 := fmt.Sprintf("/v1/secret/private_keys/%s", keyID)
		expectedPathV2 := fmt.Sprintf("/v1/secret/data/private_keys/%s", keyID)

		if (kvVersion == "v2" && r.URL.Path != expectedPathV2) || (kvVersion != "v2" && r.URL.Path != expectedPathV1) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		if !success {
			// Simulate Vault error or not found
			http.Error(w, `{"errors":["key not found"]}`, http.StatusNotFound)
			return
		}

		// Success response JSON, different for v1 and v2
		if kvVersion == "v2" {
			resp := fmt.Sprintf(`{
				"data": {
					"data": {
						"uniqueKeyID": "%s",
						"signingPublicKey": "sign-pub",
						"signingPrivateKey": "sign-priv",
						"encrPublicKey": "encr-pub",
						"encrPrivateKey": "encr-priv"
					}
				}
			}`, keyID)
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(resp)); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		} else {
			resp := fmt.Sprintf(`{
				"data": {
					"uniqueKeyID": "%s",
					"signingPublicKey": "sign-pub",
					"signingPrivateKey": "sign-priv",
					"encrPublicKey": "encr-pub",
					"encrPrivateKey": "encr-priv"
				}
			}`, keyID)
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(resp)); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		}
	})

	return httptest.NewServer(handler)
}

func TestGetKeysSuccess(t *testing.T) {
	tests := []struct {
		name      string
		kvVersion string
		keyID     string
	}{
		{
			name:      "success with KV v2",
			kvVersion: "v2",
			keyID:     "test-key-v2",
		},
		{
			name:      "success with KV v1",
			kvVersion: "v1",
			keyID:     "test-key-v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupMockVaultServer(t, tt.kvVersion, tt.keyID, true)
			defer ts.Close()

			cfg := vault.DefaultConfig()
			cfg.Address = ts.URL

			client, err := vault.NewClient(cfg)
			if err != nil {
				t.Fatalf("failed to create Vault client: %v", err)
			}

			km := &KeyMgr{
				VaultClient: client,
				KvVersion:   tt.kvVersion,
			}

			keys, err := km.getKeys(context.Background(), tt.keyID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if keys == nil {
				t.Fatalf("expected keys but got nil")
			}
			if keys.UniqueKeyID != tt.keyID {
				t.Errorf("expected UniqueKeyID %q, got %q", tt.keyID, keys.UniqueKeyID)
			}
			if keys.SigningPrivate != "sign-priv" {
				t.Errorf("expected SigningPrivate 'sign-priv', got %q", keys.SigningPrivate)
			}
		})
	}
}

func TestGetKeysFailure(t *testing.T) {
	tests := []struct {
		name      string
		kvVersion string
		keyID     string
		success   bool
	}{
		{
			name:      "failure: vault returns 404 v2",
			kvVersion: "v2",
			keyID:     "missing-key-v2",
			success:   false,
		},
		{
			name:      "failure: vault returns 404 v1",
			kvVersion: "v1",
			keyID:     "missing-key-v1",
			success:   false,
		},
		{
			name:      "failure: empty keyID",
			kvVersion: "v2",
			keyID:     "",
			success:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ts *httptest.Server
			if tt.keyID != "" {
				ts = setupMockVaultServer(t, tt.kvVersion, tt.keyID, tt.success)
				defer ts.Close()
			}

			cfg := vault.DefaultConfig()
			if ts != nil {
				cfg.Address = ts.URL
			} else {
				// For empty keyID case or no mock server, use invalid URL to force error
				cfg.Address = "http://invalid"
			}

			client, err := vault.NewClient(cfg)
			if err != nil {
				t.Fatalf("failed to create Vault client: %v", err)
			}

			km := &KeyMgr{
				VaultClient: client,
				KvVersion:   tt.kvVersion,
			}

			keys, err := km.getKeys(context.Background(), tt.keyID)
			if err == nil {
				t.Fatalf("expected error but got nil")
			}
			if keys != nil {
				t.Fatalf("expected nil keys but got %+v", keys)
			}
		})
	}
}

func TestGetPublicKeysSuccess(t *testing.T) {
	km := &KeyMgr{
		Cache: &mockCache{},
		Registry: &mockRegistry{
			LookupFunc: func(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
				return []model.Subscription{
					{
						Subscriber: model.Subscriber{
							SubscriberID: sub.SubscriberID,
						},
						KeyID:            sub.KeyID,
						SigningPublicKey: "mock-signing-public-key",
						EncrPublicKey:    "mock-encryption-public-key",
					},
				}, nil
			},
		},
	}

	got, err := km.getPublicKeys(context.Background(), "sub-id", "key-id")
	if err != nil {
		t.Fatalf("getPublicKeys() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("getPublicKeys() returned nil Keyset")
	}
	if got.SigningPublic != "mock-signing-public-key" {
		t.Errorf("SigningPublic = %v, want %v", got.SigningPublic, "mock-signing-public-key")
	}
	if got.EncrPublic != "mock-encryption-public-key" {
		t.Errorf("EncrPublic = %v, want %v", got.EncrPublic, "mock-encryption-public-key")
	}
}

func TestGetPublicKeysFailure(t *testing.T) {
	type fields struct {
		cache    definition.Cache
		registry definition.RegistryLookup
	}
	type args struct {
		subscriberID string
		uniqueKeyID  string
	}
	tests := []struct {
		name       string
		fields     fields
		args       args
		errMessage string
	}{
		{
			name: "failure - invalid parameters",
			fields: fields{
				cache:    &mockCache{},
				registry: &mockRegistry{},
			},
			args: args{
				subscriberID: "",
				uniqueKeyID:  "",
			},
			errMessage: "invalid",
		},
		{
			name: "failure - registry lookup fails",
			fields: fields{
				cache: &mockCache{},
				registry: &mockRegistry{
					LookupFunc: func(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
						return nil, fmt.Errorf("registry down")
					},
				},
			},
			args: args{
				subscriberID: "sub-id",
				uniqueKeyID:  "key-id",
			},
			errMessage: "registry down",
		},
		{
			name: "failure - registry returns empty",
			fields: fields{
				cache: &mockCache{},
				registry: &mockRegistry{
					LookupFunc: func(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
						return []model.Subscription{}, nil
					},
				},
			},
			args: args{
				subscriberID: "sub-id",
				uniqueKeyID:  "key-id",
			},
			errMessage: "no subscriber found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			km := &KeyMgr{
				Cache:    tt.fields.cache,
				Registry: tt.fields.registry,
			}
			got, err := km.getPublicKeys(context.Background(), tt.args.subscriberID, tt.args.uniqueKeyID)
			if err == nil {
				t.Errorf("getPublicKeys() expected error but got none, result: %v", got)
				return
			}
			if !strings.Contains(err.Error(), tt.errMessage) {
				t.Errorf("getPublicKeys() error = %v, want error message to contain %v", err.Error(), tt.errMessage)
			}
		})
	}
}

func TestLookupRegistrySuccess(t *testing.T) {
	km := &KeyMgr{
		Registry: &mockRegistry{
			LookupFunc: func(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
				return []model.Subscription{
					{
						Subscriber: model.Subscriber{
							SubscriberID: sub.SubscriberID,
						},
						KeyID:            sub.KeyID,
						SigningPublicKey: "signing-key",
						EncrPublicKey:    "encryption-key",
					},
				}, nil
			},
		},
	}

	got, err := km.lookupRegistry(context.Background(), "test-subscriber", "key123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := &model.Keyset{
		SigningPublic: "signing-key",
		EncrPublic:    "encryption-key",
	}

	if got.SigningPublic != want.SigningPublic {
		t.Errorf("SigningPublic = %v, want %v", got.SigningPublic, want.SigningPublic)
	}
	if got.EncrPublic != want.EncrPublic {
		t.Errorf("EncrPublic = %v, want %v", got.EncrPublic, want.EncrPublic)
	}
}

func TestLookupRegistryFailure(t *testing.T) {
	tests := []struct {
		name       string
		mockLookup func(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error)
		wantErr    error
	}{
		{
			name: "lookup error",
			mockLookup: func(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
				return nil, fmt.Errorf("registry failure")
			},
			wantErr: fmt.Errorf("registry failure"),
		},
		{
			name: "no subscriber found",
			mockLookup: func(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
				return []model.Subscription{}, nil
			},
			wantErr: ErrSubscriberNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			km := &KeyMgr{
				Registry: &mockRegistry{
					LookupFunc: tt.mockLookup,
				},
			}
			got, err := km.lookupRegistry(context.Background(), "some-id", "key-id")
			if err == nil {
				t.Fatalf("expected error, got none")
			}
			if got != nil {
				t.Errorf("expected nil keyset, got %v", got)
			}
		})
	}
}

func TestValidateParamsSuccess(t *testing.T) {
	err := validateParams("someSubscriberID", "someUniqueKeyID")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateParamsFailure(t *testing.T) {
	tests := []struct {
		name         string
		subscriberID string
		uniqueKeyID  string
		wantErr      error
	}{
		{
			name:         "empty subscriberID",
			subscriberID: "",
			uniqueKeyID:  "validKeyID",
			wantErr:      ErrEmptySubscriberID,
		},
		{
			name:         "empty uniqueKeyID",
			subscriberID: "validSubscriberID",
			uniqueKeyID:  "",
			wantErr:      ErrEmptyUniqueKeyID,
		},
		{
			name:         "both empty",
			subscriberID: "",
			uniqueKeyID:  "",
			wantErr:      ErrEmptySubscriberID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateParams(tt.subscriberID, tt.uniqueKeyID)
			if err == nil {
				t.Fatalf("expected error %v but got nil", tt.wantErr)
			}
			if err != tt.wantErr {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}
