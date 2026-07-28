# Catalog Crawler (Phase 1) — Code Review

**Branch:** `feat/catalog-crawler-phase1` · **Base:** `main` · **Scope:** `git diff main...HEAD` (48 files, ~3,586 insertions)
**Method:** 8 parallel finder angles (line-by-line, removed-behavior, cross-file, concurrency, security, reuse, efficiency/altitude, conventions) → dedup → direct code verification of every load-bearing claim.

Findings are ranked most-severe first. Each was confirmed against the actual code (not just the finder's report). One finder claim (the `schemaValidator` block failing adapter boot) was **refuted** — see the end.

---

## Severity summary

| # | Severity | File | Issue |
|---|----------|------|-------|
| 1 | **High** | `state/queue.go:43` + `engine.go` | Coalescing `Enqueue` races the claim lifecycle → lost update / double-push |
| 2 | **High** | `engine.go:315` + `state/state.go` | Give-up advances the version cursor → silent permanent data loss |
| 3 | High | `state/queue.go:61` | Orphaned claim (no lease/reaper) → in-flight work stranded on crash |
| 4 | High | `crawlHandler.go:78` + `engine.go:105` | `CrawlNow` runs detached on `context.Background()` → DB use-after-close on shutdown |
| 5 | Medium | `engine.go:115` + `config.go:75` | A `0` interval reaches `time.NewTicker(0)` → panic kills the process |
| 6 | Medium | `http.go:29`,`81` | SSRF guard bypassed by HTTP redirects + DNS-rebinding TOCTOU |
| 7 | Medium | `http.go:52` | Digest verification fails **open** when the index omits a digest |
| 8 | Medium | `resolve.go:27` + `catalogfile.go:56` | No change-file continuity check (`fromVersion` ignored) → a gap composes a wrong catalog |
| 9 | Medium | `http.go` (whole file) | Re-implements `pkg/security/artifactfetcher` — two divergent SSRF/digest fetchers |
| 10 | Low | `core/module/handler/config.go:80` | `PluginEntries()` omits the new `Crawler` slot → telemetry under-reports |

Plus **lower-severity / by-design** notes and **what's correct** at the end.

---

## 1. Coalescing `Enqueue` races the claim lifecycle → lost update / double-push  — **High**

**Where:** `pkg/catalogcrawler/state/queue.go:34-57` (`Enqueue`), `:61-84` (`ClaimNext`), `:102-115` (`Complete`); driven from `engine.go` (`indexPass` and `catalogPass` run as **two separate goroutines**, plus `CrawlNow`).

**Root cause:** the queue holds **one row per catalog** (`UNIQUE(catalog_id)`) and a claim carries **no lease token**. `Enqueue`'s `ON CONFLICT (catalog_id) DO UPDATE` resets `status='queued', attempts=0, claimed_at=NULL` on whatever row exists — *including a row a worker has already claimed and is actively processing*. `Complete`/`FailQueueItem` then settle by `id`, and the `id` is unchanged by the upsert.

**Failure scenario:** worker (catalog job) `ClaimNext`s catalog X at `to_version=5` and begins fetch/resolve/push. Concurrently the index job (or a `/crawl` `CrawlNow`) sees version 7 and `Enqueue`s X → the same row is reset to `to_version=7, claimed_at=NULL, attempts=0`. Now either:
- the worker finishes v5 and calls `Complete(id)` → `DELETE ... WHERE id=$1` **deletes the freshly-queued v7 work** → v7 is silently dropped until the publisher bumps the version again; or
- a second worker `ClaimNext`s the now-unclaimed row → **the same catalog is pushed to Discovery twice**.

This is reachable **without** `/crawl` because the index and catalog jobs are independent goroutines.

**Fix:** give each claim a token (e.g. `claim_id UUID` set by `ClaimNext`) and make `Complete`/`FailQueueItem` `WHERE id=$1 AND claim_id=$2`; have `Enqueue`'s `ON CONFLICT` **not** clear `claimed_at` for an in-progress row (or bump a `superseded` flag the worker checks before `Complete`). At minimum, `Complete` should be a no-op when the row's `to_version` no longer matches the item the worker settled.

---

## 2. Give-up advances the version cursor → silent permanent data loss  — **High**

**Where:** `pkg/catalogcrawler/engine.go:308-324` (`failItem` give-up branch) → `state/state.go:136` (`upsertCatalog` sets `version = EXCLUDED.version`).

After `MaxAttempts`, `failItem` calls `Store.Complete(..., CatalogState{Version: item.ToVersion, Status:"active", PushStatus:"failed"})`. That writes `crawler_catalog.version = ToVersion` **even though the push never succeeded**. On the next index pass, `GetCatalogVersion` returns that cursor, `Decide` sees `latest <= cursor` → `ActionSkipUnchanged`, and the catalog is **never re-enqueued** until the publisher advances the version beyond it.

**Failure scenario:** Discovery is down for longer than the `MaxAttempts` backoff window (default 5 attempts). Every catalog that changed during the outage is marked `active`/`push_status=failed` at its new version and then **permanently skipped** after Discovery recovers. Discovery is left missing those versions with no automatic recovery.

The code comment frames this as intentional ("advancing the cursor so it isn't re-enqueued... no hot loop") — but the cure (avoid hot-looping) causes silent data loss. The cursor should advance **only on a successful push**; failed give-ups should leave the cursor behind and rely on a separate dead-letter / capped-retry-with-longer-backoff mechanism, or an alert, rather than marking the version as applied.

---

## 3. Orphaned claim — no lease/reaper → in-flight work stranded on crash  — **High**

**Where:** `pkg/catalogcrawler/state/queue.go:61-84` (`ClaimNext`).

`ClaimNext` sets `claimed_at=now(), status='in_progress'`. Nothing ever resets `claimed_at` except a normal `FailQueueItem`/`Complete` or a coalescing `Enqueue`. If the process is killed, panics inside `processItem`, or the context is cancelled between claim and settle, the row stays `in_progress` with a non-NULL `claimed_at` forever. `ClaimNext`'s `WHERE claimed_at IS NULL` will never select it again, and the partial index in `0001_init.sql` (ready rows) excludes it too.

**Failure scenario:** a rolling deploy (SIGKILL after grace period) or an OOM during a large `Resolve` strands every currently-claimed catalog. Those catalogs are stuck until the publisher happens to bump their version (which triggers a coalescing `Enqueue` that resets `claimed_at`). There is no visibility-timeout reaper to reclaim stale claims.

**Fix:** add a reaper that resets claims older than a lease TTL (`UPDATE ... SET claimed_at=NULL, status='queued' WHERE status='in_progress' AND claimed_at < now() - $lease`), or fold a lease-expiry predicate into `ClaimNext`'s `WHERE` (`claimed_at IS NULL OR claimed_at < now() - $lease`).

---

## 4. `CrawlNow` runs detached → DB use-after-close on shutdown  — **High**

**Where:** `core/module/handler/crawlHandler.go:78` (`go h.crawler.CrawlNow(context.Background(), ...)`) + `engine.go:105-108` (`CrawlNow` not tracked by `e.wg`, uses the caller's ctx) + `catalogcrawler.go:91-97` (closer does `eng.Stop()` then `db.Close()`).

The `/crawl` handler launches `CrawlNow` in a **detached goroutine on `context.Background()`**. `CrawlNow` is *not* registered in `e.wg` (only the two `loop` goroutines are — `engine.go:112`), and because it uses `context.Background()`, `Stop()`'s `e.stop()` cancel doesn't reach it either. On shutdown, the plugin closer runs `eng.Stop()` (waits only for the two loops) then `db.Close()`. An in-flight `CrawlNow` keeps issuing `GetIndex`/`Enqueue`/`UpsertIndex` against a **closed `*sql.DB`** → `sql: database is closed`.

Secondary: the detached goroutine means `/crawl` errors surface only in logs, never to the `202` caller (acceptable by design, but worth stating).

**Fix:** track on-demand crawls in `e.wg` and run them under the engine's cancellable ctx (add an exported `CrawlNow` path that `wg.Add(1)`s and selects on the engine ctx), so `Stop()` drains them before `db.Close()`.

---

## 5. A `0` interval panics the process  — **Medium**

**Where:** `pkg/catalogcrawler/engine.go:111-127` (`loop` → `time.NewTicker(interval)`); `config.go:75-80` (`durOr`).

`durOr` uses `time.ParseDuration`, and `ParseDuration("0")` / `("0s")` returns `0, nil` — so a config value of `0` is accepted verbatim (the default only applies on a *parse error*). `New` clamps only `MaxAttempts`, never the intervals. `time.NewTicker(0)` panics with *"non-positive interval for NewTicker"* inside the `loop` goroutine, which has no `recover` → the whole crawler process/plugin host crashes.

**Failure scenario:** operator sets `CRAWLER_INDEX_INTERVAL=0` (or `CRAWLER_CATALOG_INTERVAL=0`) intending "disabled" or "as fast as possible." Adapter boots, then panics on the first `Start`.

**Fix:** clamp in `New` (or `LoadSettings`): `if cfg.IndexInterval <= 0 { cfg.IndexInterval = 5*time.Minute }`, same for catalog interval.

---

## 6. SSRF guard bypassed by redirects + DNS-rebinding TOCTOU  — **Medium**

**Where:** `pkg/catalogcrawler/http.go:29-31` (`NewHTTPClient` builds `&http.Client{Timeout: timeout}` with **no `CheckRedirect`**), `:81-107` (`get` runs `checkPublicURL` only on the **original** URL).

`checkPublicURL` (`:123-151`) resolves and rejects loopback/private/link-local hosts — but only for the URL passed in. The stdlib client then follows redirects with no per-hop re-check, and dials by re-resolving the hostname independently of the guard's `LookupIP`.

**Failure scenarios:** (a) a publisher index/file URL points at an attacker-controlled public host that returns `302 Location: http://169.254.169.254/…` (cloud metadata) or `http://10.0.0.5/` — the client follows it and fetches the internal target. (b) DNS rebinding: the guard's `LookupIP` returns a public IP, the client's dial re-resolves to a private IP. Both bypass the guard. The guard also omits some internal ranges (CGNAT `100.64.0.0/10`, NAT64 `64:ff9b::/96`, IPv4-mapped forms).

**Fix:** set `CheckRedirect` to re-run `checkPublicURL` on every hop, and pin the vetted IP at dial time via a custom `DialContext`/`net.Dialer.Control` that re-validates the actual connect address. Extend the deny-list to CGNAT/NAT64/mapped forms.

---

## 7. Digest verification fails open on an empty digest  — **Medium**

**Where:** `pkg/catalogcrawler/http.go:52` — `if f.Digest != "" && !digestMatches(b, f.Digest)`.

A missing/blank `digest` on a `FileEntry` is treated as success, so `FetchFile` returns unverified bytes. Since **signature verification is deferred to Phase 2** (the engine `Deps` has no verifier — `engine.go:51-61`), the digest is the *only* integrity check on fetched catalog content — and this makes it optional.

**Failure scenario:** a malicious or MITM'd publisher index omits `digest` on a change file; whatever bytes the server returns flow through `catalogfile.Apply` and are pushed to Discovery as authentic catalog content.

**Fix:** fail closed — reject a `FileEntry` with an empty digest (`if f.Digest == "" { return error }`) so integrity is mandatory, at least for non-baseline content.

---

## 8. No change-file continuity check (`fromVersion` ignored) → a gap composes a wrong catalog  — **Medium**

**Where:** `pkg/catalogcrawler/resolve.go:18-41` (fold loop) + `pkg/catalogfile/catalogfile.go:44-92` (`ChangeFileDoc.FromVersion` is parsed at `:46` but **never used** by `Apply`).

`Resolve` sorts changes by version and folds each whose version is in `(baseline, toVersion]`, skipping entries with an empty URL (`resolve.go:28`). It never verifies that each change's `fromVersion` equals the running composed version — i.e. that the change files are **contiguous**.

**Failure scenario:** index lists baseline v40 and change v42, but v41's entry has no URL (placeholder) and is skipped. v42's diff is defined relative to v41's state; applied onto v40 it silently mis-composes — an upsert meant to modify a v41-introduced item instead *adds* it, and a v41 removal of a v40 item is lost. Because the result is pushed `updateMode=FULL`, the wrong document fully **replaces** Discovery's copy — persistent wrong state, no error surfaced.

**Fix:** track the running version through the fold and require `change.FromVersion == running` (fail the resolve on a gap, rather than skipping URL-less intermediate changes). Also reject duplicate versions (`resolve.go:25` uses unstable `sort.Slice` with no dedup).

---

## 9. `http.go` re-implements `pkg/security/artifactfetcher`  — **Medium** (reuse / security-drift)

**Where:** `pkg/catalogcrawler/http.go` (whole file) vs `pkg/security/artifactfetcher/artifactfetcher.go` — **both added in this PR**, neither used by the other.

Both do size-capped GET + SHA-256 digest + SSRF host-guard. They have **already diverged**: `http.go:checkPublicURL` DNS-resolves the host and checks every IP, while `artifactfetcher.rejectPrivateHost` only inspects literal IPs and admits DNS-based SSRF isn't implemented. Two copies of security-critical fetch logic means a fix or CVE to one leaves the other exposed.

**Fix:** back the crawler's fetch on `artifactfetcher.Fetch` (add thin digest-compare + JSON-parse wrappers) so there is one SSRF/digest implementation. (Relatedly, `dedi_jws.go` re-implements the Ed25519 detached-verify tail already in `artifactverifier.go`, and the `{catalogId,version,url,digest,validUntil}` tuple bytes are hand-kept "byte-for-byte in sync" across `artifactsigner`/`artifactverifier` — consolidate into one canonical-bytes builder to remove the manual-sync hazard.)

---

## 10. `PluginEntries()` omits the new `Crawler` slot  — **Low** (observability), high confidence

**Where:** `core/module/handler/config.go:73-104`. The `Crawler` slot was added to `PluginCfg` (`:64`) but `PluginEntries()` never calls `add("crawler", p.Crawler)`.

This violates the method's **own doc-comment rule** (`:71-72`):
> *"Update this method whenever a new plugin slot is added to PluginCfg so that the `onix_plugin_info` gauge stays complete."*

**Cost:** the `onix_plugin_info` telemetry gauge never reports the crawler plugin, so fleet inventory/observability dashboards under-report which nodes run it.

**Fix:** add `add("crawler", p.Crawler)` to the list.

---

## Lower-severity / by-design notes

- **Eager engine start at module registration** (`catalogcrawler.go:33-89`): the engine is built + `Start`ed inside plugin `New`, and `LoadSettings` hard-requires `CRAWLER_DB_DSN`/`CRAWLER_PUSH_ENDPOINT` (env-only). A crawler misconfig (missing env, wrong DSN) makes `New` error → `NewCrawlHandler` error → **the whole BAP adapter fails to register**, taking down the unrelated `bapTxnReceiver`/`bapTxnCaller` modules with it. Consider degrading `/crawl` alone instead of aborting startup.
- **Validator invoked via a fabricated `url.URL{Path: action}`** (`catalogcrawler.go:57`): the crawler has no request URL, so it synthesizes one purely to satisfy `schemav2validator`'s path-keyed dispatch. Fragile coupling to that validator's path-parsing; a schema-name-keyed validation entry point would be the proper altitude.
- **Redundant per-item index re-fetch** (`engine.go:249`): `processItem` re-`FetchIndex`es (and linearly `findCatalog`-scans) the full index for every claimed catalog, on top of the fetch the index job already did. N changed catalogs ⇒ N extra full-index downloads/parses per cycle. Carry the `CatalogEntry` (or file URLs) on the queue item, or cache the index per pass.
- **O(history) resolve** (`resolve.go:18`): every sync re-fetches baseline + all change files and re-folds them, even for a single-version bump. Documented as intentional Phase-1 ("no composed state locally"), but cost grows with catalog age — worth revisiting when caching lands.
- **Sequential index crawl and single-row serial queue drain** (`engine.go:133`, `queue.go:61`): independent publisher fetches run in a plain loop and the queue drains one row at a time; one slow/hung publisher stalls the whole pass. `FOR UPDATE SKIP LOCKED` was chosen to enable concurrency that isn't yet used. Add per-item timeouts and a bounded worker pool.
- **`validUntil` signed but not enforced + `time.Time` marshal mismatch** (`dedi_jws.go` / `artifactsigner.go`): `VerifyFileTuple` never checks expiry, and the tuple marshals `validUntil` as a Go `time.Time` / `version` as `int` while the wire carries an RFC3339 **string** / `int64`. This is latent (the engine doesn't call `VerifyFileTuple` in Phase 1) but will cause round-trip verification failures/instability when signature verification is enabled in Phase 2. Marshal the tuple from the exact wire strings, and enforce `validUntil > now`.
- **Duplicate-id upserts appended twice** (`catalogfile.go:127-132`): the append loop iterates the raw `block.Upserts` and appends every id not in `seen`, but `seen` is not updated as new upserts are appended — a change file with two upserts sharing a new id produces a doc with a duplicate id. Only triggers on a malformed change file. Append from the deduped `upserts` map, or mark ids `seen` as they're appended.
- **`/crawl` is unauthenticated and does not check `indexUrl` against configured sources** (`crawlHandler.go`): by design it's a same-operator internal trigger (no `validateSign`), but there is no allowlist that `indexUrl ∈ CRAWLER_INDEX_URLS`, so combined with finding #6 it's an unauthenticated server-side fetch primitive. Consider restricting `indexUrl` to known sources.

## What's correct (verified, not findings)

- SQL is **fully parameterized** — no injection surface.
- `Complete` correctly wraps `upsertCatalog` + queue-row delete in **one transaction** with `defer tx.Rollback()`, advancing the cursor and purging the row atomically on the success path.
- `FOR UPDATE SKIP LOCKED` is used correctly for concurrent claim; NULL handling via `nullStr`/`nullInt64Zero`/`sql.Null*` is sound; migrations are idempotent (`IF NOT EXISTS`).
- Size caps are correct: `io.LimitReader(r, max+1)` then a length check catches oversize **before** parsing/OOM.
- Removal semantics within a single `Apply` are sound (removed ids are dropped before the append and not re-added), and because `Resolve` rebuilds from baseline every pass and pushes `updateMode=FULL`, removed resources are correctly absent downstream — no stale-item lingering.
- The detached-JWS **manifest** path (`VerifyDetachedJWS`) rejects `alg=none` via exact-header match and is symmetric with the signer.

## Refuted claim

- **"The `crawl` module's `schemaValidator` block has no spec and fails adapter boot."** Refuted: the crawl module's `schemaValidator` config (`config/local-beckn-one-bap.yaml:210-216`) is **identical** to the two shipping modules (`bapTxnReceiver` `:80-86`, `bapTxnCaller` `:155-161`). If those boot today, so does the crawl module; if they don't, it's a pre-existing issue not introduced by this PR.
