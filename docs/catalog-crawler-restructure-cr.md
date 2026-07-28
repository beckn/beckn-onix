# Change Request: Restructure `pkg/crawler` + Observability Foundation

| | |
| :--- | :--- |
| **CR ID** | CC-RESTRUCTURE-001 |
| **Status** | Proposed |
| **Type** | **PR-A**: structural refactor (behavior-preserving) · **PR-B**: observability foundation (real change) |
| **Component** | `pkg/crawler` (+ `pkg/plugin/implementation/crawler`, `cmd/crawler`) |
| **Author** | — |
| **Reviewers** | — |
| **Related** | `docs/catalog-crawler-phase1-review.md`, `docs/catalog-crawler-gzip-support-review.md`, ION reference ("Decentralized Catalog: Example (ION)") |

---

## 1. Summary

Reorganize the crawler from one flat package into a domain-oriented layout (~6–7 packages) with intent-revealing names, split the 576-line orchestrator, and put observability (OpenTelemetry → ClickStack) on a proper footing. **Ship as two PRs: PR-A is a behavior-preserving structural move; PR-B is the observability change** (new instruments, correlation ids, error taxonomy) with a dual-emit metric migration so live dashboards don't break.

This design is the synthesis of a three-lens architecture review (§3). It unblocks the roadmap — registry discovery, scope enforcement, restricted/authMethods fetch, more codecs — by giving each a clear home, without over-packaging a 2.7k-line codebase.

## 2. Motivation

The package is a single flat namespace that is hard to navigate, debug, and extend:

- **24 `.go` files, ~2,783 lines, one `package crawler`, 115 exported symbols.**
- **`engine.go` is 576 lines** and does three jobs (lifecycle + index pass + catalog pass) plus push helpers and a string-splitting error classifier (`reasonCategory`, `engine.go:562`). Every named failure mode — claim/lease races, push nacks, decode bombs, SSRF — surfaces inside two God-functions (`crawlIndex`, `processItem`), so triage means grepping one giant file.
- Pure rules (`change`, `select`, `resolve`) already import nothing internal, but sit beside I/O (`http`, `push`, `source`), config, codec, and orchestration, with **no expressed dependency direction**.
- Names describe *mechanism* (`http`), *layer* (`model`), or are vague (`engine`, `state`, `select`).

Note: recent commits already implemented much of the review backlog (two size caps, codec registry, CGNAT SSRF coverage, conditional-GET, MERGE/FULL push modes, size-based batching, queue claim tokens). **This CR is about where that code lives and how it's named — and about making observability real — not re-doing the logic.**

## 3. How this design was chosen (three-lens review)

Three senior reviewers independently critiqued an earlier 10-package/single-PR draft; this CR is the synthesis:

| Lens | Core contribution kept |
| :--- | :--- |
| **Pragmatic Go** | 10 packages is over-engineering for 2.7k lines; ports/purity/composition-root **already exist inline**. → keep domain as **one** package, no separate `ports.go`, drop the artificial line-count rule, no premature `depguard`. Flagged: in-package tests reach unexported symbols, so the move is **not** "import-only." |
| **DDD / hexagonal** | "membership wins over declaration" (ION) is a **cross-authority** invariant; `access.go` (networkIds + authMethods + scopes) is a junk drawer. → express membership as a **port/seam now**, promote to its own package + anti-corruption layer **when scope enforcement lands** (not 4 empty contexts today). |
| **Operability** | The draft's "no behavior change" was false — it renamed **live** metrics. → **two PRs**; add a typed **`FaultClass`** taxonomy and **`RunID`/`PassID`** correlation; **co-locate** telemetry names per adapter; `telemetry/` = thin plumbing. |

Rejected: the 10-package split (over-structured), the 4-context domain split (premature), and a central `telemetry/events.go` vocabulary (recreates the junk drawer).

## 4. Goals / Non-goals

**Goals**
- Clear separation: pure domain ← adapters ← orchestration; observability as a leaf.
- Intent-revealing package and file names; the 576-line orchestrator split by job.
- Smaller public surface; explicit, low-ceremony extension points for the roadmap.
- Observability that actually works in ClickStack: consistent metric/trace/log vocabulary, a typed error taxonomy, and per-run correlation.

**Non-goals**
- **PR-A** changes no behavior, protocol, schema, or telemetry contract — moves/renames/splits only.
- **PR-B** *does* change the telemetry contract (new instruments, renamed metrics via dual-emit) — it is a reviewed change, not a "pure refactor."
- Not implementing roadmap features (registry, scope, authMethods) — only making room.
- Not converting decode to true streaming (the `io.ReadCloser` seam in `decode/` is preserved).

## 5. Naming principles

1. Name for **intent**, not mechanism/tech (`fetch` not `http`, `publish` not `push`, `store` not `state`).
2. No **layer/junk-drawer** names (`model.go`, `util.go`, `helpers.go`, `access.go`).
3. No **stutter** — package `catalog` → `catalog.Index`.
4. **Nouns** for type files, **verbs** for behavior files.
5. **Adapters mirror the port** they satisfy.
6. **Split by cohesion, not line count.** (No fixed line ceiling; honor the repo's 500-line guideline by splitting `engine.go`, but don't fragment cohesive files.)

## 5a. Function-name review (name ⇢ behavior)

A function name is a promise about what the function does; `add(a, b)` must only add. Reviewing the current functions against their bodies, the code is **mostly honest** — but there is one genuinely misleading cluster and a few smaller mismatches. Fix these as part of PR-A (mechanical, compiler-checked renames).

### The main offender: the failure-handling vocabulary

Five `fail*`-shaped names do five different things, and **two of them actually mean "retry"** — the opposite of "fail". A reader cannot tell park from retry from route from build by the name.

| Current (`file:line`) | What it actually does | Why the name misleads | Suggested |
| :--- | :--- | :--- | :--- |
| `failItem` (`engine.go:508`) | schedules a **retry** with backoff; only records after `MaxAttempts`; never advances the cursor | says "fail", **retries** — and is a sibling of `failPermanent` that does the *opposite* | `scheduleRetry` |
| `failPermanent` (`engine.go:494`) | **parks** the item (no retry), records, alerts | ok alone, but the `failItem`/`failPermanent` pair hides that the real axis is *transient vs permanent* | `parkPermanently` |
| `fail` (`engine.go:469`) | **routes** a failure to permanent-vs-transient | reads as "cause a failure"; it's a router | `routeFailure` |
| `failReport` (`engine.go:479`) | **builds and returns** a `PassReport` | verb-shaped name for a builder (a noun result) | `newFailureReport` |
| `FailQueueItem` (`state/queue.go:99`) | releases the claim, bumps `attempts`, sets `next_attempt_at` — i.e. **reschedules for retry** | says "fail", **reschedules** | `RescheduleQueueItem` |

