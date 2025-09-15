package keymanager

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/google/uuid"
	vault "github.com/hashicorp/vault/api"
)

// Config holds configuration parameters for connecting to Vault.
type Config struct {
	VaultAddr string
	KVVersion string
}

// KeyMgr provides methods for managing cryptographic keys using Vault.
type KeyMgr struct {
	VaultClient *vault.Client
	Registry    definition.RegistryLookup
	Cache       definition.Cache
	KvVersion   string
	SecretPath  string
}

var (
	// ErrEmptyKeyID indicates that the provided key ID is empty.
	ErrEmptyKeyID = errors.New("invalid request: keyID cannot be empty")

	// ErrNilKeySet indicates that the provided keyset is nil.
	ErrNilKeySet = errors.New("keyset cannot be nil")

	// ErrEmptySubscriberID indicates that the provided subscriber ID is empty.
	ErrEmptySubscriberID = errors.New("invalid request: subscriberID cannot be empty")

	// ErrEmptyUniqueKeyID indicates that the provided unique key ID is empty.
	ErrEmptyUniqueKeyID = errors.New("invalid request: uniqueKeyID cannot be empty")

	// ErrSubscriberNotFound indicates that no subscriber was found with the provided credentials.
	ErrSubscriberNotFound = errors.New("no subscriber found with given credentials")

	// ErrNilCache indicates that the cache implementation is nil.
	ErrNilCache = errors.New("cache implementation cannot be nil")

	// ErrNilRegistryLookup indicates that the registry lookup implementation is nil.
	ErrNilRegistryLookup = errors.New("registry lookup implementation cannot be nil")
)

// ValidateCfg validates the Vault configuration and sets default KV version if missing.
func ValidateCfg(cfg *Config) error {
	if cfg.VaultAddr == "" {
		return errors.New("invalid config: VaultAddr cannot be empty")
	}
	kvVersion := strings.ToLower(cfg.KVVersion)
	if kvVersion == "" {
		kvVersion = "v1"
	} else if kvVersion != "v1" && kvVersion != "v2" {
		return fmt.Errorf("invalid KVVersion: must be 'v1' or 'v2'")
	}
	cfg.KVVersion = kvVersion
	return nil
}

// getVaultClient is a function that creates a new Vault client.
// This is exported for testing purposes.
var getVaultClient = GetVaultClient

// New creates a new KeyMgr instance with the provided configuration, cache, and registry lookup.
func New(ctx context.Context, cache definition.Cache, registryLookup definition.RegistryLookup, cfg *Config) (*KeyMgr, func() error, error) {
	log.Info(ctx, "Initializing KeyManager plugin")
	// Validate configuration.
	if err := ValidateCfg(cfg); err != nil {
		log.Error(ctx, err, "Invalid configuration for KeyManager")
		return nil, nil, err
	}
	// Check if cache implementation is provided.
	if cache == nil {
		log.Error(ctx, ErrNilCache, "Cache is nil in KeyManager initialization")
		return nil, nil, ErrNilCache
	}

	// Check if registry lookup implementation is provided.
	if registryLookup == nil {
		log.Error(ctx, ErrNilRegistryLookup, "RegistryLookup is nil in KeyManager initialization")
		return nil, nil, ErrNilRegistryLookup
	}

	// Initialize Vault client.
	log.Debugf(ctx, "Creating Vault client with address: %s", cfg.VaultAddr)
	vaultClient, err := getVaultClient(ctx, cfg.VaultAddr)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create Vault client at address: %s", cfg.VaultAddr)
		return nil, nil, fmt.Errorf("failed to create vault client: %w", err)
	}

	log.Info(ctx, "Successfully created Vault client")

	// Create KeyManager instance.
	km := &KeyMgr{
		VaultClient: vaultClient,
		Registry:    registryLookup,
		Cache:       cache,
		KvVersion:   cfg.KVVersion,
	}

	// Cleanup function to release KeyManager resources.
	cleanup := func() error {
		log.Info(ctx, "Cleaning up KeyManager resources")
		km.VaultClient = nil
		km.Cache = nil
		km.Registry = nil
		return nil
	}

	log.Info(ctx, "KeyManager plugin initialized successfully")
	return km, cleanup, nil
}

// NewVaultClient creates a new Vault client instance.
// This function is exported for testing purposes.
var NewVaultClient = vault.NewClient

// GetVaultClient creates and authenticates a Vault client using AppRole.
func GetVaultClient(ctx context.Context, vaultAddr string) (*vault.Client, error) {
	roleID := os.Getenv("VAULT_ROLE_ID")
	secretID := os.Getenv("VAULT_SECRET_ID")

	if roleID == "" || secretID == "" {
		log.Error(ctx, fmt.Errorf("missing credentials"), "VAULT_ROLE_ID or VAULT_SECRET_ID is not set")
		return nil, fmt.Errorf("VAULT_ROLE_ID or VAULT_SECRET_ID is not set")
	}

	config := vault.DefaultConfig()
	config.Address = vaultAddr

	client, err := NewVaultClient(config)
	if err != nil {
		log.Error(ctx, err, "failed to create Vault client")
		return nil, fmt.Errorf("failed to create Vault client: %w", err)
	}

	data := map[string]interface{}{
		"role_id":   roleID,
		"secret_id": secretID,
	}

	log.Info(ctx, "Logging into Vault with AppRole")
	resp, err := client.Logical().Write("auth/approle/login", data)
	if err != nil {
		log.Error(ctx, err, "failed to login with AppRole")
		return nil, fmt.Errorf("failed to login with AppRole: %w", err)
	}
	if resp == nil || resp.Auth == nil {
		log.Error(ctx, nil, "AppRole login failed: no auth info returned")
		return nil, errors.New("AppRole login failed: no auth info returned")
	}

	log.Info(ctx, "Vault login successful")
	client.SetToken(resp.Auth.ClientToken)
	return client, nil
}

