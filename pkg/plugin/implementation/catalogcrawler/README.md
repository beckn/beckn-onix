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
    parkSweepIntervalSeconds: "900"
    parkOlderThanSeconds: "0"
    maxParkCount: "0"
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
- `parkSweepIntervalSeconds`: optional, default `900` (15 min). How often the revive-or-abandon sweep runs — see "Parked and abandoned catalogs" below. Independent of `indexIntervalSeconds`/`catalogIntervalSeconds`.
- `parkOlderThanSeconds`: optional, default `0`. How long a catalog must have been sitting parked before a sweep acts on it. `0` means no extra grace period — each sweep acts on anything currently parked.
- `maxParkCount`: optional, default `0`, meaning derived from `parkSweepIntervalSeconds` and a 12-hour total retry budget (e.g. the default 15-minute sweep interval yields 48). How many times a parked catalog is revived before being abandoned instead.

## Signature verification

- Every fetched index entry and catalog file carries a self-signature, checked against the signing key the configured `RegistryLookup` returns for that entry's `(nodeId, keyId)`.
- Key resolution and caching are entirely the `RegistryLookup` plugin's responsibility (e.g. `registry`, `dediregistry`) — this plugin does not add its own cache in front of it, to avoid a second, independently-expiring cache that could still trust a key after the registry plugin's own cache has already invalidated it (e.g. on revocation).
- A signature failure (unknown key, revoked/expired subscription, malformed key material, bad signature) is permanent and the item is parked, not retried on its own schedule — but see "Parked and abandoned catalogs" below: a permanent failure isn't necessarily final if its underlying cause was transient in practice (e.g. the publisher's registered key gets corrected later). A registry lookup failure (network/outage) is transient and retried on the next tick.

## Parked and abandoned catalogs

A catalog whose sync keeps failing permanently (or exhausts `maxAttempts`) gets **parked**: `crawlmanager.SyncNext` stops retrying it on the normal `catalogIntervalSeconds` cadence. Without anything else, a parked catalog would stay parked forever unless its publisher republishes a *changed* index — an unchanged (304, or same declared version) index never re-triggers the decide loop that would otherwise notice the underlying cause cleared up.