After the rename the flow reads correctly: `routeFailure` sends a fault to either `parkPermanently` (store: `ParkQueueItem`) or `scheduleRetry` (store: `RescheduleQueueItem`). The transient-vs-permanent axis is finally visible in the names — one committed name each, no synonyms to choose between.

### Smaller mismatches

| Current (`file:line`) | Issue | Suggested |
| :--- | :--- | :--- |
| `Source.IndexURLs()` (`source.go:23`) | returns `[]IndexRef` (refs), **not** URL strings — the name promises the wrong type | `IndexRefs()` |
| `crawlIndex(…, force bool)` (`engine.go:210`) | **boolean-blindness** at call sites (`crawlIndex(ctx, ref, false)`); a bare `bool` says nothing | replace `force bool` with a named `bypassCadence bool` type or a two-value `trigger` enum |
| `processItem` (`engine.go:324`) | "process" is a junk verb, and it also **dispatches** retire-vs-sync before doing the sync | split the dispatch (`handleQueueItem`) from the sync path (`syncCatalog`) |
| `FetchFile` (`http.go:81`) | does GET **+ digest-verify + decode** — the name hides the integrity and decompression steps | `fetchVerifyDecode` — or keep `FetchFile` only if its doc comment states all three steps |
| `getCond` (`http.go:137`) | abbreviation | `getConditional` |
| `TouchIndex` (`state/state.go:126`) | "touch" is a known idiom but vague about *what* it advances (only `next_crawl_at`) | `AdvanceIndexCadence` |

### Second pass — type names (a name should also tell you what the *type* is)

| Current (`file:line`) | Problem | Suggested |
| :--- | :--- | :--- |
| `PartOutcome` (`retry.go`) | "Part" of *what*? It's the result of one push **batch** — the name doesn't say push, batch, or result | `BatchOutcome` |
| `Config` (`engine.go`) **and** `Settings` (`config.go`) | two config-shaped types, and the names don't say which is which (engine tunables vs env-loaded values) | keep `Settings` (env-loaded); rename the engine struct `EngineConfig` |
| `IndexCond` (`http.go`) | cryptic abbreviation for the conditional-GET validators | `IndexConditions` |
| `durOr` / `intOr` / `int64Or` (`config.go`) | "Or" is terse for "or default"; reads as an operator | `durationOrDefault` / `intOrDefault` / `int64OrDefault` |

### Third pass — package names (the import path is read more than any symbol)

| Original | Problem | Adopted |
| :--- | :--- | :--- |
| `codec/` | "codec" implies encode **and** decode; the crawler only ever **decodes** (the publisher encodes) | **`decode/`** — `decode.For(encoding)`, `decode.Registry` read honestly |
| `obs/` | cryptic abbreviation; a reader shouldn't have to expand it | **`telemetry/`** (note: distinct import path from the repo-level `pkg/telemetry`) |

**Both are adopted throughout this CR** (§6, §6a, §7, §9, and the mapping table already use `decode/` and `telemetry/`). The remaining open naming calls are `source/` vs `feed/` and `runner/` vs `engine/`.

### Leave these alone (name already matches behavior)

`Decide`, `Select`, `Resolve` / `ResolveWithChangeset`, `BatchCatalog`, `ParkQueueItem`, `RecordFailure`, `ClaimNext`, `Complete`, `digestMatches`, `IsPermanent`, `docCounts`, `ackedCount`, `encodingFor`, `Backoff`, `Rollup`. Renaming these would be churn for no clarity gain — the discipline is "fix names that lie", not "rename everything".

