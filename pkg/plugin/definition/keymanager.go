package definition

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// KeyManager defines the interface for key management operations/methods.
type KeyManager interface {
	GenerateKeyset() (*model.Keyset, error)
	InsertKeyset(ctx context.Context, keyID string, keyset *model.Keyset) error
	Keyset(ctx context.Context, keyID string) (*model.Keyset, error)
	LookupNPKeys(ctx context.Context, subscriberID, uniqueKeyID string) (signingPublicKey string, encrPublicKey string, err error)
	DeleteKeyset(ctx context.Context, keyID string) error
}

// KeyManagerProvider initializes a new signer instance.
type KeyManagerProvider interface {
	New(context.Context, Cache, RegistryLookup, map[string]string) (KeyManager, func() error, error)
}
