# Catalog Crawler

`catalogcrawler` discovers Beckn catalog indexes (via a registry plugin's network-scoped query, or a fixed static list), polls them on a schedule, fetches and self-signature-verifies changed catalog entries and files, and pushes the resulting catalogs onward to a Discovery service. Progress and retry state are persisted in Postgres, so a restart resumes rather than re-crawling everything.

The core fetch/verify/decode and catalog-resolve/orchestration logic lives in [github.com/beckn/catalog-core](https://github.com/beckn/catalog-core) (`pkg/catalog`, `pkg/catalog/crawler`, `pkg/catalog/crawlmanager`) — this plugin is deployment-specific wiring on top of it: config parsing, the Postgres-backed store, the registry-backed/static discovery sources, the Discovery-push sink, and the ticker-driven scheduler.

## Requirements

`catalogcrawler` requires:

- a `dbDsn` reachable Postgres database (migrations run automatically on startup)
- a registry plugin implementing `RegistryLookup` — every fetched index entry and catalog file is self-signed, and the registry is the only source of the signing keys checked against. There is deliberately no per-deployment trusted-key configuration.
- a registry plugin implementing `RegistryMetadataLookup` (e.g. `dediregistry`), required whenever `networks` is configured — its `QueryByNetwork` method resolves each configured network's member providers and their catalog index URLs. In practice this is the same plugin instance as `RegistryLookup` above (`dediregistry` implements both).

## Config

```yaml
catalogCrawler:
  id: catalogcrawler
  config:
    dbDsn: "postgres://user:pass@localhost:5432/catalogcrawler"
    networks: "example.network.production"
    discoveryPushUrl: "https://discovery.example.org/beckn/catalog/push"
    participantId: "bpp.example.org"
    bppUri: "https://bpp.example.org"
    indexIntervalSeconds: "300"
    catalogIntervalSeconds: "30"
    fetchTimeoutSeconds: "30"
    maxFetchBytes: "10485760"
    maxDecompressedBytes: "20971520"
    maxPushBytes: "10485760"
    maxAttempts: "0"
```

Supported config keys:

- `dbDsn`: required. Postgres connection string for the crawl queue/cursor store.
- `discoveryPushUrl`: required. Where crawled catalogs are pushed.
- `networks`: comma-separated networkIds to discover indexes for via the configured `RegistryMetadataLookup` plugin (e.g. `dediregistry`'s `QueryByNetwork`). Drives both discovery and scope filtering — a catalog entry naming a network not in this list is skipped.
- `staticIndexUrls`: comma-separated, optional fixed index URLs, unioned with any registry-discovered ones.
- `participantId`, `bppUri`: this deployment's own bppId/bppUri, stamped onto pushed catalogs.
- `fetchTimeoutSeconds`: optional, default `30`. Whole-attempt HTTP timeout for index/catalog fetches.
- `maxFetchBytes`: optional, default `10485760` (10 MiB). Cap on a fetched artifact's at-rest size.
- `maxDecompressedBytes`: optional, default `20971520` (20 MiB). Cap on a decompressed catalog file's size.
- `maxPushBytes`: optional, default `10485760` (10 MiB). Cap on a single push request to Discovery.
- `indexIntervalSeconds`: optional, default `300` (5 min). How often index sources are re-discovered and polled.
- `catalogIntervalSeconds`: optional, default `30`. How often the sync queue is drained.
- `maxAttempts`: optional, default `0` (unlimited). Transient-failure retries before a queue item is parked; a fresh publish of the same catalog re-arms it regardless.
- `allowPrivateHosts`: optional, default `false`. Allows loopback/private fetch targets. **Tests only — must stay `false` in production**, or the crawler's SSRF guard is defeated.

## Signature verification

- Every fetched index entry and catalog file carries a self-signature, checked against the signing key the configured `RegistryLookup` returns for that entry's `(nodeId, keyId)`.
- Key resolution and caching are entirely the `RegistryLookup` plugin's responsibility (e.g. `registry`, `dediregistry`) — this plugin does not add its own cache in front of it, to avoid a second, independently-expiring cache that could still trust a key after the registry plugin's own cache has already invalidated it (e.g. on revocation).
- A signature failure (unknown key, revoked/expired subscription, malformed key material, bad signature) is permanent and the item is parked, not retried; a registry lookup failure (network/outage) is transient and retried on the next tick.

## On-demand crawl

`CrawlRegistry(ctx, registryURL, networkIDs)` triggers an immediate registry-backed discovery pass against caller-supplied `networkIDs`, independent of the configured `networks` default, without waiting for the next scheduled tick. Discovery still goes through the configured `RegistryMetadataLookup` plugin instance (which owns its own configured registry URL) — `registryURL` is validated as non-empty but is otherwise informational, kept for backward compatibility with the `Crawler` interface's existing signature. It returns a run ID immediately; the run's outcome is only observable via logs and the queue, the same as a scheduled tick. The run is tied to the plugin's own lifecycle (not the caller's context), so it is not cut short by a request-scoped caller and is waited for on shutdown.
