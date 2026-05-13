# PayloadStore

`payloadstore` records every request and response payload passing through the gateway, indexed by `message_id` and `transaction_id`, with TTL-based expiration via the cache backend.

ONIX has been stateless since inception. PayloadStore is the foundation for all stateful use cases — it makes transaction history available to other plugins without requiring any of them to manage storage themselves.

## Requirements

`payloadstore` requires a `cache` plugin configured in the same handler. It has no dependency on any specific cache backend — Redis, ElastiCache, Memcached, or an in-process test double all work without changes to the plugin.

`payloadstore` also requires the `reqpreprocessor` middleware to be configured in the same handler with `contextKeys: transaction_id,message_id`. The middleware extracts those values from the request body into the Go request context, which PayloadStore reads for indexing and dedup. Without it, `message_id` and `transaction_id` will be empty in every stored entry and the duplicate-detection check will never fire.

```yaml
middleware:
  - id: reqpreprocessor
    config:
      contextKeys: transaction_id,message_id
      role: bap   # or bpp
```

## Behaviour

PayloadStore is an **infrastructure plugin**, not a step. It fires automatically at two fixed points in the handler when configured:

1. **Before the pipeline** — calls `Exists(message_id)`. If the message was already seen, the handler immediately returns a NACK. The original entry is preserved — the duplicate attempt is not stored. The step pipeline does not run.
2. **After the pipeline** — stores the request body, response body, and the ACK or NACK outcome (including the error reason on failure).

Do not add `payloadStore` to the `steps` list. It is wired into the handler automatically.

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
- `indexTTL`: Transaction index lifetime. Defaults to `ttl + 1h` if absent. Should be slightly longer than `ttl` so the index outlives the last entry it references.
- `maxBodyBytes`: Maximum bytes stored for `requestBody` and `responseBody` individually. Bodies exceeding this limit are **truncated** before storage. Set to `"0"` for no limit. Default: `"1048576"` (1 MiB).
- `storeBody`: Whether to persist request and response bodies. Set to `"false"` for metadata-only mode. Default: `"true"`.
- `storeSignature`: Whether to persist the raw `Authorization` header value as the `Signature` field. Useful for non-repudiation and countersignature validation. Default: `"false"`.
- `compress`: Applies gzip compression to stored body bytes before writing to cache, reducing Redis memory usage. This is **storage-level** compression — independent of HTTP `Content-Encoding`. Default: `"false"`.

## Metadata-only mode

When `storeBody: "false"`, request and response bodies are not stored. All envelope fields are always persisted regardless of this setting:

| Field | Purpose |
|-------|---------|
| `MessageID` | Dedup and point lookup |
| `TransactionID` | Group all messages in a transaction |
| `NetworkID` | Identify which network the message belongs to |
| `Action` | Ordering and flow validation |
| `SubscriberID` | Which participant sent it |
| `Role` | BAP / BPP / Gateway |
| `Outcome` | Whether the message was ACK'd or NACK'd |
| `OutcomeReason` | Error detail if NACK'd |
| `StoredAt` | Chronological ordering |
| `ExpiresAt` | Stamped at write time from `ttl` |

This mode is sufficient for transaction flow validation, duplicate detection, outcome tracking, and per-transaction rate limiting with negligible cache storage cost.

## Cache key layout

Keys are namespaced by the owning handler's module name, preventing collisions between handlers that share the same cache backend (e.g., a BAP and BPP both connected to the same Redis instance, or two modules with the same role).

| Key | Value | TTL |
|-----|-------|-----|
| `payload:{moduleName}:msg:{messageID}` | `j:<JSON>` or `c:<base64(gzip(JSON))>` — format detected from prefix at read time | `ttl` |
| `payload:{moduleName}:txn:{transactionID}:index` | JSON array of message IDs, oldest-first | `indexTTL` |

The `{moduleName}` is taken from the handler's module name at construction time (e.g., `bap-txn-caller`). Each handler instance gets its own isolated keyspace.

`GetByTransactionID` reads the index, fetches each entry individually, and silently skips any that have expired between the index write and read.

**Known limitation**: The transaction index update is a non-atomic read-modify-write over the cache. Two concurrent `Store` calls for the same transaction can race — the last writer wins, potentially dropping one message ID from the index. Individual message keys are always written correctly; only `GetByTransactionID` may return an incomplete list under this race. Individual message lookup via `GetByMessageID` and dedup via `Exists` are unaffected.

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
        storeBody: "true"
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
    middleware:
      - id: reqpreprocessor
        config:
          contextKeys: transaction_id,message_id
          role: bap
  steps:
    - validateSign
    - validateSchema
    - addRoute
```

## Testing

```bash
go test ./pkg/plugin/implementation/payloadstore/...
```
