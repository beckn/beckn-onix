package payloadstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

const (
	defaultTTL          = 24 * time.Hour
	defaultMaxBodyBytes = int64(1 << 20) // 1 MiB

	prefixJSON = "j:"
	prefixGzip = "c:"
)

// Config holds all configuration for the PayloadStore plugin.
// Compress is storage-level gzip applied to RequestBody before writing
// to cache (reduces Redis memory usage). It is independent of HTTP Content-Encoding.
type Config struct {
	TTL            time.Duration
	IndexTTL       time.Duration
	MaxBodyBytes   int64
	StoreBody      bool
	StoreSignature bool
	Compress       bool
}

// ParseConfig parses a map[string]string config into a Config, applying defaults for absent keys.
func ParseConfig(cfg map[string]string) (*Config, error) {
	c := &Config{
		TTL:            defaultTTL,
		MaxBodyBytes:   defaultMaxBodyBytes,
		StoreBody:      true,
		StoreSignature: true,
	}

	if raw := cfg["ttl"]; raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid ttl %q: %w", raw, err)
		}
		c.TTL = d
	}

	if c.TTL <= 0 {
		return nil, fmt.Errorf("payloadstore: ttl must be positive")
	}

	if raw := cfg["indexTTL"]; raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid indexTTL %q: %w", raw, err)
		}
		c.IndexTTL = d
	} else {
		c.IndexTTL = c.TTL + time.Hour
	}

	if c.IndexTTL < c.TTL {
		return nil, fmt.Errorf("payloadstore: indexTTL (%v) must be >= ttl (%v)", c.IndexTTL, c.TTL)
	}

	if raw := cfg["maxBodyBytes"]; raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid maxBodyBytes %q: %w", raw, err)
		}
		if n < 0 {
			return nil, fmt.Errorf("payloadstore: maxBodyBytes must be >= 0, got %d", n)
		}
		c.MaxBodyBytes = n
	}

	if raw := cfg["storeBody"]; raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid storeBody %q: %w", raw, err)
		}
		c.StoreBody = b
	}

	if raw := cfg["storeSignature"]; raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid storeSignature %q: %w", raw, err)
		}
		c.StoreSignature = b
	}

	if raw := cfg["compress"]; raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("payloadstore: invalid compress %q: %w", raw, err)
		}
		c.Compress = b
	}

	return c, nil
}

func msgKey(namespace, messageID string) string {
	return "payload:" + namespace + ":msg:" + messageID
}

func txnIndexKey(namespace, transactionID string) string {
	return "payload:" + namespace + ":txn:" + transactionID + ":index"
}

// marshalEntry serializes a PayloadEntry to a cache-storable string.
// The output is prefixed with a format marker so unmarshalEntry can decode
// it correctly regardless of the current compress config:
//
//	"j:" + plain JSON         (compress=false)
//	"c:" + base64(gzip(JSON)) (compress=true)
func marshalEntry(entry definition.PayloadEntry, compress bool) (string, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("payloadstore: marshal entry: %w", err)
	}
	if !compress {
		return prefixJSON + string(data), nil
	}

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return "", fmt.Errorf("payloadstore: gzip write: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("payloadstore: gzip close: %w", err)
	}
	return prefixGzip + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// unmarshalEntry deserializes a string produced by marshalEntry.
// The format is detected from the prefix ("j:" or "c:"), so entries written
// with compress=false can be read back after switching to compress=true and vice versa.
func unmarshalEntry(raw string) (definition.PayloadEntry, error) {
	var data []byte
	switch {
	case len(raw) > 2 && raw[:2] == prefixGzip:
		decoded, err := base64.StdEncoding.DecodeString(raw[2:])
		if err != nil {
			return definition.PayloadEntry{}, fmt.Errorf("payloadstore: base64 decode: %w", err)
		}
		r, err := gzip.NewReader(bytes.NewReader(decoded))
		if err != nil {
			return definition.PayloadEntry{}, fmt.Errorf("payloadstore: gzip reader: %w", err)
		}
		defer r.Close()
		data, err = io.ReadAll(r)
		if err != nil {
			return definition.PayloadEntry{}, fmt.Errorf("payloadstore: gzip read: %w", err)
		}
	case len(raw) > 2 && raw[:2] == prefixJSON:
		data = []byte(raw[2:])
	default:
		// Legacy entries written before prefix support — treat as plain JSON.
		data = []byte(raw)
	}

	var entry definition.PayloadEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return definition.PayloadEntry{}, fmt.Errorf("payloadstore: unmarshal entry: %w", err)
	}
	return entry, nil
}

