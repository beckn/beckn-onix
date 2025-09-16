package simplekeymanager

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/google/uuid"
)

// Config holds configuration parameters for SimpleKeyManager.
type Config struct {
	NetworkParticipant string `yaml:"networkParticipant" json:"networkParticipant"`
	KeyID              string `yaml:"keyId" json:"keyId"`
	SigningPrivateKey  string `yaml:"signingPrivateKey" json:"signingPrivateKey"`
	SigningPublicKey   string `yaml:"signingPublicKey" json:"signingPublicKey"`
	EncrPrivateKey     string `yaml:"encrPrivateKey" json:"encrPrivateKey"`
	EncrPublicKey      string `yaml:"encrPublicKey" json:"encrPublicKey"`
}

// SimpleKeyMgr provides methods for managing cryptographic keys using configuration.
type SimpleKeyMgr struct {
	Registry definition.RegistryLookup
	Cache    definition.Cache
	keysets  map[string]*model.Keyset // In-memory storage for keysets
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

	// ErrKeysetNotFound indicates that the requested keyset was not found.
	ErrKeysetNotFound = errors.New("keyset not found")

	// ErrInvalidConfig indicates that the configuration is invalid.
	ErrInvalidConfig = errors.New("invalid configuration")
)

// ValidateCfg validates the SimpleKeyManager configuration.
func ValidateCfg(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("%w: config cannot be nil", ErrInvalidConfig)
	}

	// But if keys are provided, all must be provided
	hasKeys := cfg.SigningPrivateKey != "" || cfg.SigningPublicKey != "" ||
		cfg.EncrPrivateKey != "" || cfg.EncrPublicKey != "" || cfg.NetworkParticipant != "" ||
		cfg.KeyID != ""

	if hasKeys {
		if cfg.SigningPrivateKey == "" {
			return fmt.Errorf("%w: signingPrivateKey is required when keys are configured", ErrInvalidConfig)
		}
		if cfg.SigningPublicKey == "" {
			return fmt.Errorf("%w: signingPublicKey is required when keys are configured", ErrInvalidConfig)
		}
		if cfg.EncrPrivateKey == "" {
			return fmt.Errorf("%w: encrPrivateKey is required when keys are configured", ErrInvalidConfig)
		}
		if cfg.EncrPublicKey == "" {
			return fmt.Errorf("%w: encrPublicKey is required when keys are configured", ErrInvalidConfig)
		}
		if cfg.NetworkParticipant == "" {
			return fmt.Errorf("%w: networkParticipant is required when keys are configured", ErrInvalidConfig)
		}
		if cfg.KeyID == "" {
			return fmt.Errorf("%w: keyId is required when keys are configured", ErrInvalidConfig)
		}
	}

	return nil
}

var (
	ed25519KeyGenFunc = ed25519.GenerateKey
	x25519KeyGenFunc  = ecdh.X25519().GenerateKey
	uuidGenFunc       = uuid.NewRandom
)

// New creates a new SimpleKeyMgr instance with the provided configuration, cache, and registry lookup.
func New(ctx context.Context, cache definition.Cache, registryLookup definition.RegistryLookup, cfg *Config) (*SimpleKeyMgr, func() error, error) {
	log.Info(ctx, "Initializing SimpleKeyManager plugin")

	// Validate configuration.
	if err := ValidateCfg(cfg); err != nil {
		log.Error(ctx, err, "Invalid configuration for SimpleKeyManager")
		return nil, nil, err
	}

	// Check if cache implementation is provided.
	if cache == nil {
		log.Error(ctx, ErrNilCache, "Cache is nil in SimpleKeyManager initialization")
		return nil, nil, ErrNilCache
	}

	// Check if registry lookup implementation is provided.
	if registryLookup == nil {
		log.Error(ctx, ErrNilRegistryLookup, "RegistryLookup is nil in SimpleKeyManager initialization")
		return nil, nil, ErrNilRegistryLookup
	}

	log.Info(ctx, "Creating SimpleKeyManager instance")

	// Create SimpleKeyManager instance.
	skm := &SimpleKeyMgr{
		Registry: registryLookup,
		Cache:    cache,
		keysets:  make(map[string]*model.Keyset),
	}

	// Try to load keys from configuration if they exist
	if err := skm.loadKeysFromConfig(ctx, cfg); err != nil {
		log.Error(ctx, err, "Failed to load keys from configuration")
		return nil, nil, err
	}

	// Cleanup function to release SimpleKeyManager resources.
	cleanup := func() error {
		log.Info(ctx, "Cleaning up SimpleKeyManager resources")
		skm.Cache = nil
		skm.Registry = nil
		skm.keysets = nil
		return nil
	}

	log.Info(ctx, "SimpleKeyManager plugin initialized successfully")
	return skm, cleanup, nil
}

