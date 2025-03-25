package definition

import (
	"context"

	"github.com/beckn/beckn-onix/pkg/model"
)

type Keyset struct {
	UniqueKeyID    string
	SigningPrivate string
	SigningPublic  string
	EncrPrivate    string
	EncrPublic     string
}

// KeyManager defines the interface for key management operations/methods.
type KeyManager interface {
	GenerateKeyPairs() (*Keyset, error)
	StorePrivateKeys(ctx context.Context, keyID string, keys *Keyset) error
	SigningPrivateKey(ctx context.Context, keyID string) (string, string, error)
	EncrPrivateKey(ctx context.Context, keyID string) (string, string, error)
	SigningPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error)
	EncrPublicKey(ctx context.Context, subscriberID, uniqueKeyID string) (string, error)
	DeletePrivateKeys(ctx context.Context, keyID string) error
}

// KeyManagerProvider initializes a new signer instance.
type KeyManagerProvider interface {
	New(context.Context, Cache, RegistryLookup, map[string]string) (KeyManager, func() error, error)
}

type RegistryLookup interface {
	Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error)
}
