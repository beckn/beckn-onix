package simplekeymanager

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// Mock implementations for testing
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

type mockRegistry struct{}

func (m *mockRegistry) Lookup(ctx context.Context, sub *model.Subscription) ([]model.Subscription, error) {
	return []model.Subscription{
		{
			Subscriber: model.Subscriber{
				SubscriberID: sub.SubscriberID,
			},
			KeyID:            sub.KeyID,
			SigningPublicKey: "test-signing-public-key",
			EncrPublicKey:    "test-encr-public-key",
		},
	}, nil
}

func TestValidateCfg(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
		},
		{
			name:    "empty config",
			cfg:     &Config{},
			wantErr: false,
		},
		{
			name: "valid config with all keys",
			cfg: &Config{
				NetworkParticipant: "test-np",
				KeyID:              "test-key",
				SigningPrivateKey:  "dGVzdC1zaWduaW5nLXByaXZhdGU=",
				SigningPublicKey:   "dGVzdC1zaWduaW5nLXB1YmxpYw==",
				EncrPrivateKey:     "dGVzdC1lbmNyLXByaXZhdGU=",
				EncrPublicKey:      "dGVzdC1lbmNyLXB1YmxpYw==",
			},
			wantErr: false,
		},
		{
			name: "partial keys - should fail",
			cfg: &Config{
				KeyID:             "test-key",
				SigningPrivateKey: "dGVzdC1zaWduaW5nLXByaXZhdGU=",
				// Missing other keys
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCfg(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCfg() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNew(t *testing.T) {
	ctx := context.Background()
	cache := &mockCache{}
	registry := &mockRegistry{}

	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name:    "valid empty config",
			cfg:     &Config{},
			wantErr: false,
		},
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
		},
		{
			name: "valid config with keys",
			cfg: &Config{
				NetworkParticipant: "test-np",
				KeyID:              "test-key",
				SigningPrivateKey:  "dGVzdC1zaWduaW5nLXByaXZhdGU=",
				SigningPublicKey:   "dGVzdC1zaWduaW5nLXB1YmxpYw==",
				EncrPrivateKey:     "dGVzdC1lbmNyLXByaXZhdGU=",
				EncrPublicKey:      "dGVzdC1lbmNyLXB1YmxpYw==",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skm, cleanup, err := New(ctx, cache, registry, tt.cfg)

			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if skm == nil {
					t.Error("New() returned nil SimpleKeyMgr")
				}
				if cleanup == nil {
					t.Error("New() returned nil cleanup function")
				}
				if cleanup != nil {
					cleanup() // Test cleanup doesn't panic
				}
			}
		})
	}
}

func TestNewWithNilDependencies(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{}

	// Test nil cache
	_, _, err := New(ctx, nil, &mockRegistry{}, cfg)
	if err == nil {
		t.Error("New() should fail with nil cache")
	}

	// Test nil registry
	_, _, err = New(ctx, &mockCache{}, nil, cfg)
	if err == nil {
		t.Error("New() should fail with nil registry")
	}
}

func TestGenerateKeyset(t *testing.T) {
	skm := &SimpleKeyMgr{}

	keyset, err := skm.GenerateKeyset()
	if err != nil {
		t.Errorf("GenerateKeyset() error = %v", err)
		return
	}

	if keyset == nil {
		t.Error("GenerateKeyset() returned nil keyset")
		return
	}

	// Check that all fields are populated
	if keyset.SubscriberID == "" {
		t.Error("GenerateKeyset() SubscriberID is empty")
	}
	if keyset.UniqueKeyID == "" {
		t.Error("GenerateKeyset() UniqueKeyID is empty")
	}
	if keyset.SigningPrivate == "" {
		t.Error("GenerateKeyset() SigningPrivate is empty")
	}
	if keyset.SigningPublic == "" {
		t.Error("GenerateKeyset() SigningPublic is empty")
	}
	if keyset.EncrPrivate == "" {
		t.Error("GenerateKeyset() EncrPrivate is empty")
	}
	if keyset.EncrPublic == "" {
		t.Error("GenerateKeyset() EncrPublic is empty")
	}
}

func TestInsertKeyset(t *testing.T) {
	skm := &SimpleKeyMgr{
		keysets: make(map[string]*model.Keyset),
	}
	ctx := context.Background()

	keyset := &model.Keyset{
		SubscriberID:   "test-np",
		UniqueKeyID:    "test-uuid",
		SigningPrivate: "test-signing-private",
		SigningPublic:  "test-signing-public",
		EncrPrivate:    "test-encr-private",
		EncrPublic:     "test-encr-public",
	}

	// Test successful insertion
	err := skm.InsertKeyset(ctx, "test-key", keyset)
	if err != nil {
		t.Errorf("InsertKeyset() error = %v", err)
	}

	// Verify insertion
	stored, exists := skm.keysets["test-key"]
	if !exists {
		t.Error("InsertKeyset() did not store keyset")
	}
	if stored != keyset {
		t.Error("InsertKeyset() stored different keyset")
	}

	// Test error cases
	err = skm.InsertKeyset(ctx, "", keyset)
	if err == nil {
		t.Error("InsertKeyset() should fail with empty keyID")
	}

	err = skm.InsertKeyset(ctx, "test-key2", nil)
	if err == nil {
		t.Error("InsertKeyset() should fail with nil keyset")
	}
}