// becknCtxFields holds the four Beckn context fields extracted from a request body.
type becknCtxFields struct {
	MessageID     string
	TransactionID string
	NetworkID     string
	Action        string
}

// parseBecknCtx extracts the four context fields from a Beckn request body.
// Both snake_case (message_id) and camelCase (messageId) keys are accepted,
// with snake_case taking priority, matching the reqpreprocessor convention.
func parseBecknCtx(body []byte) becknCtxFields {
	var wrapper struct {
		Context map[string]json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil || len(wrapper.Context) == 0 {
		return becknCtxFields{}
	}
	c := wrapper.Context
	return becknCtxFields{
		MessageID:     firstJSONString(c, "message_id", "messageId"),
		TransactionID: firstJSONString(c, "transaction_id", "transactionId"),
		NetworkID:     firstJSONString(c, "network_id", "networkId"),
		Action:        firstJSONString(c, "action"),
	}
}

// firstJSONString returns the first non-empty string found under any of the given keys.
func firstJSONString(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if raw, ok := m[k]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
		}
	}
	return ""
}

type store struct {
	cache     definition.Cache
	config    *Config
	namespace string // fixed as "onix" — reserved for future configurable namespacing
}

// New creates a PayloadStore backed by the provided Cache.
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

// Store builds a PayloadEntry from the incoming request's StepContext, performs a
// duplicate detection check, then persists the entry and updates the transaction index.
func (s *store) Store(ctx *model.StepContext) error {
	bCtx := parseBecknCtx(ctx.Body)
	if bCtx.MessageID == "" {
		log.Warnf(ctx, "payloadstore: message_id absent from request body — store skipped")
		return nil
	}

	if exists, err := s.Exists(ctx, bCtx.MessageID); err != nil {
		log.Warnf(ctx, "payloadstore.Exists: cache error — duplicate detection skipped: %v", err)
	} else if exists {
		log.Warnf(ctx, "payloadstore: duplicate message_id %s — overwriting existing entry", bCtx.MessageID)
	}

	entry := definition.PayloadEntry{
		MessageID:     bCtx.MessageID,
		TransactionID: bCtx.TransactionID,
		NetworkID:     bCtx.NetworkID,
		Action:        bCtx.Action,
		SubscriberID:  ctx.SubID,
		Role:          ctx.Role,
		RequestBody:   ctx.Body,
		Signature:     ctx.Request.Header.Get(model.AuthHeaderSubscriber),
	}
	return s.persist(ctx, entry)
}

// persist applies config policies (storeBody, storeSignature, maxBodyBytes, compress)
// and writes the entry to the cache, then updates the transaction index.
//
// Index race: definition.Cache has no atomic append primitive, so concurrent persist
// calls for the same transaction can race on the index (last writer wins). Individual
// payload:msg:{id} keys are always written correctly; only GetByTransactionID may
// return an incomplete list under concurrent writes for the same transaction.
func (s *store) persist(ctx context.Context, entry definition.PayloadEntry) error {
	if !s.config.StoreBody {
		entry.RequestBody = nil
	}
	if !s.config.StoreSignature {
		entry.Signature = ""
	}
	if s.config.MaxBodyBytes > 0 && int64(len(entry.RequestBody)) > s.config.MaxBodyBytes {
		entry.RequestBody = entry.RequestBody[:s.config.MaxBodyBytes]
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
// Cache errors are treated as a miss (fail-open) because the cache plugin currently
// leaks redis.Nil (key not found) as an error rather than returning ("", nil).
// Once the cache plugin is fixed, this should propagate real errors. See: #717.
func (s *store) Exists(ctx context.Context, messageID string) (bool, error) {
	raw, err := s.cache.Get(ctx, msgKey(s.namespace, messageID))
	if err != nil || raw == "" {
		return false, nil
	}
	return true, nil
}
