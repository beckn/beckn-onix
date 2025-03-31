package secretskeymanager

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/google/uuid"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Config Required for the module.
type Config struct {
	ProjectID string
}

type secretMgr interface {
	CreateSecret(context.Context, *secretmanagerpb.CreateSecretRequest, ...gax.CallOption) (*secretmanagerpb.Secret, error)
	AddSecretVersion(context.Context, *secretmanagerpb.AddSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.SecretVersion, error)
	DeleteSecret(context.Context, *secretmanagerpb.DeleteSecretRequest, ...gax.CallOption) error
	AccessSecretVersion(context.Context, *secretmanagerpb.AccessSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error)
	Close() error
}

type keyMgr struct {
	projectID    string
	secretClient secretMgr
	registry     definition.RegistryLookup
	cache        definition.Cache
}

// New method creates a new KeyManager instance.
func New(ctx context.Context, cache definition.Cache, registryLookup definition.RegistryLookup, cfg *Config) (*keyMgr, func() error, error) {
	if err := validateCfg(cfg); err != nil {
		return nil, nil, err
	}

	if cache == nil {
		return nil, nil, ErrNilCache
	}

	if registryLookup == nil {
		return nil, nil, ErrNilRegistryLookup
	}

	secretClient, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create secret manager client: %w", err)
	}

	km := &keyMgr{
		projectID:    cfg.ProjectID,
		secretClient: secretClient,
		registry:     registryLookup,
		cache:        cache,
	}

	return km, km.close, nil
}

// GenerateKeyPairs generates new signing and encryption key pairs.
func (km *keyMgr) GenerateKeyPairs() (*model.Keyset, error) {
	// Generate Signing keys.
	signingPublic, signingPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signing key pair: %w", err)
	}

	// Generate x25519 Keys.
	encrPrivateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate encryption key pair: %w", err)
	}

	encrPublicKey := encrPrivateKey.PublicKey().Bytes()

	// Generate uuid for UniqueKeyID.
	uuid, err := uuid.NewRandom()
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

// StorePrivateKeys stores private keys to the secret manager.
func (km *keyMgr) StorePrivateKeys(ctx context.Context, keyID string, keys *model.Keyset) error {
	if keyID == "" {
		return ErrEmptyKeyID
	}
	if keys == nil {
		return ErrNilKeySet
	}

	secretID := keyID
	secretName := fmt.Sprintf("projects/%s/secrets/%s", km.projectID, keyID)

	// Create secret.
	_, err := km.secretClient.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   fmt.Sprintf("projects/%s", km.projectID),
		SecretId: secretID,
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_Automatic_{
					Automatic: &secretmanagerpb.Replication_Automatic{},
				},
			},
		},
	})

	if err != nil {
		// check for already exists error.
		if status.Code(err) == codes.AlreadyExists {
			// Delete existing secret with same keyID.
			if err := km.DeletePrivateKeys(ctx, keyID); err != nil {
				return fmt.Errorf("failed to delete existing secret with same keyID: %w", err)
			}

			// If deletion is successful we call the function again.
			return km.StorePrivateKeys(ctx, keyID, keys)
		}
		return fmt.Errorf("failed to create secret: %w", err)
	}

	keyData := map[string]string{
		"uniqueKeyID":       keys.UniqueKeyID,
		"signingPrivateKey": keys.SigningPrivate,
		"encrPrivateKey":    keys.EncrPrivate,
	}

	payload, err := json.Marshal(keyData)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Store the secret.
	_, err = km.secretClient.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent:  secretName,
		Payload: &secretmanagerpb.SecretPayload{Data: payload},
	})
	if err != nil {
		return fmt.Errorf("failed to add secret version: %w", err)
	}
	return nil
}

// SigningPrivateKey returns the Signing Private key.
func (km *keyMgr) SigningPrivateKey(ctx context.Context, keyID string) (string, string, error) {
	// Get Private Keys from Secret Manager.
	keys, err := km.getPrivateKeys(ctx, keyID)
	if err != nil {
		return "", "", err
	}

	return keys.UniqueKeyID, keys.SigningPrivate, nil
}

// EncrPrivateKey returns the Encryption Private key.
func (km *keyMgr) EncrPrivateKey(ctx context.Context, keyID string) (string, string, error) {
	// Get Private Keys from Secret Manager.
	keys, err := km.getPrivateKeys(ctx, keyID)
	if err != nil {
		return "", " ", err
	}
	return keys.EncrPrivate, keys.UniqueKeyID, nil
}

// SigningPublicKey returns the Signing Public key.
func (km *keyMgr) SigningPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error) {
	// Getting public key data from cache or registry
	keys, err := km.getPublicKeys(ctx, subscriberID, uniqueKeyID)
	if err != nil {
		return "", err
	}

	return keys.SigningPublic, nil
}