A separate, independent sweep (`parkSweepIntervalSeconds`) periodically revisits every parked catalog and either:
- **revives** it back to the normal queue (if it's been parked fewer than `maxParkCount` times), giving it another chance without waiting for a republish, or
- **abandons** it (once `maxParkCount` is reached) — terminal: no further automatic retries, though a fresh publish still reactivates it immediately regardless of abandonment.

Both states are visible via [`/crawl/status`](#crawlstatus-http-endpoint)'s `parked`/`parkCount`/`abandoned`/`abandonedAt` fields — useful for exactly the kind of case that motivated this: a catalog failing signature verification against a registry key that turns out to be stale gets parked, retried a bounded number of times, and if the publisher never fixes it, surfaces as abandoned rather than silently retrying (or silently never retrying) forever with no visibility.

## On-demand crawl

`CrawlRegistry(ctx, networkIDs)` triggers an immediate registry-backed discovery pass against caller-supplied `networkIDs`, independent of the configured `networks` default, without waiting for the next scheduled tick. Discovery goes through the configured `RegistryMetadataLookup` plugin instance, which owns its own registry URL — there is no per-call registry URL, so this cannot target a different registry than the one the deployment is configured with. It returns a run ID immediately; the run's outcome is only observable via logs and the queue, the same as a scheduled tick. The run is tied to the plugin's own lifecycle (not the caller's context), so it is not cut short by a request-scoped caller and is waited for on shutdown. It requires the crawler already be running (`Start` already called) — calling it on a freshly-constructed, unstarted instance returns an error.

### `/crawl` HTTP endpoint

`CrawlRegistry` is also reachable over HTTP via a `catalogCrawl`-type module (see [handler.go](handler.go) and [CONFIG.md](../../../../CONFIG.md#handler-type-catalogcrawl) for the full request/response shape). This endpoint always calls into the one crawler instance `cmd/adapter/main.go` constructs and starts as a background job from the top-level `plugins.crawler` config (see [CONFIG.md](../../../../CONFIG.md#pluginscrawler)) — it is not a separately-configured plugin instance, so `plugins.crawler` must be configured for this endpoint to do anything; without it, requests fail with "no Crawler plugin configured/running".

```yaml
# top-level plugins block -- starts the crawler as a background job
plugins:
  registry:
    id: dediregistry
    config: { ... }
  crawler:
    id: catalogcrawler
    config:
      dbDsn: "postgres://user:pass@localhost:5432/catalogcrawler"
      discoveryPushUrl: "https://discovery.example.org/beckn/catalog/push"
      # ... see Config above

modules:
  # exposes the on-demand trigger for the crawler configured above
  - name: crawl
    path: /crawl
    handler:
      type: catalogCrawl
```

```
POST /crawl
{"networkIds": ["example.network.production"]}

202 Accepted
{"runId": "3fa2c1e0-4b1a-4c9e-9c3a-6a2f8e0b7d21"}
```

The module takes no `handler.plugins` of its own -- unlike `catalogPublisher`'s module, which constructs its own plugins per-request, this handler is wired directly to the singleton above (`catalogcrawler.RegisterHandler`, called once from `main.go` after the crawler starts), since `CrawlRegistry` needs that exact running instance rather than a fresh one.

### `/crawl/status` HTTP endpoint

`Status(ctx, subscriberID, catalogID)` reports the last-known crawl/sync state persisted for a
publisher's catalogs — a plain read against `crawler_catalog`/`crawler_queue`/`crawler_index`, not
a live check, and not tied to any particular `/crawl` `runId` (a caller only ever knows their own
`catalogId`, not a run's id).

Eventually this answers a specific authenticated publisher about their own data, so it's meant to
be a **signed, network-facing call** — verifying the caller the same way every other
subscriber-facing call in this codebase does (`signValidator` + `keyManager.LookupNPKeys`, inlined
into `Decode` rather than via the `std` handler's step pipeline; see
[statushandler.go](statushandler.go)'s doc comment for why it can't just reuse `validateSign`
directly). **That verification is not implemented yet.** This first cut only supports
`authDisabled: true`, which is required to construct the handler at all — omitting it (or setting
it `false`) fails at startup rather than silently running unauthenticated. With `authDisabled: true`,
`subscriberId` is a plain, unauthenticated query param instead of a verified identity: **do not run
this in any real deployment as-is** — it lets any caller read any subscriber's crawl status.

```yaml
modules:
  - name: crawlStatus
    path: /crawl/status
    handler:
      type: catalogCrawlStatus
      authDisabled: true # UNAUTHENTICATED -- see above; not for real deployments
      plugins:
        # registry is unrelated to authenticating callers (there is none
        # yet) -- it's catalogcrawler.Provider.New's own dependency, used
        # to verify fetched catalogs' self-signatures.
        registry: { id: dediregistry, config: { ... } }
        # Its own Crawler instance/config -- same dbDsn as the top-level
        # plugins.crawler, so it reads the same Postgres tables, but never
        # Start()-ed (Status only reads, it doesn't need the scheduler).
        crawler:
          id: catalogcrawler
          config:
            dbDsn: "postgres://user:pass@localhost:5432/catalogcrawler"
            discoveryPushUrl: "https://discovery.example.org/beckn/catalog/push"
```

```
GET /crawl/status?subscriberId=staging.p-node.fabric.nfh.global&catalogId=staging.p-node.fabric.nfh.global/CAT-1

200 OK
[
  {
    "catalogId": "staging.p-node.fabric.nfh.global/CAT-1",
    "indexUrl": "https://angular-absently-gab.ngrok-free.dev/beckn/index/becknCatalogs.index.json",
    "everSynced": true,
    "version": 2,
    "entryVersion": 2,
    "retired": false,
    "queued": false,
    "updatedAt": "2026-08-24T16:33:44Z",
    "indexLastPolledAt": "2026-08-24T16:33:44Z"
  },
  {
    "catalogId": "staging.p-node.fabric.nfh.global/CAT-2",
    "indexUrl": "https://angular-absently-gab.ngrok-free.dev/beckn/index/becknCatalogs.index.json",
    "everSynced": false,
    "version": 0,
    "entryVersion": 0,
    "retired": false,
    "queued": true,
    "attempts": 0
  },
  {
    "catalogId": "staging.p-node.fabric.nfh.global/CAT-3",
    "everSynced": true,
    "version": 1,
    "entryVersion": 1,
    "retired": false,
    "queued": false,
    "abandoned": true,
    "parkCount": 48,
    "abandonedAt": "2026-08-25T09:00:00Z",
    "lastError": "crawler: signature verification failed: Ed25519 signature verification failed"
  }
]
```

`subscriberId` is required; omitting `catalogId` returns every catalog owned by it. `everSynced`
is `false` for a catalog queued for its very first sync -- CAT-2 above -- in which case
`version`/`entryVersion`/`retired`/`lastError`/`updatedAt` are all zero-valued (nothing has settled
yet); check `queued` to see whether that first sync is pending right now. `lastError` is present
only if the catalog's most recent sync attempt failed (cleared on the next success); a non-empty
`catalogId` matching nothing at all (not even a pending first sync) is a `404`, an empty `catalogId`
matching nothing is a `200` with `[]`. `queued`/`attempts`/`nextAttemptAt` are only meaningful
while `queued` is `true` (a sync still pending or retrying).

`parked`/`parkCount` and `abandoned`/`abandonedAt`/`parkCount` (CAT-3 above) report the
revive-or-abandon state described in "Parked and abandoned catalogs" above -- `parked`, `queued`,
and `abandoned` are mutually exclusive. CAT-3's `lastError` is the actual reason it kept failing
(RecordFailure's last-recorded error), so this is where a persistent signature-verification
mismatch like the one that motivated this feature actually becomes visible, instead of silently
parking forever.

There is no exact "next scheduled crawl" — `PollIndexes` polls every discovered index
unconditionally on each tick, so the crawler has no per-index schedule of its own to report;
`indexLastPolledAt` plus the deployment's own `indexIntervalSeconds` is the closest estimate
available.
