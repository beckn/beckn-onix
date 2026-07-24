# catalogpublisher

Implements `definition.CatalogPublisher`: given a publisher's catalog
submissions, produces a signed DeDi manifest and a catalog index whose
wire shape matches **"Decentralized Catalog file spec.md"** exactly. This
is the producing side of the manifest -> index -> catalog chain a crawler
walks and verifies -- see the file spec for the full background, and the
decentralized-catalog design doc's "Publisher tooling" section for why
this exists as a plugin rather than bespoke per-publisher code.

**`catalogcrawler` has not been updated for this shape yet** (tracked as
the immediate next step -- it still expects the earlier, now-superseded
DeDi-wrapper-shaped index this package used to produce). The round-trip
test that used to prove the two plugins agree is temporarily skipped; see
"Known open items."

## What this package does

1. Takes a `PublishRequest`: one `CatalogSubmission` per `catalogId`
   (participant-scoped, e.g. `"open-economy.nfh.global/electronics-2026"`),
   each carrying a plain Beckn `Catalog` object (no `context`/`message`
   envelope) plus publisher-declared `NetworkIds` and `AuthMethods` --
   empty `NetworkIds` means public.
2. Validates each submission with the same shallow structural check
   `catalogcrawler` applies on the way in (`id` + `descriptor` present). A
   bad submission is reported as a non-fatal `PublishError` and skipped --
   it does not fail the rest of the batch.
3. **Diffs against caller-supplied prior state** (`PublishRequest.PriorState`,
   keyed by `catalogId`): compares the submitted catalog's `resources` and
   `offers` arrays (by item `id`) against `PriorCatalogState.Catalog` --
   the full content last published, reconstructed by the caller. No prior
   state (or `ForceBaseline`) always produces a fresh **baseline**; prior
   state present and the diff empty is a no-op (`Changed: false`); prior
   state present and the diff non-empty produces a **change file**
   (`{catalogId, fromVersion, toVersion, resources:{upserts,removals},
   offers:{...}, catalog}`) and bumps the version. `ForceBaseline` against
   existing prior state is also how to trigger **compaction**: a fresh
   baseline at the next version, discarding the change list.
4. Builds one catalog-index entry per valid catalog, carrying the
   `baseline` file reference forward unchanged and appending any new
   `changes` entry -- there is no flattened "parts" list anymore (that was
   this package's own invention for the earlier wire shape; the file spec
   has no equivalent, and `catalogcrawler` will need updating to fetch
   `baseline`+`changes` directly).
5. **Signs every catalog file individually**, not the index as a whole:
   each `baseline`/`changes` entry carries its own `signature.value`, a
   plain Ed25519 signature over the JCS-canonicalized tuple
   `{catalogId, version, url, digest, validUntil}` (file spec: "The signed
   entry is a tuple, not a bare hash" -- binding the signature to exactly
   one file, in one role, within its validity window). The catalog index
   document itself is **not signed as a whole** (file spec: trust rides on
   the per-entry signatures; only the manifest carries a document-level
   proof).
6. Retires catalogs on request (`PublishRequest.Retire`): a tombstone entry
   (`{catalogId, status: "RETIRED", retiredAt}`, no files) replaces
   whatever was there, so crawlers can tell "gone" apart from "never
   existed" (file spec: "A retired catalog stays as a tombstone").
7. Carries forward every other catalog untouched by this call
   (`PublishRequest.CarryForward`, raw entries the caller supplies) --
   the catalog index lists every catalog a publisher has, not just the
   ones touched in one `Publish` call.
8. Tracks an **index-level version** (`PublishResult.IndexVersion`),
   separate from any one catalog's own file versions -- the crawler's
   cursor over the whole index, bumped only when something in this call
   actually changed.
