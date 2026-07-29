# Crawler Logs

> **Status: implemented.** This is the logging model the crawler emits as of
> now — `runner/telemetry.go` mints exactly these components, stages, messages,
> levels, and fields. This doc is a description of current output, not just a
> target.

## 1. The model: one process, two jobs

The crawler is **one process** running **two jobs** linked by a durable queue:

```
        every ~5m                         every ~30s
   ┌── CRAWL job ──┐                 ┌── SYNC job ──┐
   │ poll indexes  │──Enqueue──►[ queue ]──ClaimNext──►│ pull→unpack→ │──► Discovery
   │ find changes  │                                   │ verify→push  │
   └───────────────┘                 └──────────────┘
     (producer)                                          (consumer)
```

So there are exactly **three log components**:

| component | what it is | role |
|---|---|---|
| `daemon` | the process | starts / stops the two jobs |
| `crawl`  | job 1      | polls publisher indexes, queues catalogs that changed |
| `sync`   | job 2      | takes a queued catalog and syncs it to Discovery |

## 2. The shape of every log line

Every line carries five things:

```
component · stage · message · {attributes} · {stats}
```

| part | field(s) | answers | example |
|---|---|---|---|
| **component** | `component` | which subsystem? | `sync` |
| **stage** | `stage` | where in it? | `failed` |
| **message** | `msg` | what happened? (natural sentence) | "couldn't send the catalog to Discovery — 503; will retry (attempt 2 of 5)" |
| **attributes** | e.g. `catalog_id`, `index_url`, `fault` | *which one* / how to filter | `catalog_id=…/electronics-2025` |
| **stats** | e.g. `resources`, `dur_ms`, `synced` | *how much* / is it healthy | `resources=12 batches=2` |

Rule of thumb: **attributes = which one; stats = how much.**

- **Event key** is always `crawler.<component>.<stage>` (e.g. `crawler.sync.failed`).
- **Base attributes on every line:** `component`, `stage`, `msg`, `run_id`.
- **Every `crawl` line also carries:** `trigger` (`scheduled` or `on_demand`) — a
  scheduled tick and a `/crawl` on-demand trigger both log through the same
  `crawl` component, so this is how to tell which one you're looking at
  without cross-referencing `run_id` against the `/crawl` HTTP response.
  `sync` has no on-demand variant, so its lines don't carry this.
- **Sync per-catalog lines also carry:** `pass_id`, `catalog_id`, `from`, `to`.

### Correlation ids
| id | scope | use |
|---|---|---|
| `run_id` | one job tick | follow everything one crawl/sync tick did |
| `pass_id` | one catalog's sync | follow one catalog through its sync |
| `catalog_id` | a catalog | follow one catalog across ticks |

### On-demand crawls (`POST /crawl`)

`/crawl` runs an immediate single-index crawl through the same `crawl`
component and the same `logCrawlFinished` summary a scheduled tick uses —
`trigger=on_demand` is the only thing that marks it as one. The HTTP response
returns `runId` (the same `run_id` on these lines) so an operator can grep for
their specific request's log lines instead of guessing from interleaving.

## 3. Levels

| level | when | examples |
|---|---|---|
| `INFO` | milestones + per-tick summaries | `daemon.ready`, `crawl.finished`, `sync.finished` |
| `DEBUG` | per-item detail | `crawl.polled`, `crawl.queued`, `sync.syncing`, `sync.synced` |
| `WARN` | recoverable — will self-heal | transient sync failure (will retry), index unreachable |
| `ERROR` | needs a human | permanent sync failure (parked), daemon start failure, DB unhealthy |

At `INFO`, a healthy crawler is quiet: `daemon.ready` once, then one `finished` line per active job tick (`crawl.finished`, `sync.finished`). An idle tick logs nothing.

## 4. Components & stages

### `daemon` — the process
| stage | level | message | attributes | stats |
|---|---|---|---|---|
| `ready` | INFO | "crawler started — polling N source(s) every 5m, pushing to `<host>`" | source_mode, push_host | sources, index_interval, catalog_interval, max_attempts |
| `stopping` | INFO | "crawler stopping" | — | — |
| `stopped` | INFO | "crawler stopped" | — | — |
| `failed` | ERROR | "crawler failed to start while opening the database: `<err>`" | at (`db_open`/`db_migrate`/`config`/`start`), error | — |

### `crawl` — job 1 (find work)

Every stage below also carries `trigger` (`scheduled` or `on_demand`) — omitted
from the attributes column since it's on all of them.

| stage | level | message (varies by result) | attributes | stats |
|---|---|---|---|---|
| `polled` | DEBUG · WARN | "index unchanged" · "index updated to v5" · "index not modified (304)" · **WARN** "couldn't reach the index: `<err>`" | index_url, version, result (`unchanged`/`updated`/`not_modified`/`unreachable`) | — |
| `queued` | DEBUG | "queued this catalog to sync (v3 → v5)" · "queued this catalog to retire" | catalog_id, op (`sync`/`retire`), from, to | — |
| `finished` | INFO | "crawl finished — polled 1 index, 1 updated, queued 1 catalog" | — | indexes, updated, queued, dur_ms |
| `failed` | ERROR | "crawl error while `<op>`: `<err>`" — source-resolve failures and DB errors during a crawl | at/operation, error | — |

