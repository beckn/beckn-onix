package definition

import (
	"context"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// PayloadEntry is a single stored record for one BECKN message.
type PayloadEntry struct {
	MessageID     string
	TransactionID string
	NetworkID     string
	Action        string
	SubscriberID  string
	Role          model.Role
	RequestBody   []byte    // nil when StoreBody: false
	Signature     string    // raw Authorization header; empty when StoreSignature: false
	StoredAt      time.Time
	ExpiresAt     time.Time
}

// PayloadStore persists and retrieves payload entries indexed by message and transaction IDs.
type PayloadStore interface {
	// Store persists an entry built from the incoming request's StepContext.
	Store(ctx *model.StepContext) error

	// GetByTransactionID returns all entries for a transaction in StoredAt ascending order.
	// Returns nil (not an error) if the transaction is unknown or expired.
	GetByTransactionID(ctx context.Context, transactionID string) ([]PayloadEntry, error)

	// GetByMessageID returns the entry for the given message ID scoped to an action.
	// Returns nil (not an error) if not found or if the action does not match.
	GetByMessageID(ctx context.Context, messageID, action string) (*PayloadEntry, error)

	// Exists is an O(1) check for dedup / replay protection.
	Exists(ctx context.Context, messageID string) (bool, error)
}

// PayloadStoreProvider is the plugin constructor interface.
type PayloadStoreProvider interface {
	New(ctx context.Context, cache Cache, namespace string, cfg map[string]string) (PayloadStore, func() error, error)
}
