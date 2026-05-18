# PayloadStore

`payloadstore` records every inbound request payload processed by the handler, indexed by `message_id` and `transaction_id`, with TTL-based expiration via the cache backend.

ONIX has been stateless since inception. PayloadStore is the foundation for all stateful use cases — it makes transaction history available to other plugins without requiring any of them to manage storage themselves.

## Requirements

`payloadstore` requires a `cache` plugin configured in the same handler.

`payloadstore` reads `message_id`, `transaction_id`, `network_id`, and `action` directly from the Beckn `context` object in the request body. Both snake_case (`message_id`) and camelCase (`messageId`) key forms are accepted.

## Behaviour

Add `storePayload` to the handler's `steps` list to control when it fires relative to other steps:

- **Duplicate detection** — if a message with the same `message_id` was already stored, a warning is logged and the request continues. The existing entry is overwritten. The request is not blocked.
- **If `message_id` is absent from the request body**, the store is skipped entirely (warning logged).
- **Only the request is stored** — there is no response body capture.

Place `storePayload` anywhere in the `steps` list. Placing it after `validateSign`, for example, means only signed requests are stored.

## Config

```yaml
payloadStore:
  id: payloadstore
  config:
    ttl: "24h"
    indexTTL: "25h"
    maxBodyBytes: "1048576"
    storeBody: "true"
    storeSignature: "true"
    compress: "false"
```

Supported config keys:

- `ttl`: Per-entry lifetime. Each `payload:msg:{id}` cache key expires after this duration. Default: `24h`.
- `indexTTL`: Transaction index lifetime. Defaults to `ttl + 1h` if absent. Must be >= `ttl` — startup fails if a shorter value is configured. Set it slightly longer than `ttl` so the index outlives the last entry it references.
- `maxBodyBytes`: Maximum bytes stored for `RequestBody`. Bodies exceeding this limit are **truncated** before storage. Set to `"0"` for no limit. Negative values are rejected. Default: `"1048576"` (1 MiB).
- `storeBody`: Whether to persist the request body. Set to `"false"` to store metadata only. Default: `"true"`.
- `storeSignature`: Whether to persist the raw `Authorization` header value as the `Signature` field. Useful for non-repudiation and countersignature validation. Default: `"true"`. **BAP handlers log a startup warning when this is explicitly set to `"false"`**.
- `compress`: Applies gzip compression to stored body bytes before writing to cache, reducing Redis memory usage. This is **storage-level** compression — independent of HTTP `Content-Encoding`. Default: `"false"`.

## Stored fields

Each entry stored under `payload:onix:msg:{messageID}` is a JSON object with the following fields:

| Field | Purpose | When set |
|-------|---------|----------|
| `MessageID` | Duplicate detection and point lookup | Always |
| `TransactionID` | Groups all messages in a transaction | Always |
| `NetworkID` | Identifies which Beckn network the message belongs to | Always |
| `Action` | Beckn action (e.g. `search`, `on_search`) | Always |
| `SubscriberID` | Subscriber that sent the request | Always |
| `Role` | BAP or BPP | Always |
| `RequestBody` | Raw request body bytes | `storeBody: "true"` (default). `nil` when `storeBody: "false"`. Truncated to `maxBodyBytes` if the body exceeds the limit. |
| `Signature` | Raw value of the `Authorization` header | `storeSignature: "true"`. Empty string otherwise. |
| `StoredAt` | UTC timestamp when the entry was written | Always |
| `ExpiresAt` | UTC expiry timestamp (`StoredAt + ttl`) | Always |

When `compress: "true"`, `RequestBody` is gzip-compressed before storage. The serialization format is self-describing (see [Cache key layout](#cache-key-layout)), so entries written with one compression setting can be read back after the setting changes.

## Cache key layout

| Key | Value | TTL |
|-----|-------|-----|
| `payload:onix:msg:{messageID}` | `j:<JSON>` or `c:<base64(gzip(JSON))>` — format detected from prefix at read time | `ttl` |
| `payload:onix:txn:{transactionID}:index` | JSON array of message IDs, oldest-first | `indexTTL` |


`GetByTransactionID` reads the index, fetches each entry individually, and silently skips any that have expired between the index write and read.

**Known limitation**: The transaction index update is a non-atomic read-modify-write over the cache. Two concurrent `Store` calls for the same transaction can race — the last writer wins, potentially dropping one message ID from the index. Individual message keys are always written correctly; only `GetByTransactionID` may return an incomplete list under this race. Individual message lookup via `GetByMessageID` and duplicate detection via `Exists` are unaffected.

## Example handler configuration

```yaml
handler:
  type: std
  role: bap
  plugins:
    cache:
      id: cache
      config:
        addr: localhost:6379
    payloadStore:
      id: payloadstore
      config:
        ttl: "24h"
        indexTTL: "25h"
        maxBodyBytes: "1048576"
        storeBody: "true"
        storeSignature: "true"
        compress: "true"
    signValidator:
      id: signvalidator
    schemaValidator:
      id: schemavalidator
      config:
        schemaDir: ./schemas
    router:
      id: router
      config:
        routingConfig: ./config/routing.yaml
  steps:
    - validateSign
    - storePayload
    - validateSchema
    - addRoute
```

## Interface

Plugins that need transaction history can depend on `definition.PayloadStore`. The interface exposes four methods:

**`Store(ctx *model.StepContext) error`** — Parses Beckn context fields from `ctx.Body`, checks for duplicates (warning only), and persists a `PayloadEntry` to the cache. Sets `StoredAt` and `ExpiresAt` at write time. Respects `storeBody`, `storeSignature`, `maxBodyBytes`, and `compress` config. Also appends the message ID to the transaction index. Returns an error if the cache write fails.

**`Exists(ctx, messageID) (bool, error)`** — O(1) check for whether a message has been seen. Returns `true` if a matching entry exists, `false` if not. Cache errors are treated as a miss (fail-open).

**`GetByMessageID(ctx, messageID, action) (*PayloadEntry, error)`** — Returns the stored entry for a message ID. If `action` is non-empty, returns `nil` when the stored entry's action does not match. Returns `nil, nil` (not an error) on a cache miss.

**`GetByTransactionID(ctx, transactionID) ([]PayloadEntry, error)`** — Returns all entries for a transaction in `StoredAt` ascending order (insertion order). Entries that have expired between index write and read are silently skipped. Returns `nil, nil` (not an error) if the transaction is unknown or the index has expired.

## Testing

```bash
go test ./pkg/plugin/implementation/payloadstore/...
```
