# catalogcrawler

Implements `definition.Crawler`: walks a participant's published
manifest -> catalog-index -> catalog-file chain per **"Decentralized
Catalog file spec.md"** and returns the verified, composed result. This is
the consuming side of the same chain
[`catalogpublisher`](../catalogpublisher) produces -- see the file spec
for the full protocol background.

**This package was rewritten to match the file spec**, superseding the
earlier DeDi-wrapper-shaped index/manifest this package originally
consumed (see git history, and `catalogpublisher`'s own history for the
producing side of the same change). The old reference fixture
(`angular-absently-gab.ngrok-free.dev`) predates this shape and can no
longer be crawled by this package -- see "Trying it against a live
fixture" below for its replacement.

Exposed to the DS's own backend via the `catalogPull` handler type
(`core/module/handler/catalogPullHandler.go`), not as a network-facing
Beckn action -- `/catalog/pull` is an internal, unsigned trigger: same
operator, same trust domain, no `validateSign`/`addRoute`/`signAck`
pipeline.

## What this phase does

1. Parses the DS-supplied `receiverId` directly into a domain -- **no
   registry lookup**. The DS already knows which participant it wants to
   sync. `receiverId` mirrors `Context.receiverId` from beckn.yaml (the
   PN's DID), but for now its value is treated as a literal domain/URI --
   real DID resolution isn't implemented yet, this is a field-name
   alignment with the spec only.
2. Fetches `{domain}/.well-known/dedi.index.json` **unsigned** (it's a
   public, RFC 8615 well-known endpoint) and verifies its document-level
   detached-JWS `proof` against its own embedded `keys[]` (JWK,
   OKP/Ed25519) -- the manifest is the trust anchor for every signature
   downstream; a key not present in it is invalid.
3. For the `files[]` entry named `"becknCatalogs"`, fetches the catalog
   index -- a plain Beckn file, **not signed as a whole** (trust rides on
   each catalog file's own signature instead).
4. For each catalog entry in the index:
   - A `status: "RETIRED"` entry is a **tombstone** -- no files to fetch;
     reported as a `CatalogResult` with `Status: "RETIRED"` and no
     `Catalog` body.
   - Otherwise, fetches the `baseline` file, then every `changes[]` entry
     in order, **applying each onto the running content**
     (`pkg/catalogfile`) to produce the catalog's current effective
     content. Each fetched file is checked for digest match, declared
     size, an unexpired `signature.validUntil`, and a valid **per-file
     signature tuple** (`{catalogId, version, url, digest, validUntil}`,
     verified against the manifest's `keys[]` by `signature.keyId`) --
     this replaced the old model, where catalog parts carried no signature
     at all and only the whole index did.
5. Any failed check on any one file (digest, signature, expiry) drops that
   whole catalog as a non-fatal `CrawlError` -- never a partial,
   unverified composition. One bad catalog never fails the whole
   `CrawlResult`.
6. The DS-facing response (`catalogPullHandler.ServeHTTP`) matches
   beckn.yaml's `CatalogPullCallbackAction` exactly:
   `{"status": "COMPLETED", "catalogs": [...]}` on success, or
   `{"status": "FAILED", "error": {"code": "BIZ_CRAWL_FAILED", "message": "..."}}`
   on a fatal crawl failure. No other crawler-internal bookkeeping
   (`catalogId`, version, digests, verification outcomes) is returned to
   the DS; all of that is logged internally instead.

## Deliberately not done in this phase

- **No caching.** Every call re-fetches and re-verifies from scratch;
  `Changed` is always `true`.
- **No `checkPolicy`/`schemaValidator` plugin calls.** Structural/policy
  checks are inline Go logic for now.
- **Signed index/catalog GETs are disabled** (see `fetchOpts` in
  `catalogcrawler.go`) -- the reference test setup hosts these as public,
  unsigned artifacts. The signing capability (DS's own keys, via
  `Signer`/`KeyManager`) is fully wired through `artifactfetcher` and is a
  one-line change to re-enable once a participant in the test setup
  expects it.
- **No version-rollback detection.** `VerificationOutcome.VersionOK` is
  always `true`; the file spec's monotonic-version rule ("A crawler that
  sees either go backwards flags it") isn't checked yet -- this needs
  persistent per-catalog cursor state this phase doesn't have (no
  caching, see above).
- **No index-level whole-file signature check.** The file spec allows an
  optional whole-index signature for publishers who want
  membership/ordering covered too; not checked here even when present.
- **No `catalogType`/`networkIds`-based filtering.** Both are parsed and
  returned on `CatalogResult`, but nothing in this phase filters on them
  (e.g. skipping a `MASTER` catalog, or a `networkIds`-scoped one the
  caller isn't a member of) -- that's `CrawlRequest.NetworkID`'s stated
  purpose, not yet wired to any actual filtering logic.

## Config

| Key | Meaning | Default |
|---|---|---|
| `maxArtifactSize` | Byte cap per fetched artifact | 10 MiB |
| `fetchTimeout` | Per-fetch timeout (Go duration, e.g. `15s`) | none |
| `retryMax` | Max retries on transient fetch failure | 0 |

## Running it locally

`catalogPull` is wired alongside the existing `bapTxnReceiver`/
`bapTxnCaller` caller/receiver modules in
[`config/local-beckn-one-bap.yaml`](../../../../config/local-beckn-one-bap.yaml).

```bash
./install/build-plugins.sh        # builds plugins/catalogcrawler.so, among others
go run ./cmd/adapter --config=config/local-beckn-one-bap.yaml
```

Then, from another terminal, point `receiverId` at any domain serving a
file-spec-shaped manifest at `/.well-known/dedi.index.json` (see below for
how to stand one up locally):

```bash
curl -X POST http://localhost:8081/catalog/pull \
  -H "Content-Type: application/json" \
  -d '{
    "receiverId": "https://your-test-domain.example",
    "networkId": "ret-food:1.0.0",
    "mode": "full"
  }'
```

`config/local-beckn-one-bap.yaml`'s `cache`/`registry` blocks under the
`catalogPull` module exist only to satisfy `keyManager`'s constructor
(`KeyManagerProvider.New` always requires a `RegistryLookup`) -- the
crawler itself never calls either. If you're running standalone without
`docker-compose`'s `redis` host, override `cache.config.addr` to
`localhost:6379` (or point it at any reachable Redis).

## Trying it against a live fixture

There is no currently-hosted live fixture in this shape (the old one,
`angular-absently-gab.ngrok-free.dev`, predates this rewrite and serves
the superseded DeDi-wrapper format). To stand up your own: publish with
[`catalogpublisherctl`](../../../../cmd/catalogpublisherctl) into a
directory, serve that directory over plain HTTP, and point
`CatalogBaseURL`/`IndexURL` at that server before publishing so the
written URLs are actually fetchable (not `file://`):

```bash
go run ./cmd/catalogpublisherctl \
  -catalog pkg/plugin/implementation/catalogpublisher/testdata/sample-catalog-v1.json \
  -out /tmp/catalog-demo -domain localhost:8000

cd /tmp/catalog-demo && python3 -m http.server 8000
```

Then, from another terminal, run the crawler's round-trip tests (which do
exactly this over an in-process `httptest` server, no manual serving
needed) or point a `catalogPull` request's `receiverId` at
`http://localhost:8000`.

## Known open items

- Re-enable signed index/catalog GETs once a participant in the test setup
  expects them (`fetchOpts` in `catalogcrawler.go`).
- `keyManager`'s constructor requiring a `RegistryLookup` even when this
  handler has no real use for one is existing friction, not introduced
  here -- every other module hits the same thing when using static keys.
- No caching, version-rollback detection, or scheduling yet.
- `catalogType`/`networkIds` filtering (see "Deliberately not done").
- Index-level whole-file signature check (optional per the file spec).

## Signature verification

Two different schemes for two different documents, matching
`catalogpublisher`'s producing side exactly:

- **The manifest** carries one whole-document detached JWS.
  `verifyManifestProof` uses `artifactverifier.VerifyDetachedJWS`, which
  JCS-canonicalizes the document with its `proof` field removed,
  reconstructs the RFC 7797 (`b64:false`) signing input, and verifies with
  Ed25519. (An earlier version of this check verified over the full
  document *including* its own embedded signature and treated `proof.jws`
  as a raw base64 signature rather than a compact detached JWS --
  self-referential and never actually authenticating anything, masked by
  a placeholder `jws` value in the old reference fixture always failing to
  decode.)
- **Every catalog file** (baseline or a change file) carries its own
  signature -- a plain Ed25519 signature over the JCS-canonicalized tuple
  `{catalogId, version, url, digest, validUntil}`. `fetchAndVerifyFile`
  uses `artifactverifier.VerifyFileTuple`, the exact counterpart to
  `catalogpublisher`'s `artifactsigner.SignFileTuple` -- the two are kept
  byte-for-byte in sync deliberately (a shared round-trip test in
  `pkg/security/artifactsigner` asserts this directly), to avoid a repeat
  of the manifest-proof asymmetry bug above.

See [`pkg/catalogfile`](../../../catalogfile) for the shared
change-file-application logic both `catalogpublisher`'s CLI and this
package use to compose a catalog's current content from its baseline plus
every change file since.