// EncrPublicKey returns the Encryption Public key.
func (km *keyMgr) EncrPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error) {
	keys, err := km.getPublicKeys(ctx, subscriberID, uniqueKeyID)
	if err != nil {
		return "", err
	}

	return keys.EncrPublic, nil
}

// DeletePrivateKeys deletes the private keys from the secret manager.
func (km *keyMgr) DeletePrivateKeys(ctx context.Context, keyID string) error {
	if keyID == "" {
		return ErrEmptyKeyID
	}

	secretID := keyID
	secretName := fmt.Sprintf("projects/%s/secrets/%s", km.projectID, secretID)

	if err := km.secretClient.DeleteSecret(ctx, &secretmanagerpb.DeleteSecretRequest{
		Name: secretName,
	}); err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}
	return nil
}

// Closes the connections.
func (km *keyMgr) close() error {
	return km.secretClient.Close()
}

// Encoding byte data to base64.
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// getPrivateKeys fetches private keys from sercret manager.
func (km *keyMgr) getPrivateKeys(ctx context.Context, keyID string) (*model.Keyset, error) {
	if keyID == "" {
		return nil, ErrEmptyKeyID
	}

	secretID := keyID
	secretName := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", km.projectID, secretID)

	res, err := km.secretClient.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to access secret version: %w", err)
	}

	var keyData map[string]string
	if err := json.Unmarshal(res.Payload.Data, &keyData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	return &model.Keyset{
		UniqueKeyID:    keyData["uniqueKeyID"],
		SigningPrivate: keyData["signingPrivateKey"],
		EncrPrivate:    keyData["encrPrivateKey"],
	}, nil
}

// getPublicKeys fetches public keys from the registry or cache.
func (km *keyMgr) getPublicKeys(ctx context.Context, subscriberID, uniqueKeyID string) (*model.Keyset, error) {
	if err := validateParams(subscriberID, uniqueKeyID); err != nil {
		return nil, err
	}

	// Check if the public keys corresponding to the subscriberID and uniqueKeyID are present in cache or not.
	cacheKey := fmt.Sprintf("%s_%s", subscriberID, uniqueKeyID)

	cachedData, err := km.cache.Get(ctx, cacheKey)
	if err == nil {
		// Cache hit: keys are present in cache,so return the keys.
		var keys model.Keyset
		if err := json.Unmarshal([]byte(cachedData), &keys); err == nil {
			return &keys, nil
		}
	}

	// Cache miss: fetch from registry.
	publicKeys, err := km.lookupRegistry(ctx, subscriberID, uniqueKeyID)
	if err != nil {
		return publicKeys, err
	}

	// Set fetched values in cache.
	cacheValue, err := json.Marshal(publicKeys)
	if err == nil {
		err := km.cache.Set(ctx, cacheKey, string(cacheValue), time.Hour)
		if err != nil {
			// Log
		}
	}

	return publicKeys, nil
}

// lookupRegistry makes the lookup call to registry using registryLookup implementation.
func (km *keyMgr) lookupRegistry(ctx context.Context, subscriberID, uniqueKeyID string) (*model.Keyset, error) {
	subscribers, err := km.registry.Lookup(ctx, &model.Subscription{
		Subscriber: model.Subscriber{
			SubscriberID: subscriberID,
		},
		KeyID: uniqueKeyID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to lookup registry: %w", err)
	}

	if len(subscribers) == 0 {
		return nil, ErrSubscriberNotFound
	}
	return &model.Keyset{
		SigningPublic: subscribers[0].SigningPublicKey,
		EncrPublic:    subscribers[0].EncrPublicKey,
	}, nil
}

// validateCfg validates the config.
func validateCfg(cfg *Config) error {
	if cfg.ProjectID == "" {
		return ErrEmptyProjectID
	}
	return nil
}

func validateParams(subscriberID, uniqueKeyID string) error {
	if subscriberID == "" {
		return ErrEmptySubscriberID
	}
	if uniqueKeyID == "" {
		return ErrEmptyUniqueKeyID
	}
	return nil
}

// Error definitions.
var (
	ErrEmptyProjectID     = errors.New("invalid config: projectID cannot be empty")
	ErrNilCache           = errors.New("cache implementation cannot be nil")
	ErrNilKeySet          = errors.New("keyset cannot be nil")
	ErrNilRegistryLookup  = errors.New("registry lookup implementation cannot be nil")
	ErrEmptySubscriberID  = errors.New("invalid request: subscriberID cannot be empty")
	ErrEmptyUniqueKeyID   = errors.New("invalid request: uniqueKeyID cannot be empty")
	ErrEmptyKeyID         = errors.New("invalid request: keyID cannot be empty")
	ErrSubscriberNotFound = errors.New("no subscriber found with given credentials")
)