**Sequencing:** these are pure renames — do them in **PR-A** (they're compiler-verified and behavior-preserving). The `failItem`/`failPermanent` → `scheduleRetry`/`parkPermanently` rename in particular should land *with* the `runner/` split (§6), since that code moves anyway.

## 6. Target structure (~6–7 packages)

```
pkg/crawler/
├── crawler.go        # composition root — New(Config, Deps); only file naming every concrete type

├── catalog/                 # pure domain — imports nothing internal (importing telemetry/ is FORBIDDEN)
│   ├── index.go             #   Index, Entry, FileRef                         (was model.go)
│   ├── signature.go         #   Signature + signed tuple                      (was model.go)
│   ├── visibility.go        #   networkIds / visibleTo — publisher declaration (was part of model/select)
│   ├── decide.go            #   Decide() → sync/skip/retire/rollback          (was change.go)
│   ├── eligibility.go       #   Select() → carry? + visibleTo; calls Membership port for scope (was select.go)
│   ├── compose.go           #   fold baseline+changes → composed catalog      (was resolve.go)
│   ├── lifecycle.go         # ★ SHARED state vocabulary (§6b): Outcome, CatalogStatus, Decision, DropReason — the bottom of the graph so every layer can import it without a cycle
│   └── fault.go             # ★ typed FaultClass taxonomy (replaces engine.go reasonCategory) — part of the same vocabulary (§6b)

├── fetch/                   # retrieval + integrity                           (was http.go)
│   ├── client.go  guard.go  integrity.go  conditional.go
│   └── telemetry.go               #   fetch's metric/span/event names, co-located
├── decode/                   # decode registry — format extension point        (was codec.go)
│   └── registry.go  gzip.go  telemetry.go
├── validate/                # ★ schema-validate FILE DATA + push body (Validator port over schemav2validator)
│   └── schema.go  telemetry.go
├── publish/                 # send to Discovery (matches catalog/push)        (was push.go)
│   └── request.go  batch.go  client.go  telemetry.go
├── source/                  # where index refs come from                      (was source.go)
│   └── static.go  registry.go  telemetry.go
├── store/                   # Postgres — ONE package (tx sharing)             (was state/)
│   └── open.go  queue.go  cursor.go  indexstate.go  schema/  telemetry.go
├── runner/                  # orchestration only                             (was engine.go, 576→split)
│   └── runner.go  indexpass.go  syncpass.go  backoff.go  ports.go  telemetry.go
│                            #   + lifecycle.go ★ — ORCHESTRATION-ONLY states (§6b): SyncPhase, IndexOutcome, DaemonState (used by no layer below runner)
├── config/                  # settings.go                                     (was config.go)
└── telemetry/                     # ★ thin plumbing only
    └── otel.go              #   meter/tracer/provider construction, exemplars
    └── correlation.go       #   RunID/PassID types + context propagation
```

### Before → after mapping

| Before | After |
| :--- | :--- |
| `model.go` | `catalog/index.go` + `signature.go` + `visibility.go` |
| `change.go` | `catalog/decide.go` |
| `select.go` | `catalog/eligibility.go` (+ `Membership` port) |
| `resolve.go` | `catalog/compose.go` |
| *(`reasonCategory` in `engine.go`)* | `catalog/fault.go` ★ |
| *(status/outcome strings scattered across `retry.go`, `model.go`, `engine.go`, `state/`)* | `catalog/lifecycle.go` (shared) + `runner/lifecycle.go` (orchestration) ★ (§6b) |
| `http.go` | `fetch/{client,guard,integrity,conditional}.go` |
| `codec.go` | `decode/{registry,gzip}.go` |
| `push.go` | `publish/{request,batch,client}.go` |
| `source.go` | `source/{static,registry}.go` |
| `retry.go` | `runner/backoff.go` |
| `engine.go` (576) | `runner/{runner,indexpass,syncpass,ports}.go` |
| `config.go` | `config/settings.go` |
| `metrics.go` + `log.go` | `telemetry/{otel,correlation}.go` + co-located `*/telemetry.go` names ★ |
| `state/` | `store/{open,queue,cursor,indexstate}.go` (one package) |

## 6a. File-content contract (a file holds only what it names)

Same principle as §5a, one level up: `request.go` must contain **only** push-request building — not batching, not transport, not a fault type. No `misc.go` / `helpers.go` / `util.go` catch-alls. Each file's contents are a promise made by its name.

### What each notable file contains — and must NOT

| File | Contains ONLY | Must NOT contain |
| :--- | :--- | :--- |
| `catalog/index.go` | `Index`, `Entry`, `FileRef` + `LatestVersion` | signatures, auth, I/O |
| `catalog/signature.go` | `Signature`, signed-tuple shape | verification transport, keys |
| `catalog/visibility.go` | `networkIds`/`visibleTo` + `IsPublic` | scope/`approvedScopes` (that's the `Membership` port) |
| `catalog/fault.go` | `FaultClass`, `PermanentError`, `IsPermanent` | any decode/http logic |
| `fetch/client.go` | the GET client + `get`/`getConditional` | **`Push`** (that's publish), digest, SSRF rules |
| `fetch/guard.go` | SSRF host checks (`checkPublicURL`, `isCGNAT`) | GET logic |
| `fetch/integrity.go` | `digestMatches` | decode |
| `publish/request.go` | `BuildPushBody`, `PushMeta`, envelope/directive, `UpdateMode*` | batching, HTTP transport |
| `publish/batch.go` | `BatchCatalog`, `CatalogBatch` | body-building, transport |
| `publish/client.go` | the `Push` transport + `PartOutcome` | body-building, batching |
| `decode/registry.go` | the registry, `decode`, `encodingFor` | the fault type (→ `catalog/fault.go`) |
| `store/cursor.go` | per-catalog version cursor + reports | queue, index state |
| `store/queue.go` | enqueue/claim/reschedule/park/complete | catalog cursor, index state |
| `runner/syncpass.go` | the sync-one-catalog flow | index-pass logic, lifecycle |

### Current cohesion violations to fix during the split

The flat package has real "file does more than its name" cases — the split must place each piece by concept, not dump leftovers:

- **`http.go` holds `Push` (`http.go:101`)** — a *fetch* file that also *publishes*. `Push` + `PartOutcome` move to **`publish/client.go`**; only retrieval stays in `fetch/`.
- **`codec.go` holds `PermanentError`/`IsPermanent` (`codec.go:15-27`)** — a *fault* concept living in the *decode* file. Move to **`catalog/fault.go`** (it's the taxonomy's home, §9).
- **`engine.go` free helpers** — place by concept, do **not** create a `runner/helpers.go`:
  - `docCounts` / `ackedCount` (`engine.go:541,550`) → `publish/` (they describe push outcomes/counts).
  - `reasonCategory` (`engine.go:562`) → deleted, replaced by `catalog/fault.go` (§9).
  - `findCatalog` (`engine.go:569`) → `catalog/` (it's an index lookup, pure).
- **`state.go` null helpers** (`nullStr`/`nullIntZero`/`nullTime`, `state.go:279-295`) → one small **`store/scan.go`**, not scattered across `cursor.go`/`indexstate.go`.
- **`push.go` mixes body-building and batching** (`BuildPushBody` + `BatchCatalog`) → split into `publish/request.go` and `publish/batch.go` (already in §6).

Acceptance check for PR-A: no file contains a symbol that belongs to another file's concept, and there is no `helpers.go`/`util.go`/`misc.go` anywhere.

### Deliberate calls
- **Domain is one `catalog/` package**, not four contexts — but `access.go` is broken up (visibility → `catalog`, scope → a `Membership` port, authMethods → `fetch`).
- **Ports are consumer-defined in `runner`** (declared next to their consumer, e.g. `runner/ports.go`) — not a separate ports *package*, not hoisted away.
- **`store/` stays one Go package** — `Complete → upsertCatalog` shares a transaction (`queue.go:134` / `state.go`); a package split would leak the tx boundary.
- **`telemetry/` is plumbing only** (~OTel wiring + correlation); each adapter owns its own telemetry *names* in a local `telemetry.go`.

### Open naming choices (one review round vs. the team's spoken vocabulary)
- `source/` vs `feed/` · `runner/` vs `engine/` · the future `membership/` and `trust/` package names.

## 6b. Define the lifecycle explicitly (single source of truth)

**Problem.** The crawler describes "what is happening" with hand-typed strings scattered across the package, in four disconnected vocabularies — two as typed constants, two as bare literals — with a casing collision and the same rule copy-pasted three times. There is no one place a reader can look to see the states the crawler can be in. This is a root cause of the confusing, inconsistent logs.

And more fundamentally: **the lifecycle had the wrong subject.** "Crawler started / running / completed" is meaningless — a crawler *process* is a long-running supervisor that never "completes," and "completed" alone never says *for what* or *ending as what*. A lifecycle is always the lifecycle **of a unit of work**.

### The subject of the lifecycle: a **Catalog Sync**

The unit of work that genuinely has a `started → running → done` life is **one catalog moving one version jump**. Everything else is a supporting actor.

```
CATALOG SYNC   subject = catalog_id + (fromVersion → toVersion)     ← the "for what"

  started ─► running ─────────────────────────────────► done AS a terminal outcome
            ├ resolving   (download baseline + changes)   │
            ├ verifying   (digest)                        │   ✅ pushed    landed in Discovery
            ├ validating  (schema of the content)         │   ⏭  skipped   nothing new to send
            ├ scoping     (member? in approved scope?)     ├─► 🚫 dropped   not a member / out of scope
            └ publishing  (push to Discovery)             │   🪦 retired   catalog tombstoned
                                                          │   ❌ faulted   + FaultClass (why)
```

Every state answers both operator questions: **for what** (the catalog + version jump) and **ended as what** (a *named* terminal outcome — never a bare "completed").

**Supporting actors — NOT this lifecycle:**
- **The crawler process** is the *supervisor* (the "driver"): `ready → stopping → stopped`. It has **no "completed"** — it just launches many Catalog Syncs until stopped.
- **An Index crawl** *feeds* the queue — "which of a participant's catalogs changed?" — and finishes as `unchanged | enqueued(N) | failed`. It decides *what to sync*; the Catalog Sync *does* the sync.

### Evidence — four vocabularies today

| # | Vocabulary | Values | Defined as | Where |
| :--- | :--- | :--- | :--- | :--- |
| 1 | sync status | `ok` / `partial` / `failed` | ✅ constants | `retry.go:9-11` (`SyncOK`…) |
| 2 | index entry status (ION wire) | `ACTIVE` / `RETIRED` | ✅ constants | `model.go:70-71` (`StatusActive`…) |
| 3 | stored catalog status | `active` / `retired` | ❌ **bare literals** | `engine.go:374,387,505`; `state/state.go:178` (comment only) |
| 4 | catalog-sync outcome | `pushed`/`partial`/`failed`/`rejected`/`skipped` | ❌ **bare literals** | `engine.go:370,390,487-491,511,536-538`; `state/state.go:165` (comment only) |

Smells this produces:
- **Casing collision:** `StatusActive = "ACTIVE"` (#2) vs the `"active"` written to the DB (#3) — same concept, two spellings, a latent bug the moment they're compared.
- **Rule duplicated 3×:** "4xx ⇒ `rejected`, else `failed`" is hand-rolled at `engine.go:487-491`, `536-538`, and in the `fail` router (`engine.go:526`).
- **Enum defined only in a comment** (`state/state.go:165`) — nothing typed, so the compiler can't catch `"reject"`.
- **The Catalog Sync lifecycle — the unit of work above — has no representation at all.** There is no sync object, no `run_id`/`pass_id`, no running sub-state, and "done" is a bare status string with no *subject* attached. The crawler process is a lone `stopped bool` (`engine.go:98`); a sync is just control flow inside `processItem` (`engine.go:383`, 138 lines) with no state you can name, log, or trace.

### Target — typed enums, placed by layer

The vocabulary is defined **once per layer that owns it** (a single root file is impossible — the pure `catalog/` core imports nothing internal, so a root `lifecycle.go` would force an import cycle). Placement rule: **each enum lives at or below its lowest-layer consumer.** The enums are grouped by *whose* lifecycle they describe — the Catalog Sync (the subject), the crawler process, and the index crawl.

| Enum | Belongs to | Values | Consumers | Home |
| :--- | :--- | :--- | :--- | :--- |
| **`SyncOutcome`** | Catalog Sync — terminal | `pushed`, `partial`, `skipped`, `dropped`, `retired`, `faulted` | store (persists), runner (sets) | `catalog/lifecycle.go` |
| **`FaultClass`** | Catalog Sync — *why* `faulted` | ssrf, oversize, digest_mismatch, decode, content_invalid, push_schema, push_rejected, gap, absent, store | catalog, fetch, decode, publish, store, runner, telemetry | `catalog/fault.go` |
| **`DropReason`** | Catalog Sync — *why* `dropped` | not_a_member, scope_not_approved | produced by `catalog/eligibility.go`, consumed by runner | `catalog/lifecycle.go` |
| `CatalogStatus` | Catalog Sync — persisted status | active, retired | catalog, store, runner | `catalog/lifecycle.go` |
| **`SyncPhase`** | Catalog Sync — *running* sub-state | resolving, verifying, validating, scoping, publishing | runner only (transient — traces/logs, not persisted) | `runner/lifecycle.go` |
| `Decision` | Index crawl — per-catalog verdict | sync, skip_unchanged, retire, rollback | produced by `catalog/decide.go`, consumed by runner | `catalog/lifecycle.go` |
| `IndexOutcome` | Index crawl — terminal | unchanged, enqueued, failed | runner only | `runner/lifecycle.go` |
| `DaemonState` | Crawler process (supervisor) | ready, stopping, stopped, start_failed | runner only | `runner/lifecycle.go` |

Two deliberate consequences of the subject-centric model:
- **There is no `PassState{started,running,completed,failed}`** — that was the subject-less mistake. `started`/`running` are the Catalog Sync's own states (`SyncPhase`); `completed` is replaced by a *named* `SyncOutcome`; and the crawler process (`DaemonState`) deliberately has **no `completed`** because a supervisor never completes.
- **`store/` may not import `runner/`** (§7) — which is precisely why `SyncOutcome`/`CatalogStatus` sit in `catalog/` (the shared bottom), not `runner/`. Each enum carries a `String()` returning the **stable wire value** used for DB persistence and log/metric rendering.

### The two decision helpers that consolidate the duplication

- `classifyOutcome(httpStatus int, ackedBatches int, err error) SyncOutcome` — the **one** place the "4xx ⇒ rejected, else failed/partial" rule lives (replaces `engine.go:487-491,536-538`); a 4xx push maps to `faulted` with `FaultClass=push_rejected`.
- `func (FaultClass) Permanent() bool` — replaces `IsPermanent` + the inline 4xx-is-permanent check in the `fail` router; drives the park-vs-retry branch for a `faulted` sync.

### Legal transitions are declared, not implied

`runner/lifecycle.go` declares the allowed moves of a Catalog Sync so "clearly defined" is literal and testable — an illegal transition is a bug the state machine rejects, not a silently-wrong string:

```
started → resolving → verifying → validating → scoping → publishing → SyncOutcome{pushed|partial}
   any running sub-state → faulted (with FaultClass)     scoping → dropped     (retire path) → retired
   publishing with nothing to send → skipped
```

### Two-layer principle (and what is explicitly rejected)

- **Model the lifecycle as an explicit state machine** (the enums above + the `runner/` files that drive them) — the orchestration layer *is* the lifecycle, and every log/span/metric attaches at a state transition (§9).
- **Keep the step logic organized by concern** (`fetch`/`decode`/`compose`/`publish`) — the reusable verbs the lifecycle calls into.
- **Rejected: packages named after lifecycle phases** (`started/`, `running/`, `completed/`). That groups by *when code runs* instead of *what changes together* — `running/` would hold ~90% of the code, `resolve` is shared across passes, and phases don't change together. Cohesion beats temporal grouping.

**Sequencing:** land `catalog/lifecycle.go` (`SyncOutcome`, `CatalogStatus`, `Decision`, `DropReason`) + the `classifyOutcome`/`Permanent` consolidation in **PR-A** (behavior-preserving — same wire values, now typed). The `SyncPhase`/`IndexOutcome`/`DaemonState` runtime states in `runner/lifecycle.go` land with the `runner/` split and are consumed by the observability work in **PR-B** (§9, §9b).

## 7. Dependency rules

- `catalog/` imports **only stdlib + `catalogfile`** — and is **forbidden** from importing `telemetry/` (telemetry must not enter the pure core; the runner instruments *around* domain calls).
- `runner/` **defines the interfaces it consumes** (`Fetcher`, `Pusher`, `Source`, `Validator`, `Membership`, `Verifier`) next to its constructor; it imports no concrete adapter.
- Adapters (`fetch`,`decode`,`publish`,`source`,`store`) import `catalog/` + `telemetry/` and **structurally satisfy** the runner's ports; they never import `runner/`.
- `telemetry/` and `config/` are leaves.
- `crawler.go` is the **only** composition root that names concrete types.

Enforcement: document these in `doc.go` now. Add `depguard` **only if/when** the graph grows enough to actually drift — with two rules the compiler can't give for free: `catalog → telemetry` forbidden, and (later) `source/registry` as the sole importer of raw registry wire types (the anti-corruption boundary).

## 8. Extension points (roadmap slots in additively)

| Feature | Lands in | Ripple |
| :--- | :--- | :--- |
| **Registry discovery** (networkId → index refs) | `source/registry.go` (with an ACL translating registry wire types → internal) | composition root swaps `source.Static` → `source.Registry` |
| **Scope enforcement** (schemaTypes vs approvedScopes) | `catalog/eligibility.go` via the `Membership` port; promote to a `membership/` package when it lands | isolated |
| **Restricted / signed-request fetch** (authMethods) | a signing decorator in `fetch/` (+ a future `trust/` for the 7-step verify) | runner still calls the `Fetcher` port |
| New codec (zstd/brotli) | one file in `decode/` + one `FaultClass` for its decode errors | none |
| **Content schema validation** (validate fetched file data vs `schemaTypes`) | `validate/schema.go` behind the `Validator` port; runner calls it in `syncpass` after decode/compose | isolated |
| Signed-`size` tuple (ION conformance) | `catalog/signature.go` + verifier | localized |

## 9. Observability (PR-B)

ClickStack is OpenTelemetry-native (OTel collector → ClickHouse → HyperDX). Design:

- **`telemetry/` = plumbing only:** OTel meter/tracer/provider construction (today inline at `crawler.go:65`) and the correlation types. **Telemetry *names* live co-located** with the code they describe (`fetch/telemetry.go`, `publish/telemetry.go`, …) so on-call opens the failing adapter and finds its metrics/spans/events together — not a central `telemetry/events.go` junk drawer.
- **Typed `FaultClass` taxonomy** in `catalog/fault.go` (`ssrf`, `too_large`, `digest_mismatch`, `unsupported_encoding`, `decode`, `content_invalid`, `push_schema`, `push_rejected`, `push_transient`, `index_fetch`, `absent`). Adapters return typed faults; the runner switches on the class. **One source of truth** feeding: the metric `outcome=` label, the log `event` key, and the retire/retry decision — replacing the fragile `reasonCategory` string-split (`engine.go:562`). Note the two distinct schema faults: `content_invalid` (fetched file data fails its `schemaTypes` — §9a) vs `push_schema` (the outbound push body fails `catalog/push`); both are **permanent** (don't advance the cursor; alert).
- **Correlation:** a `RunID` (per pass tick) and `PassID` (per catalog item) minted in `runner/`, carried in `context.Context`, stamped on spans, attached to every log line (`LoggerFrom(ctx)`), and used as histogram exemplars. This is what makes the HyperDX "failed span → its logs → the metric spike" pivot work; it does not exist today (`Log` calls take loose `kv` with no id).

**Traces (span tree)** — one span tree per lifecycle (§6b); the `crawler.sync` root **is** a Catalog Sync, its child spans are the `SyncPhase` running sub-states:
```
crawler.index_pass {run_id, source}
└─ crawler.index_crawl {participant_id, index_url, index_version, outcome=unchanged|enqueued|failed}
   └─ crawler.fetch {kind, encoding, bytes; status=ssrf|digest_mismatch on failure}
crawler.sync_pass {run_id}
└─ crawler.sync {catalog_id, from_version, to_version, pass_id, sync_outcome}   ← the subject
   ├─ crawler.sync.resolving  {baseline_v, change_count}
   ├─ crawler.sync.verifying  {digest}
   ├─ crawler.sync.validating {schema}
   ├─ crawler.sync.scoping    {kept, drop_reason}
   └─ crawler.sync.publishing {mode, batches, body_bytes, http_status}
```

**Metrics** — bounded-cardinality attributes only (never `catalog_id`/`url` as a label):

| Metric | Type | Attributes |
| :--- | :--- | :--- |
| `crawler.index.checked` | counter | `result` (changed/not_modified/error), `source` |
| `crawler.index.decision` | counter | `decision` (sync/skip_unchanged/retire/rollback) |
| `crawler.queue.depth` | observable gauge | — |
| `crawler.queue.claim_reclaimed` | counter | — (lease-race signal) |
| `crawler.queue.superseded` | counter | — (concurrency-visibility) |
| `crawler.sync.duration` | histogram | `sync_outcome` (pushed/partial/skipped/dropped/retired/faulted) |
| `crawler.fetch.bytes` | histogram | `kind`, `encoding` |
| `crawler.decode.in_bytes` / `.out_bytes` | histogram | `encoding` (ratio = bomb signal) |
| `crawler.fetch.result` | counter | `outcome`, `status_class` |
| `crawler.publish.result` | counter | `mode`, `outcome`, `status_class` |
| `crawler.publish.batches` | histogram | `mode` |
| `crawler.sync.faulted` | counter | `fault_class`, `permanent` (true/false) |

**Metric migration:** the existing names (`crawler_catalogs_pushed_total`, `crawler_queue_depth`, `crawler_push_latency_seconds`, …, `metrics.go:44-63`) are **live**. Migrate via **dual-emit** — emit old + new for one release, cut dashboards over, then drop old. Never rename live metrics inside PR-A.

## 9a. Content schema validation

Today only the **outbound push body** is schema-validated (the `Validator` port on the runner, against `catalog/push`). This CR reserves a home for validating the **inbound file data** — the fetched catalog content against its declared `schemaTypes` (`catalog.Entry.SchemaTypes`, already in the model).

- **Where:** a `validate/` adapter wrapping onix's `schemav2validator`, exposed through the runner's `Validator` port and invoked in `syncpass` **after decode, before publish** — i.e. integrity (digest) → decode → **content validation** → compose/publish. Trust order: never validate bytes that haven't passed the digest check.
- **What to validate — open design question (flag for the owner):**
  - A **baseline** file is a full catalog → validate against the catalog `schemaTypes`.
  - A **change file** is a *diff* (upserts/removals), **not** a full catalog, so it can't be validated as one. ION says each upsert is "the complete, schema-valid object" — so validate **each upsert item** against the resource/offer schema, and/or validate the **composed** catalog after the fold. Recommendation: validate the **composed result** against `schemaTypes` (covers baseline + all applied changes in one check) and treat a malformed upsert as surfacing there; validate individual upserts only if per-item error attribution is needed.
- **Failure handling:** a schema failure is a **`content_invalid`** fault — the sync ends `faulted`, **permanent** (a malformed file won't fix itself on retry), so fail fast, **do not advance the cursor**, emit `crawler.sync.faulted{fault_class=content_invalid}` + alert. Same discipline as too-large/decode faults.
- **Config:** gate it behind a flag (e.g. `CRAWLER_VALIDATE_CONTENT`) and make the schema source explicit (from `schemaTypes`); default posture (strict-reject vs log-only-warn) is a policy call for the owner — recommend **log-only-warn** at first rollout, then flip to strict once false-positive rate is known.

This reuses the same schema plumbing as the push-body validation, so `validate/` serves **both** validation points (inbound content, outbound body) behind one adapter.

## 9b. Log specification (implementation-ready)

This is the exact log contract an implementer builds to. **The rule: success is a trace, health is a metric, only exceptions and lifecycle boundaries are logs.** That is what keeps the log stream small — everything else in §9 (per-file fetch, per-batch push, "index unchanged") is a span or a metric, not a log line.

Logs are organized around the **Catalog Sync** lifecycle (§6b) — the subject. Every line names *which lifecycle* it belongs to and *what state* it is in, so the lifecycle is legible on the line itself, not buried in the event key.

### Conventions (apply to every line)

1. **Sink:** the injected `Logger` interface (`Info/Warn/Error(event string, kv ...any)`, `engine.go:17`) — no process-global logger. Backed by slog.
2. **Two mandatory fields make the lifecycle visible:**
   - `lifecycle` — whose lifecycle this is: `sync` (a Catalog Sync — the subject) · `index_crawl` (the feeder) · `daemon` (the supervisor process) · `store`. Batch ticks use `sync_pass` / `index_pass`.
   - `state` — the current state/terminal, always a §6b enum `String()`: `ready`/`stopping`/`stopped` · `resolving`…`publishing` · `pushed`/`skipped`/`dropped`/`retired`/`faulted` · `completed`.
   - The **event key** is just `crawler.<lifecycle>.<state>` (a stable id) — you never parse it; you filter on the `lifecycle`/`state` fields.
3. **Level = required operator action:** `ERROR` = broken, act now · `WARN` = degraded, act if it persists · `INFO` = lifecycle boundary + per-tick heartbeat · `DEBUG` = a sync's interior states, off by default.
4. **Subject + correlation on every line:** `run_id` (the tick that owns it); for `lifecycle=sync` → `catalog_id` + `from_version`/`to_version` + `pass_id` (**the "for what"**); for `lifecycle=index_crawl` → `participant_id` + `index_url`. (`ts`/`level` from slog.)
5. **Typed values, not prose:** `sync_outcome`, `fault_class`, `drop_reason`, `decision` are always the §6b enum `String()` — never a free-text sentence. Errors go in a separate `error` field.
6. **Endpoints/URLs:** log **host only** for the push endpoint (no query/secrets); publisher URLs (`index_url`, `file_url`) log in full (they're public).

Reference emit (a Catalog Sync that faulted):
```go
log.Error("crawler.sync.faulted",
    "lifecycle", "sync", "state", "faulted",
    "run_id", runID, "pass_id", passID,
    "catalog_id", item.CatalogID, "from_version", item.FromVersion, "to_version", item.ToVersion,
    "fault_class", fault.Class.String(), "permanent", fault.Class.Permanent(),
    "file_url", fault.URL, "error", fault.Err)
```

### Reading the lifecycle in the log stream

At **INFO** (default) you see supervisor boundaries, per-tick heartbeats, and only the *exception* terminals of a sync — because a healthy sync is a trace, not a log:
```
lifecycle=daemon      state=ready       source_mode=static sources=3 push_host=discovery.local
lifecycle=index_pass  state=completed   run_id=r1 indexes_changed=1 catalogs_enqueued=2 dur=40ms
lifecycle=sync        state=faulted     run_id=r2 pass_id=p9 catalog_id=…/electronics v1→v2 fault_class=digest_mismatch
lifecycle=sync        state=dropped     run_id=r2 pass_id=pA catalog_id=…/mobility     drop_reason=not_a_member
lifecycle=sync_pass   state=completed   run_id=r2 synced=1 skipped=0 dropped=1 faulted=1 queue_depth_after=0 dur=1.2s
```
Turn **DEBUG** on and one Catalog Sync shows its whole life — `started → running(sub-states) → done AS outcome`:
```
lifecycle=sync  state=started     run_id=r2 pass_id=p3 catalog_id=…/electronics v1→v2
lifecycle=sync  state=resolving   run_id=r2 pass_id=p3 catalog_id=…/electronics
lifecycle=sync  state=verifying   run_id=r2 pass_id=p3 catalog_id=…/electronics digest=ok
lifecycle=sync  state=scoping     run_id=r2 pass_id=p3 catalog_id=…/electronics kept=true
lifecycle=sync  state=publishing  run_id=r2 pass_id=p3 catalog_id=…/electronics mode=FULL batches=1
lifecycle=sync  state=pushed      run_id=r2 pass_id=p3 catalog_id=…/electronics resources=2 dur=180ms
```

### The log catalog

Grouped by whose lifecycle it is. **These are the only lines emitted at INFO/WARN/ERROR;** a sync's `started`/running sub-states/`pushed`/`skipped`/`retired` are DEBUG (trace).

**Catalog Sync — the subject (only exception terminals are logged; success is a trace)**

| Event (`lifecycle`.`state`) | Level | Fires when | Required fields |
| :--- | :--- | :--- | :--- |
| `crawler.sync.faulted` | ERROR | sync ends `faulted` with a **permanent** fault; item parked, cursor NOT advanced | `run_id`, `pass_id`, `catalog_id`, `from_version`, `to_version`, `fault_class`, `permanent`(true), `file_url`(if fetch), `http_status`(if push), `error` |
| `crawler.sync.dropped` | WARN | sync ends `dropped` — scope/membership excludes it ("membership wins over declaration") | `run_id`, `pass_id`, `catalog_id`, `participant_id`, `drop_reason`(not_a_member\|scope_not_approved), `network`, `schema_types` |
| `crawler.sync.retry_exhausted` | WARN | a **transient** fault still failing after `max_attempts` (stays queued) | `run_id`, `pass_id`, `catalog_id`, `attempts`, `fault_class`, `error` |

`crawler.sync.dropped` is the #1 "why isn't my catalog in Discovery?" answer — it must be explicit. A 4xx push ends the sync as `faulted` with `fault_class=push_rejected`; a *systemic* "Discovery is down" is detected from the `crawler.publish.result` **metric** + alert (§9), not one log per catalog.

**Sync batch (the catalog-job tick — the heartbeat)**

| Event | Level | Fires when | Required fields |
| :--- | :--- | :--- | :--- |
| `crawler.sync_pass.started` | INFO | catalog-job tick begins with work | `run_id`, `trigger`, `queue_depth` |
| `crawler.sync_pass.completed` | INFO | queue drained for this tick | `run_id`, `synced`, `skipped`, `dropped`, `faulted`, `queue_depth_after`, `duration_ms` |

`crawler.sync_pass.completed` **replaces the meaningless "crawler passed"** — a verifiable tally; drill into any `catalog_id` from the `sync.*` lines above.

**Index crawl — the feeder (decides *what* to sync)**

| Event | Level | Fires when | Required fields |
| :--- | :--- | :--- | :--- |
| `crawler.index_pass.completed` | INFO | an index tick ran to the end | `run_id`, `indexes_checked`, `indexes_changed`, `catalogs_enqueued`, `duration_ms` |
| `crawler.index_pass.failed` | ERROR | the tick itself couldn't run | `run_id`, `stage`(source_resolve), `error` |
| `crawler.index_crawl.fetch_failed` | WARN | one publisher index can't be fetched | `run_id`, `participant_id`, `index_url`, `fault_class`(unreachable\|ssrf\|oversize\|decode), `consecutive_failures`, `error` |
| `crawler.index_crawl.rollback` | WARN | index version < stored cursor for a catalog (not applied) | `run_id`, `participant_id`, `catalog_id`, `cursor_version`, `index_version` |

**Crawler process — the supervisor (never "completes")**

| Event | Level | Fires when | Required fields |
| :--- | :--- | :--- | :--- |
| `crawler.daemon.ready` | INFO | boot done: config + DB open + migrate + first source resolve all OK | `source_mode`(static\|registry), `sources_count`, `networks`, `push_host`, `index_interval`, `catalog_interval`, `max_artifact_bytes`, `max_decompressed_bytes`, `max_attempts` |
| `crawler.daemon.start_failed` | ERROR | boot aborts (then process exits) | `stage`(config\|db_open\|db_migrate\|source_resolve), `error` |
| `crawler.daemon.stopping` | INFO | SIGINT/SIGTERM received | `signal` |
| `crawler.daemon.stopped` | INFO | ticks drained, safe to close DB | `uptime_seconds` |

`crawler.daemon.ready` is the "what happened at startup" line — one line showing the crawler's **entire effective config**.

**Store — infrastructure**

| Event | Level | Fires when | Required fields |
| :--- | :--- | :--- | :--- |
| `crawler.store.unhealthy` | ERROR | a DB operation persistently fails | `run_id`, `operation`(claim\|complete\|enqueue\|park\|record), `error` |

This **single** event collapses today's ~10 separate `*_failed` DB-op logs (`state_failed`, `touch_failed`, `record_failed`, `enqueue_failed`, `claim_failed`, `retire_failed`, `complete_failed`, `park_failed`, `recordfailure_failed`, `fail_failed`).

### Must be DEBUG (implement, but off by default)

A Catalog Sync's `started` and its running sub-states (`resolving`/`verifying`/`validating`/`scoping`/`publishing`) and its *success* terminals (`pushed`/`skipped`/`retired`); the index crawl's per-catalog `decided`(sync/skip) and `unchanged`/`304`; per-file fetch + digest-ok; per-batch push; each transient retry **attempt** (only the final `retry_exhausted` is WARN). These are the interior of a lifecycle — they belong to the **trace** (§9 span tree) and **metrics**, and would drown the operator at INFO.

### Count

**~13 INFO/WARN/ERROR event types total** — 3 Catalog Sync (the exception terminals) + 2 sync-batch + 4 index-crawl + 4 daemon + 1 store — down from the ~25 emitted today. Every remaining line names its `lifecycle` and `state`, and is either a supervisor boundary, a per-tick tally, or a typed exception terminal of a Catalog Sync, each cross-linked to its trace by `run_id`/`pass_id`.

**Sequencing:** the event keys, levels, and field sets above are part of **PR-B** (they depend on `run_id`/`pass_id` correlation and the §6b `SyncOutcome`/`FaultClass` enums). Implement against this table verbatim so the log contract is identical across the standalone driver and the onix plugin.

## 10. Migration plan (two PRs)

**PR-A — structural, behavior-preserving.** Compiler-verified moves, tests green after each step:
1. `catalog/` first (pure, no internal imports): `model→index/signature/visibility`, `change→decide`, `select→eligibility`, `resolve→compose`.
2. `store/` — rename `state/`, split by aggregate (`cursor.go`, `indexstate.go`), **keep one package** (tx sharing).
3. Adapters — `fetch/` (split guard/integrity/conditional), `decode/`, `publish/`, `source/`.
4. `runner/` — carve `engine.go` into `runner`/`indexpass`/`syncpass`/`backoff`/`ports`; move push helpers here.
5. `config/`; thin `crawler.go` composition root; update the plugin + `cmd/crawler` imports (highest blast radius — last).

**Verification for PR-A (make zero-drift mechanical, not aspirational):** `go build ./... && go vet ./... && go test ./...`, and a gofmt-normalized `git diff` that shows **only** package clauses, import paths, and file moves. Anything else is a red flag.

**PR-B — observability (a real change, reviewed as such):**
6. `telemetry/` plumbing (OTel wiring + `RunID`/`PassID` correlation); `catalog/fault.go` taxonomy replacing `reasonCategory`; adapters return typed faults; co-located `*/telemetry.go` names; dual-emit metric migration; new instruments (`fetch.result`, `decode.*`, `queue.claim_reclaimed`, `queue.superseded`).

**Honest caveat (from the pragmatic-Go review):** tests currently live in-package (`package crawler`, reaching unexported `filterCatalog`, `docCounts`, …). Moving code into subpackages **will** require moving those tests and, in places, exporting a symbol or switching to `_test` package — that is more than "import-path edits." Budget for it in PR-A; it does not change behavior, but it is not free.

## 10a. Test-suite organization

Tests live **next to the code, per package** (Go convention). The restructure makes the suite *faster and clearer*, but it does move tests out of the single flat package. Guidelines:

- **Pure domain (`catalog/`) — white-box, no mocks, table-driven.** `decide_test.go`, `eligibility_test.go`, `compose_test.go`, `fault_test.go`. These are the fast majority of the suite: no I/O, no fakes, deterministic. This is the biggest testability win of the split — today these rules are testable only through the flat package.
- **Adapters — black-box (`package fetch_test`, etc.) against real seams:**
  - `fetch/` → `httptest` servers (SSRF guard, conditional-GET/304, digest, size cap).
  - `decode/` → round-trip + **decompression-bomb** + corrupt-gzip rejection.
  - `publish/` → body-builder assertions + `httptest` for the client (FULL/MERGE, batching).
  - `validate/` → valid/invalid catalog + change-file fixtures.
  - `store/` → **Postgres-backed**, keeping today's pattern: skip when `CRAWLER_TEST_DB_DSN` is unset, and a **per-package schema** so parallel `go test ./...` stays isolated (`state/*_test.go` already does this — carry it over).
- **Orchestration (`runner/`) — behavior tests against in-memory fakes of the ports.** Because ports are consumer-defined in `runner`, a `runner/fakes_test.go` provides an in-memory `Source`/`Fetcher`/`Store`/`Pusher`/`Validator`. This is where today's `engine_test.go` scenarios go (queue drain, retry/backoff, retire, fail-permanent-no-cursor-advance). No DB, no network — fast.
- **`telemetry/` — light tests** (correlation id propagation via context; that metric names/attrs are bounded-cardinality).
- **Shared fixtures / doubles:** a small `internal/crawlertest` (or per-package `*_test.go` helpers) for the ION-shaped index/catalog/change fixtures used across packages, so the golden model isn't duplicated.

**White-box vs black-box:** default to **black-box** (`package foo_test`) to test each package through its public surface — this also validates that the public surface is sufficient. Use white-box (`package foo`) only where a test must reach an internal (mainly `catalog/` rules and `store/` helpers). Where a black-box test needs one internal, expose it via an `export_test.go` rather than widening the real API.

**Migration caveat (repeated from §10):** current tests are in-package (`package crawler`) and reach unexported symbols (`filterCatalog`, `docCounts`, …). As their code moves to `catalog/`, `runner/`, etc., the tests move with it and some flip from white-box-in-flat-package to black-box-with-`export_test.go`. This is mechanical but **not** a zero-touch "import path only" change — budget it in PR-A.

## 11. Risks & mitigations

| Risk | Mitigation |
| :--- | :--- |
| Telemetry rename breaks live dashboards | Split into PR-A/PR-B; dual-emit in PR-B |
| Import cycles during the move | Domain-first ordering; runner defines ports; adapters never import runner |
| Silent behavior drift in PR-A | Symbol-level gofmt diff (moves + imports only); full suite green |
| In-package tests undersold as trivial | Explicitly budgeted (§10 caveat) |
| `store/` tx boundary leaking | Keep `store/` a single Go package |
| Over-fragmentation | ~6–7 packages; domain stays one package; decode/identity not its own file |

## 12. Acceptance criteria

**PR-A**
- [ ] `engine.go` split; no file mixes unrelated concerns.
- [ ] `catalog/` imports only stdlib + `catalogfile` (and never `telemetry/`).
- [ ] Adapters do not import `runner/`.
- [ ] Public surface reduced to constructor + `Config` + ports + boundary types.
- [ ] **File cohesion (§6a):** every file contains only symbols matching its name; no `helpers.go`/`util.go`/`misc.go`; the known violations are relocated (`Push`→`publish/client.go`, `PermanentError`→`catalog/fault.go`, `findCatalog`→`catalog/`, `docCounts`/`ackedCount`→`publish/`).
- [ ] **Names (§5a):** the misleading `fail*` cluster renamed (`failItem`→`scheduleRetry`, `failPermanent`→`parkPermanently`, `fail`→`routeFailure`, `failReport`→`newFailureReport`, `FailQueueItem`→`RescheduleQueueItem`); `Source.IndexURLs`→`IndexRefs`; type names (`PartOutcome`→`BatchOutcome`, engine `Config`→`EngineConfig`, `IndexCond`→`IndexConditions`); `crawlIndex`'s `force bool` replaced by a named type.
- [ ] Diff is moves + import/package/rename edits only; `build`/`vet`/`test` green; onix plugin + `cmd/crawler` behave identically.

**PR-B**
- [ ] `FaultClass` taxonomy in `catalog/`; `reasonCategory` removed; one source drives metric label + log event + retry decision.
- [ ] `RunID`/`PassID` in context, on spans, logs, and exemplars.
- [ ] Telemetry names co-located per adapter; `telemetry/` is plumbing only.
- [ ] Metric migration is dual-emit; no live dashboard breaks on deploy.
- [ ] **Lifecycle enums (§6b):** `catalog/lifecycle.go` (SyncOutcome, CatalogStatus, Decision, DropReason) + `runner/lifecycle.go` (SyncPhase, IndexOutcome, DaemonState); the `ACTIVE`/`active` casing collision and the 3× "4xx ⇒ rejected" duplication removed (`classifyOutcome`); illegal transitions rejected. **The lifecycle's subject is a Catalog Sync (catalog_id + version jump), not "the crawler."**
- [ ] **Log catalog (§9b):** exactly the ~13 INFO/WARN/ERROR events implemented with the specified keys, levels, and required fields; every line carries `lifecycle` + `state`; a Catalog Sync's success terminals (`pushed`/`skipped`/`retired`) and running sub-states carry no log (trace only); the ~10 `*_failed` DB logs collapsed to `crawler.store.unhealthy`; every in-pass line carries `run_id` (+ `pass_id`/`catalog_id` for `lifecycle=sync`).

## 13. Out of scope (tracked elsewhere)

- Registry integration, scope enforcement, restricted/authMethods fetch, pointer-file traversal, signed-`size` conformance — see the ION reference mapping.
- **Inbound content schema validation** — this CR *reserves the home* (`validate/` package + `Validator` port + `content_invalid` fault, §9a); the validation logic, the composed-vs-per-item decision, and the strict/warn policy are a **follow-up feature**, not part of PR-A/PR-B.
- True streaming decode into `json.Decoder` (the `io.ReadCloser` seam is preserved).
- Batched-push protocol negotiation with Discovery (MERGE/session, Kafka size ceiling) — see the gzip/large-catalog review.