*(A version rollback is a rare `polled` WARN: "index version went backwards — ignored". Store errors surface as component=`crawl`/`sync` stage=`failed` fault=`store`.)*

### `sync` — job 2 (do the work)
| stage | level | message | attributes | stats |
|---|---|---|---|---|
| `syncing` | DEBUG | "syncing catalog (v3 → v5)" | catalog_id, from, to | — |
| `synced` | DEBUG | "sent the catalog update to Discovery" | catalog_id, mode | resources, offers, batches, dur_ms |
| `skipped` | DEBUG | "nothing to send — this update only removed items, and removals aren't applied yet" | catalog_id, reason | — |
| `retired` | DEBUG | "recorded the catalog as retired locally — Discovery not notified yet (Phase 2)" | catalog_id | — |
| `failed` | WARN · ERROR | see below | catalog_id, step, fault, http_status, will_retry, attempt, error | — |
| `finished` | INFO | "sync finished — 1 sent, 1 skipped, 0 failed, 0 retrying; queue empty" | — | synced, skipped, failed, retrying, queue, dur_ms |

**`failed` message** is built to explain *where* it broke and *whether it recovers*:

- WARN (transient): *"couldn't send the catalog to Discovery — 503; will retry (attempt 2 of 5)"*
- ERROR (permanent): *"couldn't verify the downloaded files — digest mismatch; parked, won't retry until the publisher publishes a new version"*

The *where* comes from the fault, so no code needs decoding:

| fault | message says "couldn't **…**" |
|---|---|
| `index_fetch` / `absent` | resolve the catalog |
| `ssrf` | download the files |
| `decode` / `gap` | unpack the files |
| `digest_mismatch` | verify the downloaded files |
| `oversize` | batch the catalog |
| `content_invalid` | build the push request |
| `push_schema` / `push_rejected` / transient (5xx) | send the catalog to Discovery |
| `store` | save progress |

## 5. Reading the logs

| question | filter |
|---|---|
| Is the crawler healthy? | `component=crawl stage=finished` + `component=sync stage=finished` — watch `failed`/`retrying`/`queue` |
| What happened to one catalog? | `catalog_id=<id>` → `queued → syncing → synced` (or `failed`) |
| What did one sync attempt do? | `pass_id=<id>` |
| What did one tick do? | `run_id=<id>` |
| Anything broken? | `stage=failed` (ERROR = investigate, WARN = will retry) |

## 6. Example streams

**Healthy cycle (at DEBUG):**
```
daemon ready     "crawler started — 1 source, polling every 5m → discovery.local"
crawl  polled    "index updated to v5"                              index_url=… trigger=scheduled
crawl  queued    "queued this catalog to sync (v3 → v5)"            catalog_id=…/electronics-2025 trigger=scheduled
crawl  finished  "crawl finished — polled 1 index(es), 1 updated, queued 1 catalog(s)"   {indexes=1 updated=1 queued=1 dur_ms=142} trigger=scheduled
sync   syncing   "syncing catalog (v3 → v5)"                        catalog_id=…/electronics-2025
sync   synced    "sent the catalog update to Discovery"             catalog_id=…/electronics-2025 {resources=12 offers=4 batches=2 dur_ms=180}
sync   finished  "sync finished — 1 sent, 0 skipped, 0 failed, 0 retrying; queue empty"   {synced=1 skipped=0 failed=0 retrying=0 queue=0 dur_ms=318}
```

**A permanent failure (visible at INFO):**
```
sync   failed    "couldn't send the catalog to Discovery — rejected (400); parked, won't retry until a new version is published"
                 catalog_id=…/electronics-2025 step=push fault=push_rejected http_status=400 will_retry=false
```

## 7. Known gaps reflected in the logs

Two `sync` results exist only because Phase-1 defers removals; both are honest in their messages:

| result | why | closes when |
|---|---|---|
| `skipped` | the update was removal-only, and removals aren't applied yet | removals land (Phase 2, FULL update mode) |
| `retired` | recorded locally but **Discovery is not notified** — `/catalog/push` has no retire directive yet | Discovery exposes a retire mechanism (e.g. `updateMode: DELETE` or catalog `status: RETIRED`); then `retire` pushes before settling and becomes a sync variant |

## 8. Where this lives in code

All events are minted in one file — **`pkg/crawler/runner/telemetry.go`** — one helper per event, so this catalog can't drift from the code. The persisted enums Discovery/DB care about (`SyncOutcome`, `CatalogStatus`) live in `pkg/crawler/catalog/` (a store contract, separate from logging).
