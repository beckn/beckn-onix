# catalogcrawler

Thin onix plugin adapter over the framework-agnostic crawler engine in
[`pkg/catalogcrawler`](../../../catalogcrawler). It implements
`definition.Crawler`: it walks a publisher's catalog **index -> baseline +
change files** per the Decentralized Catalog **"File Specifications"** tab,
resolves each catalog to its current content, and pushes the result to a
Discovery service. This is the consuming side of the same chain
[`catalogpublisher`](../catalogpublisher) produces -- see the file spec for
the full protocol background.

The plugin is deliberately thin: it reads config from its plugin config map
(with **env overriding**, so secrets like the DB DSN stay out of YAML),
opens + migrates the Postgres state, injects an optional schema validator,
starts the engine's scheduled jobs, and returns a closer that stops them.
All the actual crawl logic lives in the engine package.

## What the crawler does

The engine runs as **two scheduled background jobs** plus an **on-demand
trigger** (`definition.Crawler`):

1. **Index job** (every `CRAWLER_INDEX_INTERVAL`, default `5m`) fetches each
   configured publisher index and, for every catalog in it, runs change
   detection against a stored per-catalog version cursor
   (`pkg/catalogcrawler/change.go`):
   - `sync` -- content advanced (new, or `latestVersion > cursor`): enqueue a
     sync to the catalog job.
   - `skip_unchanged` -- cursor already at the latest version: nothing to do.
   - `retire` -- the entry went `RETIRED` (a tombstone): retire it downstream.
   - `rollback` -- version went **backwards**: flagged and **not applied**
     (the file spec's monotonic-version rule).

   Enqueue is coalescing and the queue is keyed by catalog id; file URLs are
   read from the stored index, not from the queue row.

2. **Catalog job** (every `CRAWLER_CATALOG_INTERVAL`, default `30s`) claims
   queued items (`FOR UPDATE SKIP LOCKED`) and for each:
   - **Selects** whether to carry it and computes `visibleTo`
     (`select.go`): a **public** catalog (no `networkIds`) is always taken
     and visible to everyone; a **network-scoped** catalog is taken only if
     its networks intersect the crawler's configured `CRAWLER_NETWORK_IDS`,
     and its own networks flow through as `visibleTo`.
   - **Resolves** the full catalog at the target version (`resolve.go`):
     fetch the `baseline`, then fold each `changes[]` entry in version order
     via [`pkg/catalogfile`](../../../catalogfile). No composed state is kept
     locally, so a changed catalog is always resolved in full.
   - **Schema-validates** the push body against the `catalog/push` action
     (optional, via the injected `schemaValidator`).
   - **Pushes** to Discovery (`publish/`): a `catalog/push` context plus
     `message.catalogs` and a matching `publishDirectives` entry carrying
     `updateMode=FULL` and `visibleTo`. `FULL` is a replace -- resources
     absent from the pushed doc are removed downstream, so retirals and
     removals are handled by the replace rather than by explicit deletes.
   - On failure, retries with backoff up to `CRAWLER_MAX_ATTEMPTS`, and rolls
     the per-catalog `sync_status` up to `ok` / `partial` / `failed`.

3. **On-demand `/crawl`** re-crawls one index immediately (supportability for
   a stuck publisher) -- see below. It pokes an immediate index pass; the
   scheduled jobs still do the actual sync + push.

State lives in Postgres (`crawler_index` / `crawler_queue` /
`crawler_catalog`); the engine migrates these on start.

## Source resolution

The set of publisher indexes to crawl comes from one of:

- `CRAWLER_INDEX_URLS` -- an explicit, comma-separated list (config-driven),
  **or**
- `CRAWLER_REGISTRY_URL` -- resolved from a registry.

At least one is required. An on-demand `/crawl` request can also name a
single `indexUrl` to crawl right now.

## Verification in this phase

Fetched publisher content is checked for:

- **Digest** -- every catalog file's bytes are matched against its declared
  `sha-256:<hex>` digest before use (`http.go`).
- **SSRF guard** -- publisher URLs that resolve to loopback / private /
  link-local addresses are refused (untrusted content; the push endpoint is
  operator config and is exempt).
- **Size cap** -- `CRAWLER_MAX_ARTIFACT_BYTES` per fetched artifact.

**Per-file and manifest signature verification is deferred to Phase 2.** The
index carries the signed tuple `{catalogId, version, url, digest, validUntil}`
per file (`model.go`), and it is parsed and carried through, but it is **not
verified** in this phase. See [`catalogpublisher`](../catalogpublisher) for
the producing side and `pkg/security/artifactsigner` /
`pkg/security/artifactverifier` for the sign/verify primitives that Phase 2
will wire in here.

## Deliberately not done in this phase

- **No signature verification** (digest only -- see above).
- **`authMethods` (signed-request download gate) not exercised.** Parsed but
  unused; Phase 1 takes only openly-fetchable files.
- **No `catalogType` filtering.** Parsed and carried, but nothing skips e.g. a
  `MASTER` catalog yet.

## Config

Config is read from the plugin config map, with matching env vars overriding
(so secrets stay in env). Both this plugin and the standalone driver build the
same `Settings` (`pkg/catalogcrawler/config.go`).

| Key | Meaning | Required | Default |
|---|---|---|---|
| `CRAWLER_DB_DSN` | Postgres DSN for crawler state | **yes** | -- |
| `CRAWLER_PUSH_ENDPOINT` | Discovery `/push` endpoint | **yes** | -- |
| `CRAWLER_INDEX_URLS` | Comma-separated publisher index URLs | one of these two | -- |
| `CRAWLER_REGISTRY_URL` | Registry to resolve indexes from | one of these two | -- |
| `CRAWLER_NETWORK_IDS` | Networks this crawler is a member of (for scoped catalogs) | no | (public only) |
| `CRAWLER_BPP_URI` | Publisher URI stamped into the push context | no | -- |
| `CRAWLER_INDEX_INTERVAL` | Index-job interval (Go duration) | no | `5m` |
| `CRAWLER_CATALOG_INTERVAL` | Catalog-job interval (Go duration) | no | `30s` |
| `CRAWLER_FETCH_TIMEOUT` | Per-fetch timeout (Go duration) | no | `30s` |
| `CRAWLER_MAX_ARTIFACT_BYTES` | Byte cap per fetched artifact | no | `10485760` (10 MiB) |
| `CRAWLER_MAX_ATTEMPTS` | Max sync attempts before `failed` | no | `5` |

`CRAWLER_DB_DSN` and `CRAWLER_PUSH_ENDPOINT` are secret / deploy-specific and
should come from env; the non-secret keys can live in the YAML config block.
Env always wins.

## Running it locally

The `crawl` module is wired alongside the existing `bapTxnReceiver` /
`bapTxnCaller` modules in
[`config/local-beckn-one-bap.yaml`](../../../../config/local-beckn-one-bap.yaml)
(the `crawl` block, path `/crawl`). Build the plugin, provide the secrets via
env, and run the adapter:

```bash
./install/build-plugins.sh        # builds plugins/catalogcrawler.so, among others

export CRAWLER_DB_DSN="postgres://user:pass@localhost:5432/crawler?sslmode=disable"
export CRAWLER_PUSH_ENDPOINT="https://discovery.local/beckn/catalog/push"

go run ./cmd/adapter --config=config/local-beckn-one-bap.yaml
```

The scheduled index + catalog jobs start in the background at plugin init.
To poke an **immediate** re-crawl of one index (returns `202 Accepted`; the
crawl runs asynchronously and results surface through telemetry, not the
response):

```bash
curl -X POST http://localhost:8081/crawl \
  -H "Content-Type: application/json" \
  -d '{"indexUrl": "https://cdn.publisher.example.com/beckn/catalog-index.json"}'
# -> 202 {"status":"ACCEPTED","indexUrl":"..."}
```

`participantId` is accepted in the `/crawl` body for forward-compatibility
with DID resolution but is not yet used.

## Standalone driver

The same engine can be run without onix as a config-driven worker
([`cmd/catalog-crawler`](../../../../cmd/catalog-crawler)) -- all config comes
from `CRAWLER_*` env, it migrates the DB, runs the two scheduled jobs in the
foreground, and stops cleanly on SIGINT / SIGTERM:

```bash
export CRAWLER_DB_DSN="postgres://user:pass@localhost:5432/crawler?sslmode=disable"
export CRAWLER_PUSH_ENDPOINT="https://discovery.local/beckn/catalog/push"
export CRAWLER_INDEX_URLS="https://cdn.publisher.example.com/beckn/catalog-index.json"

go run ./cmd/catalog-crawler
```

## Trying it against a live fixture

There is no currently-hosted live fixture in this shape. To stand up your own:
publish with [`catalogpublisherctl`](../../../../cmd/catalogpublisherctl) into
a directory, serve that directory over plain HTTP, and point
`-domain` at that server before publishing so the written URLs are actually
fetchable (not `file://`):

```bash
go run ./cmd/catalogpublisherctl \
  -catalog pkg/plugin/implementation/catalogpublisher/testdata/sample-catalog-v1.json \
  -out /tmp/catalog-demo -domain localhost:8000

cd /tmp/catalog-demo && python3 -m http.server 8000
```

Then point `CRAWLER_INDEX_URLS` (or a `/crawl` request's `indexUrl`) at the
index that publisher wrote under `http://localhost:8000`. Note the SSRF guard
refuses private/loopback hosts in production; the crawler's own round-trip
tests exercise this over an in-process `httptest` server with the private-host
guard relaxed (`NewHTTPClient(..., allowPrivate=true)`), so no manual serving
is needed there.

## Known open items

- Phase-2 signature verification (per-file tuple + manifest proof).
- `authMethods` signed-request download gate.
- `catalogType`-based filtering.
- Registry-source resolution hardening.

See [`pkg/catalogfile`](../../../catalogfile) for the shared change-file
application logic both `catalogpublisher`'s CLI and this crawler use to
compose a catalog's current content from its baseline plus every change file.
