# Crawler Metrics & Traces

> **Status: metrics implemented; traces pending.** The crawler emits the metric
> set below through the `runner.Metrics` port (OTel adapter in
> `pkg/crawler/telemetry.go`); it stays inert on a no-op meter until a real sink
> (e.g. the onix OTLP plugin) is injected. The traces in §7 are not built yet.
> Companion to `docs/crawler-logs.md`.

## 1. What the crawler is for (the North Star)

The crawler keeps the Discovery Service an **accurate, current mirror of what
publishers publish**. Every metric is a leading indicator of one question:

> *Is Discovery fresh and complete relative to the publishers?*

If the crawler falls behind (stale) or drops catalogs (incomplete), consumers on
Discovery see out-of-date or missing inventory — that is the business impact the
metrics exist to protect.

## 2. Two lenses

| Ops asks | Business asks |
|---|---|
| Is it alive & keeping up? Is it stuck? When it breaks — *what, where, whose fault*? Do I page someone? | Are consumers seeing **fresh, complete** catalogs? How much inventory is **missing** from Discovery right now? Are we meeting the freshness SLA? |

## 3. Why the original port fell short (the motivation)

The first cut of the port had 5 signals — `CatalogPushed()`, `CatalogFailed(reason)`,
`SetQueueDepth(n)`, `ObservePushSeconds`, `ObserveIndexSeconds` — the crawler's
**plumbing**, not whether it's **delivering**. Three critical blind spots drove
the redesign in §4/§8:

1. **No freshness/liveness signal.** `queue_depth = 0` is *ambiguous* — it means
   either "all caught up ✅" or "the crawler died and stopped enqueuing ☠️". The
   current metrics cannot tell a wedged crawler from a healthy idle one. **This
   is the single most important gap.**
2. **No standing "missing inventory" number.** Failures are only a *counter* (a
   rate), not a *gauge* of how many catalogs are stuck/missing from Discovery
   right now — the number the business cares about.
3. **No clean success rate.** Success and failure are separate counters with no
   total and no `skipped`/`retired`/`partial`, so you can't compute a real
   outcome distribution.

## 4. The critical metrics

Naming convention: `crawler_<name>` · counters end `_total` · durations end
`_seconds` (histogram). **Labels are bounded-cardinality only** (`job`,
`outcome`, `fault`, `result`) — never `catalog_id`, `index_url`, or versions
(those are high-cardinality → they belong in traces/logs, §7).

### Tier 1 — page-worthy (build first)

| # | metric | type | labels | persona | answers | alert |
|---|---|---|---|---|---|---|
| 1 | `crawler_seconds_since_last_success` | gauge | `job=crawl\|sync` | ops + biz | Is it alive & keeping up? (disambiguates the empty-queue trap) | `> 3 × interval` |
| 2 | `crawler_sync_outcome_total` | counter | `outcome`, `fault` | ops + biz | Success rate **and why it fails** → who to call | failure ratio `> 5%` (tune) |
| 3 | `crawler_catalogs_parked` | gauge | — | biz + ops | How much inventory is **missing from Discovery right now** | `> 0` sustained |
| 4 | `crawler_queue_depth` *(have)* | gauge | — | ops | Falling behind? | sustained growth / `> N` for `> M`min |

`outcome ∈ {pushed, skipped, retired, partial, faulted}`. On `faulted`, the
`fault` label routes the page (§5). The other outcomes carry `fault=""`.

### Tier 2 — important

