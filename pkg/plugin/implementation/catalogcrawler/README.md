# catalogcrawler

Implements `definition.Crawler`: walks a provider node's published
manifest -> index -> catalog chain (DeDi-published, self-hosted catalogs)
and returns the verified result. See
`onix-catalog-crawler-plugin-requirements.md` for the full protocol
background.

Exposed to the DS's own backend via the `catalogPull` handler type
(`core/module/handler/catalogPullHandler.go`), not as a network-facing
Beckn action -- `/catalog/pull` is an internal, unsigned trigger: same
operator, same trust domain, no `validateSign`/`addRoute`/`signAck`
pipeline.

## What this phase does

1. Parses the DS-supplied `receiverId` directly into a domain -- **no
   registry lookup**. The DS already knows which provider it wants to
   sync. `receiverId` mirrors `Context.receiverId` from beckn.yaml (the
   PN's DID), but for now its value is treated as a literal domain/URI --
   real DID resolution isn't implemented yet, this is a field-name
   alignment with the spec only.
2. Fetches `{domain}/.well-known/dedi.json` **unsigned** (it's a public,
   RFC 8615 well-known endpoint) and verifies it against its own embedded
   `keys[]` (JWK, OKP/Ed25519).
3. For each `files[]` entry whose `schema` identifies a
   `BecknCatalogIndexRecord`, fetches that index, checks its digest against
   what the manifest declared, and verifies the index's own detached
   `proof.jws` against its `publisher.key` (logged, not gating -- see §9).
4. For each `records[].details.parts[]` entry, fetches the catalog part
   and checks: digest match against the index's declaration, freshness
   (`next_update`, when present), status (`ACTIVE`/`LIVE`), and a shallow
   structural check (a Beckn `Catalog` object has no `context` envelope --
   it's the bare `{id, descriptor, provider, resources}` shape). **Catalog
   parts carry no signature of their own by design** -- their integrity is
   inherited entirely from the digest the index declared for them, so
   there is no signature check at this level, unlike the manifest and
   index.
5. One bad index or catalog part becomes a non-fatal `CrawlError`; it
   never fails the whole `CrawlResult` (FR11).
6. The DS-facing response (`catalogPullHandler.ServeHTTP`) matches
   beckn.yaml's `CatalogPullCallbackAction` exactly:
   `{"status": "COMPLETED", "catalogs": [...]}` on success, or
   `{"status": "FAILED", "error": {"code": "BIZ_CRAWL_FAILED", "message": "..."}}`
   on a fatal crawl failure. No other crawler-internal bookkeeping
   (`catalogId`, `version`, digests, verification outcomes) is returned to
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
  one-line change to re-enable once a PN in the test setup expects it.
- **No catalog-level signature check, by design.** Catalog parts don't
  carry a `proof.jws` at all -- only the manifest and the index do. A
  part's integrity comes entirely from the digest its index declared for
  it (checked), not from a signature (there isn't one to check).

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

Then, from another terminal:

```bash
curl -X POST http://localhost:8081/catalog/pull \
  -H "Content-Type: application/json" \
  -d '{
    "receiverId": "https://angular-absently-gab.ngrok-free.dev",
    "networkId": "ret-food:1.0.0",
    "mode": "full"
  }'
```

This has been run end-to-end against the live reference fixture
(`angular-absently-gab.ngrok-free.dev`, described in §9 of the
requirements doc): manifest fetched and its signature checked, the index
digest-checked and its own signature checked, and the one catalog part
(`CAT-GENERIC-001`) fetched with a matching digest and valid shape --
without failing the crawl. The manifest's and index's signature checks
both come back `false` against this fixture, since its `proof.jws` is a
literal placeholder (`"UNSIGNED_LOCAL_TEST_DATA_NO_CRYPTOGRAPHIC_SIGNATURE"`),
which is expected and non-fatal (§9). The DS-facing response is
`{"status": "COMPLETED", "catalogs": [...]}` -- these verification
details are logged, not returned.

`config/local-beckn-one-bap.yaml`'s `cache`/`registry` blocks under the
`catalogPull` module exist only to satisfy `keyManager`'s constructor
(`KeyManagerProvider.New` always requires a `RegistryLookup`) -- the
crawler itself never calls either. If you're running standalone without
`docker-compose`'s `redis` host, override `cache.config.addr` to
`localhost:6379` (or point it at any reachable Redis).

## Known open items

- Re-enable signed index/catalog GETs once a PN in the test setup expects
  them (`fetchOpts` in `catalogcrawler.go`).
- `keyManager`'s constructor requiring a `RegistryLookup` even when this
  handler has no real use for one is existing friction, not introduced
  here -- every other module hits the same thing when using static keys.
- No incremental digest-skip, version-rollback detection, or scheduling
  yet (Phases 2-4 of the requirements doc's phasing).
