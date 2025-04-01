package definition

import (
	"context"

	"github.com/beckn/beckn-onix/pkg/model"
)

// KeyManager defines the interface for key management operations/methods.
type KeyManager interface {
	GenerateKeyPairs() (*model.Keyset, error)
	StorePrivateKeys(ctx context.Context, keyID string, keys *model.Keyset) error
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