| # | metric | type | labels | persona | answers |
|---|---|---|---|---|---|
| 5 | `crawler_sync_lag_seconds` | histogram | — | biz | the **freshness SLA** — publisher bump → live in Discovery (needs index publish-time; approximate with #1 until then) |
| 6 | `crawler_push_seconds` *(have)* | histogram | — | ops | Discovery responsiveness |
| 7 | `crawler_catalogs_tracked` / `crawler_sources_total` | gauge | — | biz | **coverage** — how much are we serving |
| 8 | `crawler_index_poll_total` | counter | `result=updated\|unchanged\|not_modified\|unreachable` | ops | per-source reachability — a broken publisher shows as `unreachable` |

### Tier 3 — refinement
`crawler_index_seconds` *(have)*, retries/sec (retry-storm detection),
batches-per-push, artifact-size histogram.

## 5. The "who to call" table (why the `fault` label matters)

`crawler_sync_outcome_total{outcome=faulted, fault=…}` turns an error spike into a
routing decision:

| `fault` | it's whose problem | action |
|---|---|---|
| `push_rejected` (4xx) | **Discovery** — rejecting our payloads (schema drift?) | call the Discovery team |
| `transient` / 5xx | **Discovery / infra** — down or slow | check infra |
| `digest_mismatch` / `decode` / `gap` / `content_invalid` | the **publisher's data** is bad | contact the publisher |
| `oversize` | payload cap too small or a huge catalog | tune `MaxPushBytes` / publisher |
| `store` | **our DB** is unhealthy | check the crawler's Postgres |
| `absent` | catalog vanished from the index mid-flight | usually self-heals |

## 6. Alerts (starter set)

| alert | condition | severity |
|---|---|---|
| **Crawler wedged** | `crawler_seconds_since_last_success{job=sync} > 3× CatalogInterval` | page |
| **Backlog growing** | `crawler_queue_depth` rising for `> 15m` (not draining) | page |
| **High failure rate** | `rate(sync_outcome_total{outcome=faulted}) / rate(sync_outcome_total)` `> 5%` for `10m` | page (route by top `fault`) |
| **Inventory stuck** | `crawler_catalogs_parked > 0` for `> 30m` | ticket (biz-visible) |
| **Source down** | `rate(index_poll_total{result=unreachable})` sustained per source | ticket |
| **Freshness SLA** | `histogram_quantile(0.95, sync_lag_seconds) > SLA` | ticket/page |

## 7. Traces (secondary — do metrics first)

The crawler is a **background poller, not request-driven**, so traces are *not*
the primary tool — the structured logs (with `run_id`/`pass_id`, see
`docs/crawler-logs.md`) already answer most "what happened to catalog X". Traces
earn their place only for the **latency/failure breakdown of one sync**:

- **`crawler.sync`** (root span, per catalog): attrs `catalog_id`, `from`, `to`,
  `outcome`. Child spans: `resolve` (fetch index) → `pull` (files, `bytes`) →
  `verify` → `build_push` → `push` (per batch, `http_status`). Answers *"where
  did this catalog's 8 seconds — or its 400 — go?"*
- **`crawler.crawl`** (root span, per tick): child span per index (`fetch`,
  `304?`).

High-cardinality attributes (`catalog_id`, urls, versions) live **on spans**,
never as metric labels. Correlate traces↔logs via `run_id` / `pass_id`.

**Priority: metrics ≫ traces.** Build Tier-1 metrics first; add the two span
trees when you need per-catalog latency forensics.

## 8. The `runner.Metrics` port (implemented)

The port collapses the two outcome counters into one labeled counter and adds the
liveness/standing-state signals (OTel adapter in `pkg/crawler/telemetry.go`;
`NopMetrics` is the default so the module stays framework-agnostic):

```go
type Metrics interface {
    RecordSyncOutcome(outcome, fault string)   // #2 — replaces CatalogPushed + CatalogFailed
    MarkPassSuccess(job string)                // #1 — drives seconds_since_last_success
    SetCatalogsParked(n int)                   // #3 — refreshed from the store each tick
    SetCatalogsTracked(n int)                  // #7
    SetQueueDepth(n int)                        // keep (#4)
    ObservePushSeconds(seconds float64)         // keep (#6)
    ObserveIndexSeconds(seconds float64)        // keep (Tier 3)
    ObserveSyncLagSeconds(seconds float64)      // #5 — when index publish-time is available
    RecordIndexPoll(result string)              // #8
}
```

`NopMetrics` stays the default so the module remains framework-agnostic; a real
OTLP adapter implements this and is injected by the composition root.

## 9. Bottom line

The current metrics show the crawler's *plumbing* but not whether it's
*delivering*. The three additions that matter most:

1. **`seconds_since_last_success`** — detect a wedged crawler (the empty-queue trap).
2. **`sync_outcome_total{outcome, fault}`** — a real error rate *with cause* (who to call).
3. **`catalogs_parked`** — quantify inventory currently missing from Discovery.

Everything else is refinement.