9. Signs the manifest with a **document-level detached JWS**
   (`pkg/security/artifactsigner`): JCS-canonicalize the document with
   `proof` absent, build the RFC 7515 detached-JWS signing input, sign
   with Ed25519. This is the exact counterpart to `catalogcrawler`'s fixed
   `verifyDediProof` (see that package's own history: the original
   verification check was self-referential and never actually
   authenticated anything -- fixed alongside this package's first version).
10. Returns `PublishResult{Manifest, Index, IndexVersion, Catalogs,
    Errors}` as JSON. No I/O happens here -- where these bytes get written
    and served is a separate concern (an `ArtifactStore`-shaped plugin,
    not yet built).

## Wire shapes, at a glance

**Manifest** (`.well-known/dedi.index.json`):
```json
{
  "dedi_version": "0.1",
  "type": "dedi-manifest",
  "domain": "open-economy.nfh.global",
  "keys": [{ "kid": "key-1", "kty": "OKP", "crv": "Ed25519", "x": "..." }],
  "updated_at": "...", "next_update": "...",
  "files": [
    { "name": "becknCatalogs", "url": "...", "schema": "...", "networkIds": [] }
  ],
  "proof": { "verification_method": "key-1", "canonicalization": "JCS", "jws": "..." }
}
```
No digest on the file entry (integrity comes from the index's own
per-entry signatures, not a manifest-declared hash); `name` replaces
DeDi's `registry` convention, since this entry references a Beckn file.

**Catalog index** (a plain Beckn file; **not** a DeDi file, and not signed
as a whole):
```json
{
  "participantId": "open-economy.nfh.global",
  "version": 2,
  "next_update": "...",
  "catalogs": [
    {
      "catalogId": "open-economy.nfh.global/electronics-2026",
      "catalogType": "REGULAR", "status": "ACTIVE",
      "schemaTypes": ["..."],
      "baseline": { "version": 1, "url": "...", "size": 413, "digest": "sha-256:...",
                    "signature": { "keyId": "key-1", "value": "...", "validUntil": "..." } },
      "changes": [ { "version": 2, "url": "...", "size": 336, "digest": "sha-256:...",
                     "signature": { "keyId": "key-1", "value": "...", "validUntil": "..." } } ]
    },
    { "catalogId": "...", "status": "RETIRED", "retiredAt": "..." }
  ]
}
```

**Change file**:
```json
{
  "catalogId": "...", "fromVersion": 1, "toVersion": 2,
  "resources": { "upserts": [ {"id": "...", "descriptor": {...}} ], "removals": ["..."] },
  "offers": { "upserts": [], "removals": [] }
}
```

## Try it yourself: `catalogpublisherctl`

[`cmd/catalogpublisherctl`](../../../../cmd/catalogpublisherctl) is a small
demo CLI that wraps `Publish` end to end: it reads a catalog JSON file,
reconstructs prior state (and every other catalog's index entry, to carry
forward) from whatever it previously wrote to an output directory, calls
`Publish`, and writes the resulting manifest/index/catalog files to that
directory. It owns a demo-only local signing key and all state
reconstruction -- none of that lives in this package. Run everything below
from the repo root.

### 1. Publish a fresh catalog (baseline)

```bash
go run ./cmd/catalogpublisherctl \
  -catalog pkg/plugin/implementation/catalogpublisher/testdata/sample-catalog-v1.json \
  -out /tmp/catalog-demo -domain open-economy.nfh.global
```

Expected output:
```
catalog open-economy.nfh.global/CAT-DEMO-1: published baseline, version 1
  digest: sha-256:...
index version 1, artifacts written to /tmp/catalog-demo
```

(`-catalogId` defaults to `{domain}/{the catalog's own top-level "id"}` --
here, `sample-catalog-v1.json`'s `id` is `CAT-DEMO-1`.)

Inspect what got written:
```bash
find /tmp/catalog-demo -type f
cat /tmp/catalog-demo/catalogs/CAT-DEMO-1.v1.json          # the baseline -- unchanged Beckn Catalog JSON
cat /tmp/catalog-demo/dedi/becknCatalogs.index.json         # participantId/version/catalogs[], per-file signatures
cat /tmp/catalog-demo/.well-known/dedi.index.json           # keys[] + files[] pointing at the index, signed --
                                                              # this filename/path is deliberate, see below;
                                                              # it is NOT the catalog index.
```

### 2. Publish an update to the same catalog (change file + version bump)

`sample-catalog-v2.json` updates `ITEM-1`'s price, removes `ITEM-2`, and
adds `ITEM-3` -- publish it to the **same** output directory so the tool
can diff against what it wrote in step 1:

```bash
go run ./cmd/catalogpublisherctl \
  -catalog pkg/plugin/implementation/catalogpublisher/testdata/sample-catalog-v2.json \
  -out /tmp/catalog-demo -domain open-economy.nfh.global
```

Expected output:
```
catalog open-economy.nfh.global/CAT-DEMO-1: published change file, version 2
  resources: 2 upserts, 1 removals; offers: 0 upserts, 0 removals
  digest: sha-256:...
index version 2, artifacts written to /tmp/catalog-demo
```

```bash
cat /tmp/catalog-demo/catalogs/CAT-DEMO-1.v2.changes.json   # {"catalogId":...,"fromVersion":1,"toVersion":2,"resources":{"upserts":[...ITEM-1,ITEM-3],"removals":["ITEM-2"]},"offers":{}}
cat /tmp/catalog-demo/dedi/becknCatalogs.index.json          # index version now 2; baseline entry unchanged (same signature); changes[] has one new signed entry
```

### 3. Publish the same content again (no-op)

```bash
go run ./cmd/catalogpublisherctl \
  -catalog pkg/plugin/implementation/catalogpublisher/testdata/sample-catalog-v2.json \
  -out /tmp/catalog-demo -domain open-economy.nfh.global
```
```
catalog open-economy.nfh.global/CAT-DEMO-1: unchanged, still version 2
  digest:
index version 2, artifacts written to /tmp/catalog-demo
```
No new file is written, and the index-level version also stays at 2 (it
only bumps when something actually changed).

### 4. Retire a catalog

```bash
go run ./cmd/catalogpublisherctl -retire "open-economy.nfh.global/CAT-DEMO-1" -out /tmp/catalog-demo -domain open-economy.nfh.global
```
```
catalog open-economy.nfh.global/CAT-DEMO-1: marked RETIRED
index version 3, artifacts written to /tmp/catalog-demo
```
```bash
cat /tmp/catalog-demo/dedi/becknCatalogs.index.json   # {"catalogId":"...","status":"RETIRED","retiredAt":"..."} -- no files, a tombstone
```
`-retire` works with or without `-catalog` in the same invocation, and
accepts a comma-separated list.

### 5. Try it with your own catalog file

Any JSON file matching `{id, descriptor, provider, resources: [{id, ...}],
offers: [{id, ...}]}` works (`offers` is optional) -- point `-catalog` at
it and give it a fresh `-out` directory. Edit the file and rerun against
the same `-out` to see the diff. Delete the `-out` directory to start over
as a fresh baseline.

### The manifest's filename

The manifest -- `keys[]` + `files[]` pointing at the catalog index -- is
written to **`.well-known/dedi.index.json`**, matching the file spec's
stated well-known path exactly. Despite the filename, this is **not** the
catalog index -- that's the separate `dedi/becknCatalogs.index.json` file.
The "index" in `dedi.index.json` is just DeDi's own naming convention for
"the published record naming everything about a domain," a coincidence
with the unrelated "catalog index" concept, not a hint that they're the
same file.

### Output layout

```
<out>/
  .well-known/
    dedi.index.json              # the manifest
  dedi/
    becknCatalogs.index.json     # the catalog index
  catalogs/
    <localName>.v<version>.json           # a baseline
    <localName>.v<version>.changes.json   # a change file
  .keys/
    <keyID>.json                 # demo-only local signing key
```
`<localName>` is `catalogId` with any `domain/` prefix stripped (matching
the file spec's own example: `open-economy.nfh.global/electronics-2026` ->
`electronics-2026.v40.json`). Files are flat -- one shared directory, not
one subdirectory per catalog -- matching the file spec's own example URLs.

### Flags

| Flag | Meaning | Default |
|---|---|---|
| `-catalog` | path to a Beckn Catalog JSON file | -- |
| `-catalogId` | catalog id; a bare name (no `/`) is prefixed with `-domain` | `{domain}/{the catalog's own top-level "id"}` |
| `-out` | output directory for artifacts and local state | `./catalog-publish-out` |
| `-keyID` | signing key id (auto-generated into `<out>/.keys/` on first use) | `local-publisher-key` |
| `-domain` | publisher domain -- the manifest's `domain` and the index's `participantId` | `local.test` |
| `-indexSchemaURL` | JSON-Schema URL for the catalog-index document shape | the live reference fixture's schema URL |
| `-nextUpdateDays` | days until `next_update` expires; `0` omits it | `14` |
| `-fileValidityDays` | days until each file's `signature.validUntil` expires; `0` falls back to `-nextUpdateDays` | `14` |
| `-retire` | comma-separated catalogIds to mark RETIRED this run | *(empty)* |
| `-forceBaseline` | publish a fresh baseline for `-catalog`, discarding its change history (also how to trigger compaction) | `false` |

At least one of `-catalog` or `-retire` is required.

### Running the automated tests instead

```bash
go test ./pkg/plugin/implementation/catalogpublisher/... -v
```

## Deliberately not done in this package

- **No compaction scheduling.** `-forceBaseline` triggers it manually;
  automatic triggers (change-list size/count threshold, or a schedule) are
  a follow-up.
- **No grace-period deletion of superseded files** after compaction (file
  spec: "Old files remain for a grace period... then are deleted").
- **No storage wiring.** `Config.IndexURL`/`CatalogBaseURL` are read
  straight from config; when unset, a `pending-artifact-store://...`
  placeholder URL is used so the plugin can still be exercised and tested
  before a real storage backend exists (`catalogpublisherctl` fills these
  in with `file://` paths for its own demo purposes).
- **`diffCatalogAttributes` is a best-effort subset**, not a complete
  implementation of the change file's optional `catalog` object. The file
  spec names "name, validity window" as examples of catalog-level
  attribute changes without pinning an exact shape; this package currently
  only detects changes to `descriptor` and `provider`.
- **ES256 support.** The file spec accepts both Ed25519 and ES256 keys;
  this package only implements Ed25519 (matching its own examples).
- **The per-entry signature's exact encoding.** The file spec explicitly
  allows either a plain signature value (what this package does) or a
  detached JWS "to match DeDi's proof encoding... the final encoding is a
  schema decision, not a semantic one." A plain Ed25519 signature over the
  JCS-canonicalized tuple was chosen as the simpler of the two allowed
  options.
- **Whole-index signature.** The file spec allows an optional whole-file
  signature for publishers who want membership/ordering covered too; not
  implemented here.
- **Restricted catalogs/index.** `NetworkIds`/`AuthMethods` pass straight
  through to the wire format correctly, but nothing here implements the
  signed-challenge gateway side (that's `catalogcrawler`/a gateway's job,
  not the publisher's).

## Known open items

- **`catalogcrawler` has not been updated for this wire shape.** It still
  expects the earlier DeDi-wrapper-shaped index (`publisher`/`registry`/
  `records[].details`/flattened `parts[]`, one whole-document proof) this
  package used to produce before this rewrite. `pkg/plugin/implementation/
  catalogcrawler/roundtrip_test.go` is temporarily `t.Skip`-ed with this
  reason -- rewriting the crawler to match the file spec (participantId/
  version/catalogs[]/baseline+changes, per-entry signature verification,
  version-regression checks against the file-spec's monotonic version
  rule, tombstone handling) is the immediate next step.
- Compaction scheduling and grace-period cleanup (see above).
- `ArtifactStore` wiring once a storage plugin exists, to replace the
  placeholder URLs with real published locations.
- A `catalog/publish`-style handler (mirroring `catalogPullHandler`) for
  DS-internal HTTP triggering, and/or a non-demo CLI/desktop packaging --
  this package's `Publisher.Publish` is a plain in-process call with no
  ONIX-runtime dependency, so either caller can be added without touching
  this package.
