# Catalog Crawler — `.json.gzip` Support Review (Phase 2 design)

**Scope:** what it takes to support catalog artifacts in **`.json` and `.json.gzip`** — *efficiently, memory-safely, and extensibly* (more formats such as `zstd`/`brotli` are expected later, so the decode layer is built as a codec registry — G10 — not a gzip-specific branch).
**Status today:** verified — the crawler supports **`.json` only**. There is no decompression anywhere in the fetch/parse path (`grep -ri 'gzip|compress|inflate|Content-Encoding'` over `pkg/catalogcrawler/`, `pkg/catalogfile/`, `pkg/security/artifactfetcher/` → **0 matches**). `FetchFile`/`FetchIndex` hand raw fetched bytes straight to `json.Unmarshal` (`http.go:40,99`, `catalogfile.go:58`).

These are **enhancement comments**, distinct from the correctness findings in `catalog-crawler-phase1-review.md`. Nothing here is a bug in shipped behavior; it's the work to lift the format limitation.

---

## Current behavior with a `.json.gzip` file (confirmed)

1. `FetchFile` (`http.go:47-56`) downloads the raw **gzip bytes** and digest-checks them.
2. Raw gzip bytes go straight to `json.Unmarshal` (`resolve.go` → `catalogfile.go:58`).
3. gzip's magic header `0x1f 0x8b` is not valid JSON → `json.Unmarshal` fails (`invalid character '\x1f'`).
4. Error propagates → `Resolve` fails → `failItem` → the catalog is dropped (and, per phase-1 finding #2, its cursor may advance so it's never retried).

**Narrow accidental exception:** if a CDN serves the file with an HTTP `Content-Encoding: gzip` header (transport compression, *not* a `.json.gzip` artifact), Go's default client transparently inflates it and it parses — but the digest then covers *decompressed* bytes, silently breaking verification against a digest computed over the file at rest. This is not real artifact support and must not be relied on (see G3 below).

---

## Priority summary

| # | Priority | Item | Why |
|---|----------|------|-----|
| G1 | **Must** | Add a bounded gzip-decode step, gated on format detection | Without it, `.json.gzip` is unparseable |
| G2 | **Must** | Second size cap on **decompressed** output | gzip bombs (1000:1) OOM the process — the one real OOM vector |
| G3 | **Must** | Disable transparent transport gzip; control inflation explicitly | Avoids digest-over-decompressed ambiguity + double-inflate |
| G4 | **Must** | Verify digest on **compressed** bytes, then inflate | Reject tampered/bomb files cheaply, before spending CPU/memory on inflate |
| G5 | **Must** | Detect format from an explicit `encoding` field (url suffix as fallback), not content-sniffing | Deterministic; avoids guessing |
| G6 | **Must** | Stream-inflate the verified compressed blob into the parser under the decompressed cap | The recommended decode path; enforces the OOM cap efficiently |
| G7 | Consider | Clarify which bytes `FileEntry.Digest`/`Size` describe, with `catalogpublisher` | Cross-component contract; interop-breaking if mismatched |
| G8 | Consider | Send `Accept-Encoding` / negotiate format per publisher capability | Bandwidth; only if publishers serve both |
| G10 | **Must** | Build decode as a **codec registry** (`encoding` → decoder), not a gzip-specific branch | `json` + `json.gzip` today, more formats later — additive, not a rewrite |
| G11 | **Must** | Handle compressed files *efficiently* — incremental composition, streaming, pooled decoders, cheap pre-checks | Steady-state cost of a compressed sync must stay small even with many fetches |

**Design intent:** the supported set is **`json` and `json.gzip` today, with more formats likely later** (e.g. `zstd`, `br`). The decode layer must therefore be **open for extension** — adding a format is registering one decoder, touching no fetch/verify/parse/push code (G10).

---

## Recommended solution (build this one)

Of everything below, **this is the single approach to implement.** It is memory-safe, efficient, and doesn't require a cross-service rewrite.

**One decode path, wired into the existing fetch — dispatched through a codec registry so future formats are additive (G10):**

1. **Detect format explicitly** — a new `encoding` field on `FileEntry` (`""`/`"json"` = plain, `"gzip"`, and later `"zstd"`/`"br"`/…), populated by `catalogpublisher`; fall back to the `.json.gzip` url suffix if the field is absent (G5, Option B). The value is a lookup key into the codec registry — an unknown value is a clean permanent error, not a crash.
2. **Take control of compression** — `Transport{DisableCompression: true}` so `resp.Body` is always the exact artifact bytes we hash and inflate (G3).
3. **Fetch the compressed bytes under the existing cap** (`CRAWLER_MAX_ARTIFACT_BYTES`) — the compressed blob is small, so buffering it is fine.
4. **Verify the digest on the compressed bytes** *before* inflating — a tampered or bomb file is rejected on a cheap hash, no CPU/memory spent decompressing (G4).
5. **Stream-inflate the verified blob into the JSON decoder under a second cap** (`CRAWLER_MAX_DECOMPRESSED_BYTES`), reject-don't-truncate (G6 + G2). This is the key memory-safety step — it makes a decompression bomb a clean rejection, not an OOM.
6. **On exceeding the decompressed cap: fail *permanently*** — no 5× transient retry, **do not advance the version cursor**, emit an error + metric + alert (G9 required behavior). "Too big" becomes visible and actionable, never silently dropped.

**For large catalogs, the strategy is the protocol's own model, not a crawler rewrite:** the index already lists **many catalogs**, each is **baseline + small change files**. Keeping individual artifacts small is a **publisher-side** responsibility (split large catalogs, keep change files small) — that's cheaper and more robust than making the crawler stream-merge and batch-push GB-scale documents (G9, Tier 3b). Set `CRAWLER_MAX_DECOMPRESSED_BYTES` generously from your RAM budget (Tier 2) so ordinary large baselines pass; reject + alert beyond that (Tier 3a).

**The one crawler-side efficiency win worth pursuing next** (separate from gzip): **incremental composition** — cache the last composed catalog per `catalog_id` and, on a new version, fetch and apply only the *new* change file instead of re-fetching baseline + all changes every sync (phase-1's O(history) issue). This also makes gzip nearly free: after the first sync only small change files move. Do it as a follow-up, not part of the gzip change.

**Explicitly *not* building now:** the full streaming-parse + spill-to-disk merge + batched-push pipeline (G9, Tier 3c). It's gated on Discovery supporting batched/session publish and only pays off for GB-scale single catalogs, which publisher splitting should prevent.

The G-items below are the detailed rationale for each step above.

---

## G1 — Add a bounded gzip-decode step (Must)

**Where:** `pkg/catalogcrawler/http.go` (`FetchFile` `:47`, `FetchIndex` `:33`, and the shared `get` `:81`).

Insert a decode stage between "fetched bytes" and "hand to parser", selected by format (G5) and dispatched through the **codec registry** (G10) so `json` is a passthrough, `json.gzip` inflates through a bounded reader, and a future format is just another registered entry. Do **not** hardcode a `gzip`-only branch here. See G10 for the registry; a decoder for one codec looks like:

```go
// gzipDecoder — one entry in the registry. Called only after the COMPRESSED
// bytes are digest-verified (G4). Returns a reader; the caller applies the
// shared decompressed cap (G2), so no codec can forget the bomb guard.
func gzipDecoder(compressed []byte) (io.ReadCloser, error) {
    zr, err := gzip.NewReader(bytes.NewReader(compressed))
    if err != nil {
        return nil, fmt.Errorf("catalogcrawler: gzip open: %w", err)
    }
    return zr, nil // zr.Close() called by the shared decode wrapper
}
```

`FetchIndex` uses the same registry — the index itself may also be compressed.

---

## G2 — Second size cap, on the **decompressed** output (Must)

**Where:** new config `CRAWLER_MAX_DECOMPRESSED_BYTES` (`config.go:12-59`), threaded into the engine like `MaxArtifactBytes`.

The existing `CRAWLER_MAX_ARTIFACT_BYTES` caps the **compressed** download only. gzip ratios of 1000:1+ mean a 10 MiB gzip can inflate to ~10 GB; a plain `gzip.NewReader` + `io.ReadAll`/`json.Unmarshal` on that is an **unbounded allocation → process OOM**. This is the single real memory-safety risk introduced by gzip.

Requirements:
- Enforce with the same **reject-don't-truncate** pattern already used at `http.go:99` (`LimitReader(r, max+1)` then length check) — never `io.Copy`/`ReadAll` an unbounded `gzip.Reader`.
- Default to a sane multiple of the compressed cap (e.g. 100 MiB) and **clamp `<= 0` to the default** — do *not* repeat the `MAX_ARTIFACT_BYTES=0` foot-gun where `0` silently rejects everything (parses to `0`, not the default; see phase-1 notes).
- `Size` in `FileEntry` (if it refers to the compressed artifact) can be a cheap pre-check against the compressed cap, but the decompressed cap must be enforced on the *actual* inflate, not a declared field.

---

## G3 — Disable transparent transport gzip (Must)

**Where:** `NewHTTPClient` (`http.go:29-31`) currently builds `&http.Client{Timeout: timeout}` with the default transport.

Go's default transport auto-adds `Accept-Encoding: gzip` and transparently inflates `Content-Encoding: gzip` responses. Combined with our own `.json.gzip` inflation this creates two problems: (a) **double semantics** — a file could be inflated by the transport before our code sees it, so `sha256(resp.Body)` hashes decompressed bytes and mismatches a digest computed over the compressed artifact; (b) non-determinism depending on how the CDN sets headers.

Fix: take control explicitly.

```go
tr := &http.Transport{DisableCompression: true} // we handle gzip ourselves, over the exact bytes we hash
return &HTTPClient{hc: &http.Client{Timeout: timeout, Transport: tr}, ...}
```

This guarantees `resp.Body` is always the **exact artifact bytes** at the URL — what we digest and what we (conditionally) inflate.

---

## G4 — Verify digest on compressed bytes, then inflate (Must)

**Where:** `FetchFile` (`http.go:47-56`).

Order the operations: **fetch (capped) → digest-verify the compressed bytes → inflate (bounded).** Verifying before inflating means a tampered or bomb artifact is rejected on a cheap SHA-256 of ≤ the compressed cap, before any CPU is spent decompressing. This also keeps the trust boundary clean: we only ever inflate bytes we've already authenticated by digest.

(Do **not** switch to a `TeeReader`-through-hash-while-parsing scheme: it would parse unverified bytes and only learn the digest is bad at end-of-stream. For untrusted publisher content, verify-before-use wins, and the compressed buffer is small anyway.)

---

## G5 — Detect format deterministically (Should)

**Where:** `model.go` `FileEntry` (`:23-29`) and the fetch call sites.

Prefer an explicit signal over content-sniffing:
- **Option A (no schema change):** derive from the `url` suffix — `strings.HasSuffix(url, ".json.gzip")` → gzip, else plain.
- **Option B (explicit):** add `Encoding string \`json:"encoding,omitempty"\`` to `FileEntry` (values `"gzip"` / `""`), populated by `catalogpublisher`. Sturdier if URLs ever lack a meaningful suffix (query strings, signed URLs, content-addressed paths).

**Decision: Option B** (explicit `encoding` field), with url-suffix as the fallback when the field is absent. It survives signed/query-string/content-addressed URLs that a suffix check would misclassify. Avoid magic-byte sniffing as the primary mechanism — it's a fallback at best, and it fights the "verify before inflate" ordering.

---

## G6 — Stream-inflate into the parser (recommended decode path)

**Where:** the decode stage (G1) and, longer-term, `catalogfile.Apply`.

This is the decode mechanism the recommended solution uses. **Buffer only the compressed blob** (small — bounded by the compressed cap and already digest-verified), then inflate straight into the JSON decoder rather than materializing the full decompressed `[]byte` first:

```go
zr, err := gzip.NewReader(bytes.NewReader(compressed)) // compressed already in hand + digest-verified (G4)
if err != nil {
    return fmt.Errorf("catalogcrawler: gzip open: %w", err)
}
defer zr.Close()
dec := json.NewDecoder(io.LimitReader(zr, maxDecompressed+1)) // bounded, streamed (G2)
if err := dec.Decode(&doc); err != nil {
    return fmt.Errorf("catalogcrawler: decode: %w", err) // covers corrupt gzip and oversize
}
```

Buffering the compressed blob (not the decompressed one) is what keeps this both safe and simple: the digest can be verified up front (G4), and only the small compressed bytes are held alongside the parsed doc. G1's "inflate to a bounded `[]byte` then `Unmarshal`" is an acceptable equivalent if a call site needs the raw bytes (e.g. to re-hash); prefer the streamed decoder otherwise.

**Caveat — what this does and doesn't buy:** `catalogfile.Apply` (`catalogfile.go:56`) currently `json.Unmarshal`s the whole `Doc` (with `Resources []json.RawMessage`) into memory, and the by-id fold needs the working set resident anyway. So stream-decode alone doesn't make the *fold* O(1) — it only avoids holding `compressed + full-decompressed-bytes + parsed-structs` simultaneously, and (crucially) it enforces the decompressed cap. The parsed doc itself is still resident, governed by that cap. True O(working-set) resolution is a larger change (streaming/spilling merge) — deferred to the large-catalog work (Tier 3c), which publisher splitting should keep off the table.

---

## G7 — Nail down the digest/size contract with `catalogpublisher` (Consider)

**Cross-component question, blocking correctness of G4:** does `FileEntry.Digest` (and `Size`, and the Phase-2 signed tuple `{catalogId, version, url, digest, validUntil}`) describe the **compressed artifact at rest** or the **decompressed JSON**?

- Almost certainly the artifact at rest (that's what the URL serves and what a crawler can verify without inflating) → verify compressed, matching G4.
- Confirm against `pkg/security/artifactsigner` and the publisher CLI (`cmd/catalogpublisherctl`) so both sides hash the same bytes. A mismatch here silently fails verification for every gzipped file once Phase-2 signatures turn on.

---

## G8 — Optional: negotiate format per publisher (Consider)

If some publishers serve both `.json` and `.json.gzip`, the crawler could prefer gzip to save bandwidth (large catalogs compress well). This is a fetch-selection concern, not a decode concern, and only worth it if the index actually offers a choice. Not needed for baseline two-format support.

---

## G9 — Handling oversize catalogs: a tiered strategy (Must decide)

Streaming the decompression lets you **safely reject** an arbitrarily large gzip with bounded memory, but it does **not** let you *process* one — the downstream steps (`json.Unmarshal` of the whole doc, the by-id fold in `catalogfile.Apply`, and the single-body `updateMode=FULL` push) all materialize the full catalog in memory. So "handle any size" is not a decompression setting; it's a pipeline decision. Match the strategy to the size:

### Tier 1 — Normal catalogs (the ~99% case)
Keep the current shape: download → decompress (bounded) → parse → push. No change beyond adding gzip. Most catalogs are a few MB.

### Tier 2 — Big but bounded (up to a few hundred MB decompressed)
Just **raise `CRAWLER_MAX_DECOMPRESSED_BYTES`** to what RAM allows and let it buffer. The queue drains **serially** (one catalog at a time), so peak memory ≈ `cap × ~3` (raw + parsed structs + re-marshaled body). Size the cap from the memory budget:

> e.g. 4 GB box with headroom → decompressed cap ≈ 500 MB (≈ 1.5 GB peak, one at a time).

This is the pragmatic default and covers almost everything. No re-architecture. **Caveat:** if the catalog job is ever made concurrent (a worker pool, per phase-1 efficiency notes), multiply the peak by the worker count and re-check the budget.

### Tier 3 — Genuinely huge (GB-scale)
Do **not** try to swallow it. Pick one:

- **(a) Reject cleanly + alert — recommended default.** On exceeding the cap: stop, mark `failed: too large`, emit a metric, fire an alert. A human then decides (raise cap / add memory / split). The point is it's **visible**, not silently dropped.
- **(b) Push the fix to the publisher.** The spec already supports splitting — an index lists **many catalogs**, each is baseline + small **change files**. A multi-GB single catalog is usually a modeling problem; ask the publisher to split into multiple catalog entries or keep change files small. Solves the size problem where it's cheapest.
- **(c) Full streaming + batched-push pipeline.** Only if GB-scale becomes a real, recurring requirement. Big project: stream-parse + spill-to-disk/DB-backed by-id merge + batched publish — and it needs **Discovery's `/push` to support batched/session publish** (batching breaks `updateMode=FULL` replace semantics). Cross-service, not crawler-only. Defer until forced.

### Required behavior on "too big" — regardless of tier
An oversize file is a **permanent** error (it won't shrink on retry), so the crawler must:

1. **Fail fast** — do not run it through the 5× transient-retry loop.
2. **Not advance the version cursor** — otherwise it's silently marked "done" and never retried even after the cap is raised (this is exactly phase-1 finding #2).
3. **Emit a clear error + metric** — `catalog <id> rejected: decompressed size exceeds <N>` — so ops sees it and can act.

### Recommendation
Ship **Tier 1 + Tier 2 + Tier 3(a)** now: add gzip with a decompressed cap, set the cap from the RAM budget, and on exceed → **fail permanently, don't advance the cursor, alert.** Steer publishers toward splitting (b). Defer the streaming + batch rewrite (c) until a real GB-scale catalog forces it, since half of it lives in Discovery.

---

## G10 — Design for format extensibility: a codec registry (Must — architecture)

`json` + `json.gzip` are the two formats today, but **more are expected later** (`zstd`, `brotli`, or a different packaging). Build the decode layer so adding one is *additive* — register a decoder, change nothing else. A `switch encoding {...}` scattered across `FetchFile`/`FetchIndex` is the wrong altitude: every new format would mean editing the fetch, the caps, and the tests in several places, and it's easy to add a codec that forgets the bomb guard (G2).

**Shape: one small registry, one shared bounded-decode wrapper.**

```go
// A decoder turns already-digest-verified compressed bytes into a reader over
// the decoded content. It does NOT enforce the size cap — the shared wrapper does.
type decoder func(compressed []byte) (io.ReadCloser, error)

var codecs = map[string]decoder{
    "":     plainDecoder, // "" and "json" == identity passthrough
    "json": plainDecoder,
    "gzip": gzipDecoder,
    // "zstd": zstdDecoder,   // <- future: one line, nothing else changes
    // "br":   brotliDecoder,
}

// decode is the single choke point every fetch goes through. It looks up the
// codec, then applies the shared decompressed cap so no codec can bypass it.
func decode(encoding string, compressed []byte, maxDecompressed int64) ([]byte, error) {
    d, ok := codecs[encoding]
    if !ok {
        return nil, &permanentError{fmt: "unsupported encoding %q", arg: encoding} // G9: permanent, alert, no cursor advance
    }
    rc, err := d(compressed)
    if err != nil {
        return nil, err
    }
    defer rc.Close()
    out, err := io.ReadAll(io.LimitReader(rc, maxDecompressed+1)) // shared bomb guard (G2)
    if err != nil {
        return nil, err
    }
    if int64(len(out)) > maxDecompressed {
        return nil, &permanentError{fmt: "decoded body exceeds max %d bytes", arg: maxDecompressed}
    }
    return out, nil
}
```

**Why this shape:**

1. **Additive** — a new format is one map entry + one `decoder` func + its tests. Fetch/verify/parse/push are untouched (open/closed).
2. **The bomb guard can't be forgotten** — the decompressed cap (G2) lives in the shared `decode` wrapper, not in each codec. Every present and future format inherits it. This is the single most important reason to centralize: each new codec is a new decompression-bomb surface, and a per-codec cap would eventually be missed.
3. **Codec-agnostic invariants hold for all formats** — digest-verify-then-decode (G4), disable-transport-compression (G3), and permanent-error-on-failure (G9) are all *outside* the codec, so they apply uniformly. Adding `zstd` can't accidentally weaken them.
4. **Unknown/unsupported encoding is a clean permanent error**, not a panic — a publisher advertising a format this crawler doesn't have yet is rejected + alerted (G9), and starts working the moment its decoder is registered.

**Config generalizes too, minimally:** keep a **single** `CRAWLER_MAX_DECOMPRESSED_BYTES` that the shared wrapper applies to every codec. Only introduce per-codec caps if a real format needs a different bound — don't pre-build that. Format negotiation (G8) likewise generalizes to advertising the registry's key set as the crawler's accepted-encodings list, if/when publishers offer a choice.

**Keep the registry closed to config** — codecs are compiled-in Go, registered in code, not enabled via YAML/env. A decoder is executable trust surface (it inflates untrusted bytes); it should never be selectable by external configuration.

---

## G11 — Handle compressed files efficiently (Must)

A publisher-side control (capping/splitting artifacts at the source) is being added in the publish tool — good, it keeps the *big* files out. But that means the crawler's steady-state diet is **many small-to-medium compressed files, fetched frequently**, so "smart handling" here = **minimize per-fetch overhead and never redo work**. Techniques, ranked by impact:

### 1. Don't re-process the whole catalog every sync — *incremental composition* (biggest win)
Today `Resolve` (`resolve.go:18`) re-fetches the **baseline + every change file** and re-decompresses + re-folds them on *every* version bump (the phase-1 O(history) issue). With compression this is pure waste — you gunzip the entire history to add one delta.

Fix: **cache the last composed catalog per `catalog_id`** (in crawler state or a blob store), and on a new version fetch + decompress + apply **only the new change file**. After the first sync, only small deltas move. This is the single highest-leverage efficiency change, and compression makes it *more* valuable, not less: steady-state work drops from "gunzip the whole catalog" to "gunzip a small change file." Pair it with publisher-side small change files and a compressed sync becomes nearly free.

### 2. Stream decode; buffer only the compressed blob (G6)
Inflate straight into the `json.Decoder`; never hold `compressed + full-decompressed bytes + parsed structs` at once. The compressed blob is small (bounded by the compressed cap); the decompressed side streams through the parser under the cap.

### 3. Reuse decoders — don't allocate one per fetch
`gzip.Reader` supports `Reset(r)`, and future codecs (`zstd`) allocate sizeable window buffers. Keep a `sync.Pool` of readers so N fetches don't allocate N decompressors + N window buffers. Meaningful for a crawler doing many fetches per pass; the pool lives in the codec layer (G10), one pool per codec.

### 4. Cheap pre-checks before spending CPU/bandwidth
- If `FileEntry.Size` (compressed) already exceeds the compressed cap → **reject before downloading a byte**.
- **Verify the digest on the compressed bytes before inflating** (G4) → never spend CPU decompressing a tampered/bomb/corrupt file.
- If the index version is unchanged, the catalog is skipped entirely (already the case) — so no fetch/decode happens on a no-op pass.

### 5. Pre-size the decode buffer from a hint, clamped to the cap
`io.ReadAll` grows by doubling (~2× transient churn). If the entry carries a decompressed-size hint, pre-allocate to it (`make([]byte, 0, min(hint, cap))`) to avoid reallocation — but **still enforce the cap on the actual inflate**; the hint is an optimization, never a trust boundary.

### 6. Bounded parallelism (optional, later)
Decompression is CPU-bound; a small worker pool lets several catalogs decode concurrently once the queue is made concurrent (phase-1 efficiency note). **Tradeoff:** peak memory = `workers × decompressed cap` — size the pool and cap together, don't just crank both.

**Net steady-state after #1–#3:** a compressed catalog sync = fetch a small change file → digest-verify → stream-inflate with a pooled reader → apply to the cached composed doc → push. Small, streamed, no re-allocation, no re-doing history. That's the efficient outcome; the publisher-side control complements it by keeping the first-sync baseline small too.

**Sequencing:** #2–#5 ship *with* the gzip work (they're part of the decode path). #1 (incremental composition) and #6 (parallelism) are larger, separable follow-ups — call them out now so the decode layer is designed not to block them, but don't gate gzip support on them.

---

## Interaction with existing (phase-1) findings

- **Retry classification (phase-1 finding #2 neighbor):** an oversize-decompressed or malformed-gzip file is a **permanent** error, but `failItem` retries it 5× as transient then advances the cursor (dropping it). When adding gzip, classify decode/oversize failures as permanent → fail fast, and **do not advance the cursor** on give-up, so the content isn't silently lost.
- **Fetcher duplication (phase-1 finding #9):** whatever decode/decompress logic is added should live in **one** fetcher. If `http.go` and `artifactfetcher.go` aren't consolidated first, gzip support has to be written twice and will drift.
- **`MAX_ARTIFACT_BYTES=0` foot-gun:** apply the same clamp lesson to `MAX_DECOMPRESSED_BYTES` (G2).

---

## Minimal implementation checklist

1. `model.go` — add `Encoding` to `FileEntry` (G5, Option B); url suffix as fallback.
2. New `codecs` registry + shared `decode(encoding, compressed, cap)` wrapper (G10), with `plainDecoder` + `gzipDecoder` registered. This is the extension point for future formats.
3. `config.go` — add `CRAWLER_MAX_DECOMPRESSED_BYTES` (default 100 MiB, clamp `<= 0`); one cap applied by the shared wrapper to all codecs.
4. `http.go` — `Transport{DisableCompression:true}` (G3); in `FetchFile`/`FetchIndex`: fetch (compressed cap) → digest-verify (G4) → `decode(entry.Encoding, …)` (G1/G10) with decompressed cap (G2).
5. `engine.go` — thread `MaxDecompressed` through `Config`/`Deps` to the fetchers.
6. Classify decode failures (unsupported encoding, corrupt, oversize-decompressed) as **permanent** in `failItem` (no cursor advance, alert — G9).
7. Tests: plain `.json` passthrough, valid `.json.gzip` round-trip, gzip **bomb** rejected at the decompressed cap, truncated/corrupt gzip rejected, digest-mismatch-on-compressed rejected before inflate, **unknown `encoding` rejected as a clean permanent error**, and a **placeholder/registration test proving a new codec is additive** (register a fake codec → it works end-to-end with no other changes).
