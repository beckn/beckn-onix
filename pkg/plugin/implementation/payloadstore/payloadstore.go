package payloadstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

type store struct {
	cache     definition.Cache
	config    *Config
	namespace string // module name — scopes all cache keys to prevent cross-handler collisions
}

// New creates a PayloadStore backed by the provided Cache.
// namespace should be the module name of the owning handler (e.g. "bap-txn-caller").
func New(ctx context.Context, cache definition.Cache, namespace string, cfg map[string]string) (*store, func() error, error) {
	if cache == nil {
		return nil, nil, fmt.Errorf("payloadstore: cache cannot be nil")
	}
	if namespace == "" {
		return nil, nil, fmt.Errorf("payloadstore: namespace cannot be empty")
	}
	config, err := ParseConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return &store{cache: cache, config: config, namespace: namespace}, func() error { return nil }, nil
}

// Store persists a PayloadEntry and updates the transaction index.
//
// Index race: definition.Cache has no atomic append primitive, so concurrent Store
// calls for the same transaction can race on the index (last writer wins). Individual
// payload:msg:{id} keys are always written correctly; only GetByTransactionID may
// return an incomplete list under concurrent writes for the same transaction.
func (s *store) Store(ctx context.Context, entry definition.PayloadEntry) error {
	if !s.config.StoreBody {
		entry.RequestBody = nil
		entry.ResponseBody = nil
	}
	if !s.config.StoreSignature {
		entry.Signature = ""
	}
	if s.config.MaxBodyBytes > 0 {
		if int64(len(entry.RequestBody)) > s.config.MaxBodyBytes {
			entry.RequestBody = entry.RequestBody[:s.config.MaxBodyBytes]
		}
		if int64(len(entry.ResponseBody)) > s.config.MaxBodyBytes {
			entry.ResponseBody = entry.ResponseBody[:s.config.MaxBodyBytes]
		}
	}

	now := time.Now().UTC()
	entry.StoredAt = now
	entry.ExpiresAt = now.Add(s.config.TTL)

	serialized, err := marshalEntry(entry, s.config.Compress)
	if err != nil {
		return err
	}
	if err := s.cache.Set(ctx, msgKey(s.namespace, entry.MessageID), serialized, s.config.TTL); err != nil {
		return fmt.Errorf("payloadstore: set msg key: %w", err)
	}

	if entry.TransactionID == "" {
		log.Warnf(ctx, "payloadstore: transaction_id absent — skipping transaction index update for message %s", entry.MessageID)
		return nil
	}
	return s.appendToIndex(ctx, entry.TransactionID, entry.MessageID)
}

func (s *store) appendToIndex(ctx context.Context, transactionID, messageID string) error {
	key := txnIndexKey(s.namespace, transactionID)
	raw, err := s.cache.Get(ctx, key)

	var ids []string
	if err == nil && raw != "" {
		if jsonErr := json.Unmarshal([]byte(raw), &ids); jsonErr != nil {
			ids = nil
		}
	}

	for _, id := range ids {
		if id == messageID {
			return nil
		}
	}
	ids = append(ids, messageID)

	data, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("payloadstore: marshal index: %w", err)
	}
	if err := s.cache.Set(ctx, key, string(data), s.config.IndexTTL); err != nil {
		return fmt.Errorf("payloadstore: set index key: %w", err)
	}
	return nil
}

// GetByTransactionID returns all entries for a transaction in insertion (StoredAt ascending) order.
func (s *store) GetByTransactionID(ctx context.Context, transactionID string) ([]definition.PayloadEntry, error) {
	raw, err := s.cache.Get(ctx, txnIndexKey(s.namespace, transactionID))
	if err != nil || raw == "" {
		return nil, nil
	}

	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, nil
	}

	entries := make([]definition.PayloadEntry, 0, len(ids))
	for _, id := range ids {
		entryRaw, err := s.cache.Get(ctx, msgKey(s.namespace, id))
		if err != nil || entryRaw == "" {
			continue // expired or missing — skip silently
		}
		entry, err := unmarshalEntry(entryRaw)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// GetByMessageID returns the entry for the given message ID, optionally filtered by action.
func (s *store) GetByMessageID(ctx context.Context, messageID, action string) (*definition.PayloadEntry, error) {
	raw, err := s.cache.Get(ctx, msgKey(s.namespace, messageID))
	if err != nil || raw == "" {
		return nil, nil
	}

	entry, err := unmarshalEntry(raw)
	if err != nil {
		return nil, err
	}
	if action != "" && entry.Action != action {
		return nil, nil
	}
	return &entry, nil
}

// Exists returns true if a payload with the given message ID is present in the store.
// Errors from the cache are treated as a miss (fail-open).
func (s *store) Exists(ctx context.Context, messageID string) (bool, error) {
	raw, err := s.cache.Get(ctx, msgKey(s.namespace, messageID))
	if err != nil || raw == "" {
		return false, nil
	}
	return true, nil
}