// GenerateKeyset generates a new signing (Ed25519) and encryption (X25519) key pair.
func (skm *SimpleKeyMgr) GenerateKeyset() (*model.Keyset, error) {
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

// InsertKeyset stores the given keyset in memory under the specified key ID.
func (skm *SimpleKeyMgr) InsertKeyset(ctx context.Context, keyID string, keys *model.Keyset) error {
	if keyID == "" {
		return ErrEmptyKeyID
	}
	if keys == nil {
		return ErrNilKeySet
	}

	log.Debugf(ctx, "Storing keyset for keyID: %s", keyID)
	skm.keysets[keyID] = keys
	log.Debugf(ctx, "Successfully stored keyset for keyID: %s", keyID)
	return nil
}

// DeleteKeyset deletes the keyset for the given key ID from memory.
func (skm *SimpleKeyMgr) DeleteKeyset(ctx context.Context, keyID string) error {
	if keyID == "" {
		return ErrEmptyKeyID
	}

	log.Debugf(ctx, "Deleting keyset for keyID: %s", keyID)
	if _, exists := skm.keysets[keyID]; !exists {
		log.Warnf(ctx, "Keyset not found for keyID: %s", keyID)
		return ErrKeysetNotFound
	}

	delete(skm.keysets, keyID)
	log.Debugf(ctx, "Successfully deleted keyset for keyID: %s", keyID)
	return nil
}

// Keyset retrieves the keyset for the given key ID from memory.
func (skm *SimpleKeyMgr) Keyset(ctx context.Context, keyID string) (*model.Keyset, error) {
	if keyID == "" {
		return nil, ErrEmptyKeyID
	}

	log.Debugf(ctx, "Retrieving keyset for keyID: %s", keyID)
	keyset, exists := skm.keysets[keyID]
	if !exists {
		log.Warnf(ctx, "Keyset not found for keyID: %s", keyID)
		return nil, ErrKeysetNotFound
	}

	// Return a copy to prevent external modifications
	copyKeyset := &model.Keyset{
		SubscriberID:   keyset.SubscriberID,
		UniqueKeyID:    keyset.UniqueKeyID,
		SigningPrivate: keyset.SigningPrivate,
		SigningPublic:  keyset.SigningPublic,
		EncrPrivate:    keyset.EncrPrivate,
		EncrPublic:     keyset.EncrPublic,
	}

	log.Debugf(ctx, "Successfully retrieved keyset for keyID: %s", keyID)
	return copyKeyset, nil
}

// LookupNPKeys retrieves the signing and encryption public keys for the given subscriber ID and unique key ID.
func (skm *SimpleKeyMgr) LookupNPKeys(ctx context.Context, subscriberID, uniqueKeyID string) (string, string, error) {
	if err := validateParams(subscriberID, uniqueKeyID); err != nil {
		return "", "", err
	}

	cacheKey := fmt.Sprintf("%s_%s", subscriberID, uniqueKeyID)
	cachedData, err := skm.Cache.Get(ctx, cacheKey)
	if err == nil {
		var keys model.Keyset
		if err := json.Unmarshal([]byte(cachedData), &keys); err == nil {
			log.Debugf(ctx, "Found cached keys for subscriber: %s, uniqueKeyID: %s", subscriberID, uniqueKeyID)
			return keys.SigningPublic, keys.EncrPublic, nil
		}
	}

	log.Debugf(ctx, "Cache miss, looking up registry for subscriber: %s, uniqueKeyID: %s", subscriberID, uniqueKeyID)
	subscribers, err := skm.Registry.Lookup(ctx, &model.Subscription{
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

	log.Debugf(ctx, "Successfully looked up keys for subscriber: %s, uniqueKeyID: %s", subscriberID, uniqueKeyID)
	return subscribers[0].SigningPublicKey, subscribers[0].EncrPublicKey, nil
}

// loadKeysFromConfig loads keys from configuration if they exist
func (skm *SimpleKeyMgr) loadKeysFromConfig(ctx context.Context, cfg *Config) error {
	// Check if all keys are provided in configuration
	if cfg.SigningPrivateKey != "" && cfg.SigningPublicKey != "" &&
		cfg.EncrPrivateKey != "" && cfg.EncrPublicKey != "" {

		log.Info(ctx, "Loading keys from configuration")

		signingPrivate, err := skm.parseKey(cfg.SigningPrivateKey)
		if err != nil {
			return fmt.Errorf("failed to parse signingPrivateKey: %w", err)
		}

		signingPublic, err := skm.parseKey(cfg.SigningPublicKey)
		if err != nil {
			return fmt.Errorf("failed to parse signingPublicKey: %w", err)
		}

		encrPrivate, err := skm.parseKey(cfg.EncrPrivateKey)
		if err != nil {
			return fmt.Errorf("failed to parse encrPrivateKey: %w", err)
		}

		encrPublic, err := skm.parseKey(cfg.EncrPublicKey)
		if err != nil {
			return fmt.Errorf("failed to parse encrPublicKey: %w", err)
		}

		// Determine keyID - use configured keyId or default to "default"
		networkParticipant := cfg.NetworkParticipant
		keyId := cfg.KeyID

		// Create keyset from configuration
		keyset := &model.Keyset{
			SubscriberID:   networkParticipant,
			UniqueKeyID:    keyId,
			SigningPrivate: encodeBase64(signingPrivate),
			SigningPublic:  encodeBase64(signingPublic),
			EncrPrivate:    encodeBase64(encrPrivate),
			EncrPublic:     encodeBase64(encrPublic),
		}

		// Store the keyset using the keyID
		skm.keysets[networkParticipant] = keyset
		log.Infof(ctx, "Successfully loaded keyset from configuration with keyID: %s", keyId)
	} else {
		log.Debug(ctx, "No keys found in configuration, keyset storage will be empty initially")
	}

	return nil
}

// parseKey auto-detects and parses key data (PEM or base64)
func (skm *SimpleKeyMgr) parseKey(keyData string) ([]byte, error) {
	keyData = strings.TrimSpace(keyData)
	if keyData == "" {
		return nil, fmt.Errorf("key data is empty")
	}

	// Auto-detect format: if starts with "-----BEGIN", it's PEM; otherwise base64
	if strings.HasPrefix(keyData, "-----BEGIN") {
		return skm.parsePEMKey(keyData)
	} else {
		return skm.parseBase64Key(keyData)
	}
}

// parsePEMKey parses PEM encoded key
func (skm *SimpleKeyMgr) parsePEMKey(keyData string) ([]byte, error) {
	block, _ := pem.Decode([]byte(keyData))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM key")
	}

	return block.Bytes, nil
}

// parseBase64Key parses base64 encoded key
func (skm *SimpleKeyMgr) parseBase64Key(keyData string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(keyData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 key: %w", err)
	}

	return decoded, nil
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