func TestKeyset(t *testing.T) {
	originalKeyset := &model.Keyset{
		SubscriberID:   "test-np",
		UniqueKeyID:    "test-uuid",
		SigningPrivate: "test-signing-private",
		SigningPublic:  "test-signing-public",
		EncrPrivate:    "test-encr-private",
		EncrPublic:     "test-encr-public",
	}

	skm := &SimpleKeyMgr{
		keysets: map[string]*model.Keyset{
			"test-key": originalKeyset,
		},
	}
	ctx := context.Background()

	// Test successful retrieval
	keyset, err := skm.Keyset(ctx, "test-key")
	if err != nil {
		t.Errorf("Keyset() error = %v", err)
		return
	}

	if keyset == nil {
		t.Error("Keyset() returned nil")
		return
	}

	// Verify it's a copy, not the same instance
	if keyset == originalKeyset {
		t.Error("Keyset() should return a copy, not original")
	}

	// Verify content is the same
	if keyset.UniqueKeyID != originalKeyset.UniqueKeyID {
		t.Error("Keyset() copy has different UniqueKeyID")
	}

	// Test error cases
	_, err = skm.Keyset(ctx, "")
	if err == nil {
		t.Error("Keyset() should fail with empty keyID")
	}

	_, err = skm.Keyset(ctx, "non-existent")
	if err == nil {
		t.Error("Keyset() should fail with non-existent keyID")
	}
}

func TestDeleteKeyset(t *testing.T) {
	originalKeyset := &model.Keyset{
		UniqueKeyID: "test-uuid",
	}

	skm := &SimpleKeyMgr{
		keysets: map[string]*model.Keyset{
			"test-key": originalKeyset,
		},
	}
	ctx := context.Background()

	// Test successful deletion
	err := skm.DeleteKeyset(ctx, "test-key")
	if err != nil {
		t.Errorf("DeleteKeyset() error = %v", err)
	}

	// Verify deletion
	_, exists := skm.keysets["test-key"]
	if exists {
		t.Error("DeleteKeyset() did not delete keyset")
	}

	// Test error cases
	err = skm.DeleteKeyset(ctx, "")
	if err == nil {
		t.Error("DeleteKeyset() should fail with empty keyID")
	}

	err = skm.DeleteKeyset(ctx, "non-existent")
	if err == nil {
		t.Error("DeleteKeyset() should fail with non-existent keyID")
	}
}

func TestLookupNPKeys(t *testing.T) {
	skm := &SimpleKeyMgr{
		Cache:    &mockCache{},
		Registry: &mockRegistry{},
	}
	ctx := context.Background()

	// Test successful lookup
	signingKey, encrKey, err := skm.LookupNPKeys(ctx, "test-subscriber", "test-key")
	if err != nil {
		t.Errorf("LookupNPKeys() error = %v", err)
		return
	}

	if signingKey == "" {
		t.Error("LookupNPKeys() returned empty signing key")
	}
	if encrKey == "" {
		t.Error("LookupNPKeys() returned empty encryption key")
	}

	// Test error cases
	_, _, err = skm.LookupNPKeys(ctx, "", "test-key")
	if err == nil {
		t.Error("LookupNPKeys() should fail with empty subscriberID")
	}

	_, _, err = skm.LookupNPKeys(ctx, "test-subscriber", "")
	if err == nil {
		t.Error("LookupNPKeys() should fail with empty uniqueKeyID")
	}
}

func TestParseKey(t *testing.T) {
	skm := &SimpleKeyMgr{}

	// Test base64 key
	testData := "hello world"
	base64Data := base64.StdEncoding.EncodeToString([]byte(testData))

	result, err := skm.parseKey(base64Data)
	if err != nil {
		t.Errorf("parseKey() error = %v", err)
		return
	}

	if string(result) != testData {
		t.Errorf("parseKey() = %s, want %s", string(result), testData)
	}

	// Test error cases
	_, err = skm.parseKey("")
	if err == nil {
		t.Error("parseKey() should fail with empty input")
	}

	_, err = skm.parseKey("invalid-base64!")
	if err == nil {
		t.Error("parseKey() should fail with invalid base64")
	}
}

func TestLoadKeysFromConfig(t *testing.T) {
	skm := &SimpleKeyMgr{
		keysets: make(map[string]*model.Keyset),
	}
	ctx := context.Background()

	// Test with valid config
	cfg := &Config{
		NetworkParticipant: "test-np",
		KeyID:              "test-key",
		SigningPrivateKey:  base64.StdEncoding.EncodeToString([]byte("signing-private")),
		SigningPublicKey:   base64.StdEncoding.EncodeToString([]byte("signing-public")),
		EncrPrivateKey:     base64.StdEncoding.EncodeToString([]byte("encr-private")),
		EncrPublicKey:      base64.StdEncoding.EncodeToString([]byte("encr-public")),
	}

	err := skm.loadKeysFromConfig(ctx, cfg)
	if err != nil {
		t.Errorf("loadKeysFromConfig() error = %v", err)
		return
	}

	// Verify keyset was loaded
	_, exists := skm.keysets["test-np"]
	if !exists {
		t.Error("loadKeysFromConfig() did not load keyset")
	}

	// Test with empty config (should not error)
	skm2 := &SimpleKeyMgr{
		keysets: make(map[string]*model.Keyset),
	}
	err = skm2.loadKeysFromConfig(ctx, &Config{})
	if err != nil {
		t.Errorf("loadKeysFromConfig() with empty config error = %v", err)
	}
}
