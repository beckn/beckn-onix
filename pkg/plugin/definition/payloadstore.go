package definition

import (
	"context"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// Outcome records whether a message was acknowledged or rejected.
type Outcome int

const (
	OutcomeUnknown Outcome = iota
	OutcomeACK
	OutcomeNACK
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
	ResponseBody  []byte    // nil when StoreBody: false
	Signature     string    // raw Authorization header; empty when StoreSignature: false
	Outcome       Outcome
	OutcomeReason string
	StoredAt      time.Time
	ExpiresAt     time.Time
}

// PayloadStore persists and retrieves payload entries indexed by message and transaction IDs.
type PayloadStore interface {
	// Store persists an entry. Called by the handler on both ACK and NACK paths.
	Store(ctx context.Context, entry PayloadEntry) error

	// GetByTransactionID returns all entries for a transaction in StoredAt ascending order.
	// Returns an empty slice (not an error) if the transaction is unknown or expired.
	GetByTransactionID(ctx context.Context, transactionID string) ([]PayloadEntry, error)

	// GetByMessageID returns the entry for the given message ID scoped to an action.
	// Returns nil (not an error) if not found or if the action does not match.
	GetByMessageID(ctx context.Context, messageID, action string) (*PayloadEntry, error)

	// Exists is an O(1) check for dedup / replay protection.
	Exists(ctx context.Context, messageID string) (bool, error)
}

// PayloadStoreProvider is the plugin constructor interface.
// namespace should be the module name of the owning handler to scope all cache keys
// and prevent collisions between handlers sharing the same cache backend.
type PayloadStoreProvider interface {
	New(ctx context.Context, cache Cache, namespace string, cfg map[string]string) (PayloadStore, func() error, error)
}
