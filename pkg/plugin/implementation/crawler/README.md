# crawler

Thin onix plugin adapter over the framework-agnostic crawler engine in
[`internal/crawler`](internal/crawler). It implements `definition.Crawler`: it
walks a publisher's catalog **index -> baseline + change files** per the
Decentralized Catalog **"File Specifications"** tab, resolves each catalog to its
current content, and pushes the result to a Discovery service.

The plugin is deliberately thin: it reads config from its plugin config map
(with **env overriding**, so secrets like the DB DSN stay out of YAML), opens and
migrates the Postgres state, injects an optional schema validator, starts the
engine's scheduled jobs, and returns a closer that stops them. All the actual
crawl logic lives in the engine package.

## What the crawler does

The engine runs as **two scheduled background jobs** plus an **on-demand
trigger** (`definition.Crawler`):

1. **Index job** (every `CRAWLER_INDEX_INTERVAL`, default `5m`) fetches each
   configured publisher index and, for every catalog in it, runs change
   detection against a stored per-catalog version cursor
   ([`internal/crawler/catalog/change.go`](internal/crawler/catalog/change.go)):
   - `sync`: content advanced (new, or `latestVersion > cursor`). Enqueue a sync
     to the catalog job.
   - `skip_unchanged`: cursor already at the latest version, nothing to do.
   - `retire`: the entry went `RETIRED` (a tombstone). Retire it downstream.
   - `rollback`: version went **backwards**. Flagged and **not applied** (the
     file spec's monotonic-version rule).

   Enqueue is coalescing and the queue is keyed by catalog id. File URLs are read
   from the stored index, not from the queue row.

2. **Catalog job** (every `CRAWLER_CATALOG_INTERVAL`, default `30s`) claims
   queued items (`FOR UPDATE SKIP LOCKED`) and for each:
   - **Selects** whether to carry it and computes `visibleTo`
     ([`catalog/scope.go`](internal/crawler/catalog/scope.go)). A **public**
     catalog (no `networkIds`) is always taken and visible to everyone. A
     **network-scoped** catalog is taken only if its networks intersect the
     crawler's configured `CRAWLER_NETWORK_IDS`, and its own networks flow
     through as `visibleTo`.
   - **Resolves** the full catalog at the target version
     ([`catalog/resolve.go`](internal/crawler/catalog/resolve.go)): fetch the
     `baseline`, then fold each `changes[]` entry in version order via
     [`pkg/catalogfile`](../../../catalogfile). No composed state is kept
     locally, so a changed catalog is always resolved in full.
   - **Schema-validates** the push body against the configured action (optional,
     via the injected `schemaValidator`).
   - **Pushes** to Discovery ([`internal/crawler/publish`](internal/crawler/publish)):
     a push context plus `message.catalogs` and a matching `publishDirectives`
     entry carrying `updateMode` and `visibleTo`. This version always sends
     `MERGE` (id-keyed upserts) and builds the delta from the change files.
     Removals are not applied yet, so an update that only removes resources
     settles as `skipped`. Setting `CRAWLER_MERGE_ONLY=false` re-enables the
     dormant mode-by-changeset path that can send `FULL`.
   - On failure, retries with backoff up to `CRAWLER_MAX_ATTEMPTS`, and rolls the
     per-catalog sync status up to `pushed` / `partial` / `faulted`.

3. **On-demand `/crawl`** re-crawls one index immediately (supportability for a
   stuck publisher). It pokes an immediate index pass. The scheduled jobs still
   do the actual sync and push.

State lives in Postgres (`crawler_index` / `crawler_queue` / `crawler_catalog`).
The engine migrates these on start.

## Source resolution

The set of publisher indexes to crawl comes from `CRAWLER_INDEX_URLS`, an
explicit comma-separated list. It is the only source, and it is required when
the crawler is enabled.

`CRAWLER_REGISTRY_URL` is **rejected at startup**. Discovering indexes from a
registry has no client implementation yet, so a crawler configured that way
would poll nothing forever. Config load fails with a message naming
`CRAWLER_INDEX_URLS` instead of starting a crawler that only looks configured
([`config/settings.go`](internal/crawler/config/settings.go)).

Two different things in this document are called a registry. Keep them apart.

| Use of "registry" | Status | How it is configured |
|---|---|---|
| **Key directory**: resolve a publisher's signing key so a catalog file signature can be verified | wired, and **required** | the `registry` plugin block on the crawl handler |
| **Index source**: ask a registry which publisher indexes exist | not wired, rejected at startup | would have been `CRAWLER_REGISTRY_URL` |

Everything below about trust and signatures is the first row. Only this section
is about the second.

An on-demand `/crawl` request can name a single `indexUrl` to crawl right now,
but only one that is already in `CRAWLER_INDEX_URLS`. The handler checks the
request against that list and answers `403` for anything else
([`catalogPullHandler.go`](../../../../core/module/handler/catalogPullHandler.go)).
With no `CRAWLER_INDEX_URLS` configured the allowlist is empty, so every
`/crawl` request is refused.

## Verification

Fetched publisher content is verified before it is used. Every check below fails
**closed**: if it cannot be satisfied, the file is rejected rather than accepted
on weaker evidence.

- **Per-file signature (mandatory).** Every file entry in the index carries a
  signed tuple over `{catalogId, version, url, digest, validUntil}`
  ([`catalog/index.go`](internal/crawler/catalog/index.go)). It is verified
  through [`pkg/security/artifactverifier`](../../../security/artifactverifier),
  in [`internal/crawler/fetch/verify.go`](internal/crawler/fetch/verify.go),
  against the publisher's public key **as published in the network registry**.
  The gate runs inside `FetchFile` **before** the GET, so an entry that is
  unsigned, not bound to a catalog, expired, keyed to a `keyId` the registry
  does not know, or carrying a tuple that does not verify is rejected without
  spending a fetch. There is no digest-only mode and no way to turn the gate
  off. With no key source wired at all, every file is rejected, so the gate
  cannot silently degrade to a digest check.
- **Digest.** Once the signature passes, the fetched bytes are matched against
  the declared `sha-256:<hex>` digest before anything is decoded
  ([`internal/crawler/fetch/integrity.go`](internal/crawler/fetch/integrity.go)).
  The signature says the publisher really declared this `{url, digest, version}`
  triple. The digest says the bytes served are the ones that triple named.
- **SSRF guard**: publisher URLs that resolve to loopback, private, or
  link-local addresses are refused
  ([`internal/crawler/fetch/guard.go`](internal/crawler/fetch/guard.go)). The
  push endpoint is operator config and is exempt.
- **Size caps**: `CRAWLER_MAX_ARTIFACT_BYTES` per fetched artifact, and
  `CRAWLER_MAX_DECOMPRESSED_BYTES` while inflating a compressed one.

### Where the signing keys come from

Signing keys are **not** deployment config. There is no key list to paste into
YAML or env, and no environment variable holds one. The crawler resolves each
key through the registry, the same channel signvalidator uses for transport
signatures ([`fetch/verify.go`](internal/crawler/fetch/verify.go)):

1. The index document declares a top-level `participantId`. The index decoder
   stamps it onto every file entry, along with the entry's enclosing `catalogId`
   ([`catalog/signature.go`](internal/crawler/catalog/signature.go)).
2. The gate calls the registry plugin's `Lookup` with that `participantId` as
   the subscriber id and the file entry's `keyId`. That is the same
   `{subscriberId, keyId}` pair the transport signature path looks up.
3. The subscription that comes back must be usable. `model.IsKeyStatusUsable` is
   the shared rule, so a revoked, expired or invalid-SSL subscription verifies
   nothing.
4. Its `signingPublicKey` is decoded from standard base64 and must be exactly a
   32-byte Ed25519 key.
5. The file's tuple is verified under that key.

The `participantId` is publisher-asserted, because the index document itself is
not signed. That is still safe, and the reason needs stating because it is not
obvious. A forged `participantId` resolves to **that** participant's real
registered key, and the file signature then fails under it. The forgery turns
into a closed failure rather than into trust. So `participantId` is a hint for
key lookup only. Nothing may start trusting it for authorization, scoping or
attribution without giving it a verified source first.

Successful lookups are cached per `{participantId, keyId}` for 5 minutes
(`fetch.DefaultKeyCacheTTL`), so an index of thousands of files costs one
registry call per key rather than one per file. Failures are never cached, so a
registry outage recovers on the next pass. The TTL is a constant. No config key
exposes it.

The registry plugin is **required** when the crawler is enabled, and it is
checked twice before anything opens. The crawl handler refuses to construct
without a `registry` block, and the crawler's composition root refuses to build
without a registry (`crawler.ErrNoRegistry`). Both fail at startup on purpose. A
crawler that cannot reach keys is not "less strict", it is inert, and hours of
parked catalogs read like a publisher problem rather than a missing plugin
block.

### What a signature failure does

Signature failures are **permanent** and carry their own fault class,
`signature` ([`catalog/fault.go`](internal/crawler/catalog/fault.go)).
Re-fetching does not fix an unsigned, expired or forged entry, so the catalog is
**parked** (ERROR) and re-activates only when the publisher publishes a new
version.

One failure on this path is deliberately **not** permanent: a registry that
could not be reached. An unreachable registry says nothing about the file, so it
stays an unclassified error, classifies as `transient`, and retries with
backoff. A registry that answers, but answers "no such key" or hands back a
revoked or undecodable key, is permanent and parks. The registry lookup runs
under the same `CRAWLER_FETCH_TIMEOUT` budget a fetch gets.

## Still deferred

Signature verification is **not** on this list. It is implemented, mandatory,
and fails closed. What is still deferred is below.

- **The registry as an index source.** The crawler is told which indexes to
  crawl by `CRAWLER_INDEX_URLS` only. Asking a registry which publisher indexes
  exist has no client implementation, and `CRAWLER_REGISTRY_URL` is rejected at
  startup rather than accepted and ignored. This is separate from registry
  **key** lookup, which is wired and required.
- **The index document itself is not signature-verified.** Only the per-file
  tuples inside it are. So `participantId`, `catalogType` and `visibleTo` in the
  push envelope are **publisher-asserted, not authenticated**. Anyone able to
  serve or tamper with the index can change them and the per-file gate will not
  notice, because those fields are not covered by the signed tuple. Treat them
  as claims about the publisher, not as an authenticated identity.
- **`authMethods` (signed-request download gate) not exercised.** Parsed but
  unused. This phase takes only openly-fetchable files.
- **No `catalogType` filtering.** Parsed and carried, but nothing skips e.g. a
  `MASTER` catalog yet.
- **Removals and retirals are not applied downstream.** See
  [Known gaps reflected in the logs](#known-gaps-reflected-in-the-logs).

## Config

Config is read from the plugin config map, with matching env vars overriding (so
secrets stay in env). The engine's own settings are built by
[`internal/crawler/config/settings.go`](internal/crawler/config/settings.go).

| Key | Meaning | Required | Default |
|---|---|---|---|
| `CRAWLER_ENABLED` | Master on/off switch. When it is not `true` the plugin loads inert: no DSN, no database, no jobs, and no other key is read | no | `false` |
| `CRAWLER_STORE_PROVIDER` | Which registered persistence backend to use. `postgres` is the only one registered ([`store/postgres.go`](internal/crawler/store/postgres.go)) | no | `postgres` |
| `CRAWLER_DB_DSN` | Postgres DSN for crawler state | **yes, when enabled** | none |
| `CRAWLER_PUSH_ENDPOINT` | Discovery push endpoint | **yes, when enabled** | none |
| `CRAWLER_INDEX_URLS` | Comma-separated publisher index URLs. The only source, and the `/crawl` allowlist | **yes, when enabled** | none |
| `CRAWLER_REGISTRY_URL` | Registry to resolve indexes from. **Not implemented. Any non-empty value fails startup** | must be unset | none |
| `CRAWLER_NETWORK_IDS` | Comma-separated networks this crawler is a member of (for scoped catalogs) | no | empty, so public catalogs only |
| `CRAWLER_BPP_URI` | Publisher URI stamped into the push context | no | none |
| `CRAWLER_INDEX_INTERVAL` | Index-job interval (Go duration) | no | `5m` |
| `CRAWLER_CATALOG_INTERVAL` | Catalog-job interval (Go duration) | no | `30s` |
| `CRAWLER_FETCH_TIMEOUT` | Per-fetch timeout (Go duration). Also bounds each registry key lookup | no | `30s` |
| `CRAWLER_MAX_ARTIFACT_BYTES` | Byte cap per fetched artifact | no | `10485760` (10 MiB) |
| `CRAWLER_MAX_DECOMPRESSED_BYTES` | Byte cap after decompression | no | `104857600` (100 MiB) |
| `CRAWLER_MAX_PUSH_BYTES` | Byte cap per push batch | no | `10485760` (10 MiB) |
| `CRAWLER_MAX_ATTEMPTS` | Max sync attempts before parking | no | `5` |
| `CRAWLER_MERGE_ONLY` | Always push `MERGE`. Set to `false` for the dormant mode-by-changeset path | no | `true` |
| `CRAWLER_LOG_LEVEL` | Handler level for the crawler's own logger. `debug`, `info`, `warn` (or `warning`), `error`. Read by the plugin adapter, not by the engine settings | no | `info` |
| `CRAWLER_SCHEMA_ACTION` | Action path the push body is schema-validated against. Read by the plugin adapter, and only used when a `schemaValidator` is configured | no | `catalog/publish` |

There is deliberately **no key configuration in this table**. Publisher signing
keys come from the registry plugin, never from env or YAML. See
[Where the signing keys come from](#where-the-signing-keys-come-from).

`CRAWLER_DB_DSN` and `CRAWLER_PUSH_ENDPOINT` are secret or deploy-specific and
should come from env. The non-secret keys can live in the YAML config block. Env
wins, but only when it is set to a non-empty value: an env var set to the empty
string falls through to the YAML value rather than clearing it.

The crawler is off by default. `CRAWLER_ENABLED=true` is what makes the plugin
open a database and start the jobs, and it is also what makes `CRAWLER_DB_DSN`,
`CRAWLER_PUSH_ENDPOINT` and `CRAWLER_INDEX_URLS` required. A disabled crawler
resolves to the zero value and reads none of them.
[`internal/crawler/config/settings.go`](internal/crawler/config/settings.go) is
the exact contract for how each key is parsed.

Two parsing rules are worth knowing before an operator debugs a setting that
looks ignored.

- Any numeric or duration value that parses to zero or less is clamped back to
  its default, so a literal `0` cap cannot silently reject everything and a `0s`
  interval cannot become a hot loop.
- A value that does not parse at all falls back silently to the default. The one
  exception is `CRAWLER_MERGE_ONLY`, which **fails startup** on anything
  `strconv.ParseBool` rejects, naming the value given. That is deliberate:
  `CRAWLER_MERGE_ONLY=0` used to mean `true`.

## Running it locally

The `crawl` module is wired alongside the existing `bapTxnReceiver` and
`bapTxnCaller` modules in
[`config/local-beckn-one-bap.yaml`](../../../../config/local-beckn-one-bap.yaml)
(the `crawl` block, path `/crawl`).

That block needs a `registry` plugin next to its `crawler` and `schemaValidator`
blocks. It is not optional. The crawl handler refuses to construct without one,
because that registry is where publisher signing keys come from. An optional
`cache` block next to it is what the registry client memoises lookups in; leave
it out and lookups simply are not memoised.

Build the plugin, provide the secrets via env, and run the adapter:

```bash
./install/build-plugins.sh        # builds plugins/crawler.so, among others

export CRAWLER_DB_DSN="postgres://user:pass@localhost:5432/crawler?sslmode=disable"
export CRAWLER_PUSH_ENDPOINT="https://discovery.local/beckn/catalog/push"

go run ./cmd/adapter --config=config/local-beckn-one-bap.yaml
```

The scheduled index and catalog jobs start in the background at plugin init. To
poke an **immediate** re-crawl of one index (returns `202 Accepted`; the crawl
runs asynchronously and results surface through telemetry, not the response):

```bash
curl -X POST http://localhost:8081/crawl \
  -H "Content-Type: application/json" \
  -d '{"indexUrl": "https://cdn.publisher.example.com/beckn/catalog-index.json"}'
# -> 202 {"status":"ACCEPTED","indexUrl":"...","runId":"..."}
```

`participantId` is accepted in the `/crawl` body for forward-compatibility with
DID resolution but is not yet used.

`runId` is the same `run_id` the crawl's log lines carry, so an operator can grep
for one specific request instead of guessing from interleaving.

The only `indexUrl` this accepts is one already listed in `CRAWLER_INDEX_URLS`.
Anything else comes back `403`.

For a runnable local stack there is [`install/crawler-fixture`](../../../../install/crawler-fixture),
with a publisher origin, a Postgres and real signatures. Read its README first:
it needs a registry to resolve the fixture publisher's key, and that registry is
not part of the stack.

The crawler's own round-trip tests exercise the full chain over an in-process
`httptest` server, with the private-host guard relaxed and a test key source
injected (`fetch.NewClient(..., allowPrivate=true, fetch.WithTrustedKeys(fetch.StaticKeys(...)))`).
`fetch.StaticKeys` is a test helper only. Nothing in the composition root builds
one, so no manual serving and no registry are needed to run the tests.

---

# Logs

This is the logging model the crawler emits.
[`internal/crawler/runner/telemetry.go`](internal/crawler/runner/telemetry.go)
mints exactly these components, stages, messages, levels, and fields.

## One process, two jobs

The crawler is **one process** running **two jobs** linked by a durable queue:

```
        every ~5m                              every ~30s
   +-- CRAWL job --+                     +----- SYNC job -----+
   | poll indexes  |--Enqueue-->[queue]--| pull -> unpack ->  |--> Discovery
   | find changes  |          ClaimNext  | verify -> push     |
   +---------------+                     +--------------------+
      (producer)                                (consumer)
```

So there are exactly **three log components**:

| component | what it is | role |
|---|---|---|
| `daemon` | the process | starts and stops the two jobs |
| `crawl` | job 1 | polls publisher indexes, queues catalogs that changed |
| `sync` | job 2 | takes a queued catalog and syncs it to Discovery |

## The shape of every log line

Every line carries five things:

```
component / stage / message / {attributes} / {stats}
```

| part | field(s) | answers | example |
|---|---|---|---|
| **component** | `component` | which subsystem? | `sync` |
| **stage** | `stage` | where in it? | `failed` |
| **message** | `msg` | what happened? (natural sentence) | "couldn't send the catalog to Discovery, 503; will retry (attempt 2 of 5)" |
| **attributes** | e.g. `catalog_id`, `index_url`, `fault` | *which one* / how to filter | `catalog_id=.../electronics-2025` |
| **stats** | e.g. `resources`, `dur_ms`, `synced` | *how much* / is it healthy | `resources=12 batches=2` |

Rule of thumb: **attributes tell you which one, stats tell you how much.**

- **Event key** is always `crawler.<component>.<stage>`, e.g.
  `crawler.sync.failed`.
- **Base attributes on every line:** `component`, `stage`, `msg`, `run_id`.
- **Every `crawl` line also carries** `trigger` (`scheduled` or `on_demand`). A
  scheduled tick and a `/crawl` on-demand trigger both log through the same
  `crawl` component, so this is how to tell which one you are looking at without
  cross-referencing `run_id` against the `/crawl` HTTP response. `sync` has no
  on-demand variant, so its lines do not carry this.
- **Sync per-catalog lines also carry** `pass_id`, `catalog_id`, `from`, `to`.

### Correlation ids

| id | scope | use |
|---|---|---|
| `run_id` | one job tick | follow everything one crawl or sync tick did |
| `pass_id` | one catalog's sync | follow one catalog through its sync |
| `catalog_id` | a catalog | follow one catalog across ticks |

### On-demand crawls (`POST /crawl`)

`/crawl` runs an immediate single-index crawl through the same `crawl` component
and the same crawl-finished summary a scheduled tick uses. `trigger=on_demand`
is the only thing that marks it as one. The HTTP response returns `runId` (the
same `run_id` on these lines) so an operator can grep for their specific
request's log lines instead of guessing from interleaving.

## Levels

| level | when | examples |
|---|---|---|
| `INFO` | milestones and per-tick summaries | `daemon.ready`, `crawl.finished`, `sync.finished` |
| `DEBUG` | per-item detail | `crawl.polled`, `crawl.queued`, `sync.syncing`, `sync.synced` |
| `WARN` | recoverable, will self-heal | transient sync failure (will retry), index unreachable |
| `ERROR` | needs a human | permanent sync failure (parked), daemon start failure, DB unhealthy |

At `INFO`, a healthy crawler is quiet: `daemon.ready` once, then one `finished`
line per active job tick (`crawl.finished`, `sync.finished`). An idle tick logs
nothing.

The handler defaults to `INFO`, so `DEBUG` lines are off unless
`CRAWLER_LOG_LEVEL` is set to `debug` (env, or the plugin config map). This is a
separate setting from the onix core logger's own `log.level`. That one governs a
different sink and does not reach this one.

## Components and stages

### `daemon`, the process

| stage | level | message | attributes | stats |
|---|---|---|---|---|
| `ready` | INFO | "crawler started, polling N source(s) every 5m, pushing to `<host>`" | source_mode, push_host, store_provider, key_source | sources, index_interval, catalog_interval, max_attempts |
| `disabled` | INFO | "crawler disabled (set CRAWLER_ENABLED=true to run it)" | none | none |
| `stopping` | INFO | "crawler stopping" | none | none |
| `stopped` | INFO | "crawler stopped" | none | none |
| `failed` | ERROR | "crawler failed to start while opening the database: `<err>`" | at (`config`/`registry`/`db_open`/`db_migrate`/`start`), error | none |

`key_source` is always `registry`, and `source_mode` is always `config`. Both are
constants for now, kept in the line so the field does not appear and disappear
when the registry index source lands. `at=registry` is the start failure that
means no registry plugin was configured.

### `crawl`, job 1 (find work)

Every stage below also carries `trigger` (`scheduled` or `on_demand`), omitted
from the attributes column since it is on all of them.

| stage | level | message (varies by result) | attributes | stats |
|---|---|---|---|---|
| `polled` | DEBUG or WARN | "index unchanged" / "index updated to v5" / "index not modified (304)" / **WARN** "couldn't reach the index: `<err>`" | index_url, version, result (`unchanged`/`updated`/`not_modified`/`unreachable`) | none |
| `queued` | DEBUG | "queued this catalog to sync (v3 -> v5)" / "queued this catalog to retire" | catalog_id, op (`sync`/`retire`), from, to | none |
| `finished` | INFO | "crawl finished, polled 1 index, 1 updated, queued 1 catalog" | none | indexes, updated, queued, dur_ms |
| `failed` | ERROR | "crawl error while `<op>`: `<err>`", for source-resolve failures and DB errors during a crawl | at/operation, error | none |

A version rollback is a rare `polled` WARN: "index version went backwards,
ignored". Store errors surface as `component=crawl` or `component=sync` with
`stage=failed` and `fault=store`.

### `sync`, job 2 (do the work)

| stage | level | message | attributes | stats |
|---|---|---|---|---|
| `syncing` | DEBUG | "syncing catalog (v3 -> v5)" | catalog_id, from, to | none |
| `synced` | DEBUG | "sent the catalog update to Discovery" | catalog_id, mode | resources, offers, batches, dur_ms |
| `skipped` | DEBUG | "nothing to send, this update only removed items, and removals aren't applied yet" | catalog_id, reason | none |
| `retired` | DEBUG | "recorded the catalog as retired locally, Discovery not notified yet (Phase 2)" | catalog_id | none |
| `failed` | WARN or ERROR | see below | catalog_id, step, fault, http_status, will_retry, attempt, error | none |
| `finished` | INFO | "sync finished, 1 sent, 1 skipped, 0 failed, 0 retrying; queue empty" | none | synced, skipped, failed, retrying, queue, dur_ms |

The **`failed`** message is built to explain *where* it broke and *whether it
recovers*:

- WARN (transient): "couldn't send the catalog to Discovery, 503; will retry
  (attempt 2 of 5)"
- ERROR (permanent): "couldn't verify the downloaded files, digest mismatch;
  parked, won't retry until the publisher publishes a new version"

The *where* comes from the fault, so no code needs decoding:

| fault | message says "couldn't ..." |
|---|---|
| `index_fetch` / `absent` | resolve the catalog |
| `ssrf` / `oversize` | download the files |
| `decode` / `gap` | unpack the files |
| `digest_mismatch` | verify the downloaded files |
| `content_invalid` | build the push request |
| `push_schema` / `push_rejected` / transient (5xx) | send the catalog to Discovery |
| `store` | save progress |
| `signature` | send the catalog to Discovery (**wrong**, see below) |

`signature` has no case of its own in `stepPhrase`
([`runner/telemetry.go`](internal/crawler/runner/telemetry.go)), so it falls to
the default clause and a signature failure reads "couldn't send the catalog to
Discovery, signature; parked". Nothing was sent and nothing was even fetched.
Filter on `fault=signature`, not on the sentence. This is documented rather than
described as intended.

**Which faults park.** `ssrf`, `oversize`, `digest_mismatch`, `signature`,
`decode`, `gap`, `content_invalid`, `push_schema` and `push_rejected` are
permanent. They park (ERROR) and re-activate only when the publisher publishes a
new version. `index_fetch`, `store` and `transient` retry with backoff (WARN).
The fetch layer names its own fault class, so a tampered artifact reports
`digest_mismatch`, a forged or unverifiable signature reports `signature`, and a
continuity gap reports `gap`, rather than all of them collapsing to `decode`.

A transient fault that keeps failing eventually parks too. `CRAWLER_MAX_ATTEMPTS`
is the budget, and once a catalog has consumed it the failure parks. That is how
an unreachable registry ends: retry, retry, then park with `fault=transient`.

Two "too big" caps land on different stages, because they fail at different
points. The download cap (`CRAWLER_MAX_ARTIFACT_BYTES`) is `oversize` ->
"download the files". A decompression bomb (`CRAWLER_MAX_DECOMPRESSED_BYTES`) is
caught while inflating, so it is `decode` -> "unpack the files". Both park.

## Reading the logs

| question | filter |
|---|---|
| Is the crawler healthy? | `component=crawl stage=finished` plus `component=sync stage=finished`, watch `failed`/`retrying`/`queue` |
| What happened to one catalog? | `catalog_id=<id>` -> `queued -> syncing -> synced` (or `failed`) |
| What did one sync attempt do? | `pass_id=<id>` |
| What did one tick do? | `run_id=<id>` |
| Anything broken? | `stage=failed` (ERROR means investigate, WARN means it will retry) |

## Example streams

Healthy cycle (at DEBUG):

```
daemon ready     "crawler started, 1 source, polling every 5m -> discovery.local"
crawl  polled    "index updated to v5"                              index_url=... trigger=scheduled
crawl  queued    "queued this catalog to sync (v3 -> v5)"           catalog_id=.../electronics-2025 trigger=scheduled
crawl  finished  "crawl finished, polled 1 index(es), 1 updated, queued 1 catalog(s)"   {indexes=1 updated=1 queued=1 dur_ms=142} trigger=scheduled
sync   syncing   "syncing catalog (v3 -> v5)"                       catalog_id=.../electronics-2025
sync   synced    "sent the catalog update to Discovery"             catalog_id=.../electronics-2025 {resources=12 offers=4 batches=2 dur_ms=180}
sync   finished  "sync finished, 1 sent, 0 skipped, 0 failed, 0 retrying; queue empty"   {synced=1 skipped=0 failed=0 retrying=0 queue=0 dur_ms=318}
```

A permanent failure (visible at INFO):

```
sync   failed    "couldn't send the catalog to Discovery, rejected (400); parked, won't retry until a new version is published"
                 catalog_id=.../electronics-2025 step=push fault=push_rejected http_status=400 will_retry=false
```

## Known gaps reflected in the logs

Two `sync` results exist only because this phase defers removals. Both are honest
in their messages:

| result | why | closes when |
|---|---|---|
| `skipped` | the update was removal-only, and removals aren't applied yet | removals land (Phase 2, `FULL` update mode) |
| `retired` | recorded locally, but **Discovery is not notified**, since the push action has no retire directive yet | Discovery exposes a retire mechanism (e.g. `updateMode: DELETE` or catalog `status: RETIRED`), then `retire` pushes before settling and becomes a sync variant |

## Where this lives in code

All events are minted in one file,
[`internal/crawler/runner/telemetry.go`](internal/crawler/runner/telemetry.go),
one helper per event, so this catalog cannot drift from the code. The persisted
enums Discovery and the DB care about (`SyncOutcome`, `CatalogStatus`) live in
[`internal/crawler/catalog/status.go`](internal/crawler/catalog/status.go), a
store contract kept separate from logging.

---

# Metrics and traces

The crawler emits the metric set below through the `runner.Metrics` port. The
OTel adapter is in
[`internal/crawler/telemetry.go`](internal/crawler/telemetry.go). It stays inert
on a no-op meter until a real sink is injected, so the engine stays
framework-agnostic. The traces below are not built yet.

## What the crawler is for

The crawler keeps the Discovery Service an **accurate, current mirror of what
publishers publish**. Every metric is a leading indicator of one question:

> Is Discovery fresh and complete relative to the publishers?

If the crawler falls behind (stale) or drops catalogs (incomplete), consumers on
Discovery see out-of-date or missing inventory. That is the impact the metrics
exist to protect.

## Two lenses

| Ops asks | Business asks |
|---|---|
| Is it alive and keeping up? Is it stuck? When it breaks, what, where, and whose fault? Do I page someone? | Are consumers seeing **fresh, complete** catalogs? How much inventory is **missing** from Discovery right now? Are we meeting the freshness SLA? |

## The metric set

Naming convention: `crawler_<name>`, counters end `_total`, durations end
`_seconds` (histogram). **Labels are bounded-cardinality only** (`job`,
`outcome`, `fault`, `result`). Never `catalog_id`, `index_url`, or versions.
Those are high-cardinality and belong in traces and logs.

### Tier 1, page-worthy

| metric | type | labels | persona | answers | alert |
|---|---|---|---|---|---|
| `crawler_seconds_since_last_success` | gauge | `job=crawl\|sync` | ops and biz | Is it alive and keeping up? Disambiguates the empty-queue trap: `queue_depth = 0` on its own means either "all caught up" or "the crawler died and stopped enqueuing" | `> 3x interval` |
| `crawler_sync_outcome_total` | counter | `outcome`, `fault` | ops and biz | Success rate **and why it fails**, so it routes to who to call | failure ratio `> 5%` (tune) |
| `crawler_catalogs_parked` | gauge | none | biz and ops | How much inventory is **missing from Discovery right now** | `> 0` sustained |
| `crawler_queue_depth` | gauge | none | ops | Falling behind? | sustained growth, or `> N` for `> M` min |

`outcome` is one of `pushed`, `skipped`, `retired`, `partial`, `faulted`. On
`faulted`, the `fault` label routes the page (see the table below). The other
outcomes carry `fault=""`.

### Tier 2, important

| metric | type | labels | persona | answers |
|---|---|---|---|---|
| `crawler_sync_lag_seconds` | histogram | none | biz | the **freshness SLA**: queue residence, `synced_at - enqueued_at` |
| `crawler_push_latency_seconds` | histogram | none | ops | Discovery responsiveness |
| `crawler_catalogs_tracked` | gauge | none | biz | **coverage**, how much are we serving |
| `crawler_index_poll_total` | counter | `result=updated\|unchanged\|not_modified\|unreachable` | ops | per-source reachability, a broken publisher shows as `unreachable` |
| `crawler_index_crawl_seconds` | histogram | none | ops | index-crawl latency |

### Not built yet

Retries per second (retry-storm detection), batches per push, artifact-size
histogram.

## Which fault means who to call

`crawler_sync_outcome_total{outcome=faulted, fault=...}` turns an error spike
into a routing decision:

| `fault` | whose problem | action |
|---|---|---|
| `push_rejected` (4xx) | **Discovery**, rejecting our payloads (schema drift?) | call the Discovery team |
| `transient` / 5xx | **Discovery or infra**, down or slow | check infra |
| `digest_mismatch` / `decode` / `gap` / `content_invalid` | the **publisher's data** is bad | contact the publisher |
| `signature` | the file is **not authentic**, or the **registry** does not hold the key it names | check the registry for the publisher's `keyId` first, then contact the publisher |
| `oversize` | payload cap too small, or a huge catalog | tune the byte caps, or talk to the publisher |
| `store` | **our DB** is unhealthy | check the crawler's Postgres |
| `absent` | catalog vanished from the index mid-flight | usually self-heals |
| `ssrf` | publisher URL resolves to a private address | contact the publisher |
| `index_fetch` | publisher index unreachable | check the publisher, retries with backoff |
| `push_schema` | our push body failed schema validation | check schema drift against the push action |

## Alerts, starter set

| alert | condition | severity |
|---|---|---|
| Crawler wedged | `crawler_seconds_since_last_success{job=sync} > 3x CRAWLER_CATALOG_INTERVAL` | page |
| Backlog growing | `crawler_queue_depth` rising for `> 15m` and not draining | page |
| High failure rate | `rate(crawler_sync_outcome_total{outcome=faulted}) / rate(crawler_sync_outcome_total) > 5%` for `10m` | page, route by top `fault` |
| Inventory stuck | `crawler_catalogs_parked > 0` for `> 30m` | ticket, business-visible |
| Source down | `rate(crawler_index_poll_total{result=unreachable})` sustained per source | ticket |
| Freshness SLA | `histogram_quantile(0.95, crawler_sync_lag_seconds) > SLA` | ticket or page |

## The `runner.Metrics` port

Defined in
[`internal/crawler/runner/ports.go`](internal/crawler/runner/ports.go):

```go
type Metrics interface {
	RecordSyncOutcome(outcome, fault string) // one labeled counter
	MarkPassSuccess(job string)              // liveness: last completed tick per job
	SetQueueDepth(n int)                     // backlog gauge
	SetCatalogsParked(n int)                 // gauge of permanently-failed items
	SetCatalogsTracked(n int)                // gauge of catalogs we track
	ObservePushSeconds(seconds float64)      // push latency
	ObserveIndexSeconds(seconds float64)     // index-crawl latency
	ObserveSyncLagSeconds(seconds float64)   // queue residence: synced_at - enqueued_at
	RecordIndexPoll(result string)           // updated/unchanged/not_modified/unreachable
}
```

`NopMetrics` is the default so the engine stays framework-agnostic. The OTel
adapter implements this and is injected by the composition root. In the onix
plugin that happens in [`crawler.go`](crawler.go), which builds it off
`otel.Meter("crawler")` and falls back to `NopMetrics` if the meter cannot
create its instruments.

## Traces

The crawler is a **background poller, not request-driven**, so traces are not the
primary tool. The structured logs above, with `run_id` and `pass_id`, already
answer most "what happened to catalog X". Traces earn their place only for the
latency and failure breakdown of one sync. The intended shape:

- `crawler.sync`, a root span per catalog, with attributes `catalog_id`, `from`,
  `to`, `outcome`. Child spans: `resolve` (fetch index) -> `pull` (files,
  `bytes`) -> `verify` -> `build_push` -> `push` (per batch, `http_status`). It
  answers "where did this catalog's 8 seconds, or its 400, go?".
- `crawler.crawl`, a root span per tick, with a child span per index (`fetch`,
  `304?`).

High-cardinality attributes (`catalog_id`, URLs, versions) belong **on spans**,
never as metric labels. Correlate traces to logs via `run_id` and `pass_id`.

Metrics come first. Build the Tier-1 metrics, then add the two span trees when
per-catalog latency forensics are needed.

---

## Known open items

- Signature verification of the index document itself, so `participantId`,
  `catalogType` and `visibleTo` stop being publisher-asserted.
- The registry as an **index source**, so a crawler can be told what to crawl
  instead of being handed `CRAWLER_INDEX_URLS`. `CRAWLER_REGISTRY_URL` is
  rejected at startup until then.
- A `signature` case in `stepPhrase`, so the failure sentence stops saying
  "couldn't send the catalog to Discovery" for a file that was never fetched.
- `authMethods` signed-request download gate.
- `catalogType`-based filtering.
- Removals and retirals applied downstream (see the known-gaps table above).
- Traces (the two span trees above).

Publisher key resolution is **not** on this list. Keys come from the registry
today, and there is no fallback path and no configuration surface for them.

See [`pkg/catalogfile`](../../../catalogfile) for the shared change-file
application logic the crawler uses to compose a catalog's current content from
its baseline plus every change file.