var (
	ed25519KeyGenFunc = ed25519.GenerateKey
	x25519KeyGenFunc  = ecdh.X25519().GenerateKey
	uuidGenFunc       = uuid.NewRandom
)

// GenerateKeyset generates a new signing (Ed25519) and encryption (X25519) key pair.
func (km *KeyMgr) GenerateKeyset() (*model.Keyset, error) {
	signingPublic, signingPrivate, err := ed25519KeyGenFunc(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signing key pair: %w", err)
	}

	encrPrivateKey, err := x25519KeyGenFunc(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate encryption key pair: %w", err)
	}
	encrPublicKey := encrPrivateKey.PublicKey().Bytes()
	uuid, err := uuidGenFunc()
	if err != nil {
		return nil, fmt.Errorf("failed to generate unique key id uuid: %w", err)
	}
	return &model.Keyset{
		UniqueKeyID:    uuid.String(),
		SigningPrivate: encodeBase64(signingPrivate.Seed()),
		SigningPublic:  encodeBase64(signingPublic),
		EncrPrivate:    encodeBase64(encrPrivateKey.Bytes()),
		EncrPublic:     encodeBase64(encrPublicKey),
	}, nil
}

// getSecretPath constructs the Vault secret path for storing keys based on the KV version.
func (km *KeyMgr) getSecretPath(keyID string) string {
	if km.KvVersion == "v2" {
		return fmt.Sprintf("secret/data/keys/%s", keyID)
	}
	return fmt.Sprintf("secret/keys/%s", keyID)
}

// InsertKeyset stores the given keyset in Vault under the specified key ID.
func (km *KeyMgr) InsertKeyset(ctx context.Context, keyID string, keys *model.Keyset) error {
	if keyID == "" {
		return ErrEmptyKeyID
	}
	if keys == nil {
		return ErrNilKeySet
	}

	keyData := map[string]interface{}{
		"uniqueKeyID":       keys.UniqueKeyID,
		"signingPublicKey":  keys.SigningPublic,
		"signingPrivateKey": keys.SigningPrivate,
		"encrPublicKey":     keys.EncrPublic,
		"encrPrivateKey":    keys.EncrPrivate,
	}
	path := km.getSecretPath(keyID)
	var payload map[string]interface{}
	if km.KvVersion == "v2" {
		payload = map[string]interface{}{"data": keyData}
	} else {
		payload = keyData
	}

	_, err := km.VaultClient.Logical().Write(path, payload)
	if err != nil {
		return fmt.Errorf("failed to store secret in Vault at path %s: %w", path, err)
	}
	return nil
}

// DeleteKeyset deletes the private keys for the given key ID from Vault.
func (km *KeyMgr) DeleteKeyset(ctx context.Context, keyID string) error {
	if keyID == "" {
		return ErrEmptyKeyID
	}
	path := km.getSecretPath(keyID)
	return km.VaultClient.KVv2(path).Delete(ctx, keyID)
}

// Keyset retrieves the keyset for the given key ID from Vault and public keys from the registry.
func (km *KeyMgr) Keyset(ctx context.Context, keyID string) (*model.Keyset, error) {
	if keyID == "" {
		return nil, ErrEmptyKeyID
	}

	path := km.getSecretPath(keyID)

	secret, err := km.VaultClient.Logical().Read(path)
	if err != nil || secret == nil {
		return nil, fmt.Errorf("failed to read secret from Vault: %w", err)
	}

	var data map[string]interface{}
	if km.KvVersion == "v2" {
		dataRaw, ok := secret.Data["data"]
		if !ok {
			return nil, errors.New("missing 'data' in secret response")
		}
		data, ok = dataRaw.(map[string]interface{})
		if !ok {
			return nil, errors.New("invalid 'data' format in Vault response")
		}
	} else {
		data = secret.Data
	}

	return &model.Keyset{
		UniqueKeyID:    data["uniqueKeyID"].(string),
		SigningPublic:  data["signingPublicKey"].(string),
		SigningPrivate: data["signingPrivateKey"].(string),
		EncrPublic:     data["encrPublicKey"].(string),
		EncrPrivate:    data["encrPrivateKey"].(string),
	}, nil
}

// LookupNPKeys retrieves the signing and encryption public keys for the given subscriber ID and unique key ID.
func (km *KeyMgr) LookupNPKeys(ctx context.Context, subscriberID, uniqueKeyID string) (string, string, error) {
	cacheKey := fmt.Sprintf("%s_%s", subscriberID, uniqueKeyID)
	cachedData, err := km.Cache.Get(ctx, cacheKey)
	if err == nil {
		var keys model.Keyset
		if err := json.Unmarshal([]byte(cachedData), &keys); err == nil {
			return keys.SigningPublic, keys.EncrPublic, nil
		}
	}
	subscribers, err := km.Registry.Lookup(ctx, &model.Subscription{
		Subscriber: model.Subscriber{
			SubscriberID: subscriberID,
		},
		KeyID: uniqueKeyID,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to lookup registry: %w", err)
	}
	if len(subscribers) == 0 {
		return "", "", ErrSubscriberNotFound
	}
	return subscribers[0].SigningPublicKey, subscribers[0].EncrPublicKey, nil
}

// validateParams checks that subscriberID and uniqueKeyID are not empty.
func validateParams(subscriberID, uniqueKeyID string) error {
	if subscriberID == "" {
		return ErrEmptySubscriberID
	}
	if uniqueKeyID == "" {
		return ErrEmptyUniqueKeyID
	}
	return nil
}

// encodeBase64 returns the base64-encoded string of the given data.
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
