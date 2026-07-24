# catalogpublisher

Implements `definition.CatalogPublisher`: given a publisher's catalog
submissions, produces a signed DeDi manifest and a signed catalog index
whose wire shape matches exactly what
[`catalogcrawler`](../catalogcrawler) consumes. This is the producing side
of the same three-level chain (manifest → index → catalog) the crawler
walks and verifies -- see `onix-catalog-crawler-plugin-requirements.md` for
the background, and the decentralized-catalog design doc's "Publisher
tooling" section for why this exists as a plugin rather than bespoke
per-publisher code.

## What this phase (MVP) does

1. Takes a `PublishRequest`: one `CatalogSubmission` per `catalogId`, each
   carrying a plain Beckn `Catalog` object (no `context`/`message`
   envelope) plus publisher-declared `Visibility` and `SchemaTypes`.
2. Validates each submission with the same shallow structural check
   `catalogcrawler` applies on the way in (`id` + `descriptor` present). A
   bad submission is reported as a non-fatal `PublishError` and skipped --
   it does not fail the rest of the batch.
3. **Diffs against caller-supplied prior state** (`PublishRequest.PriorState`,
   keyed by `catalogId`): compares the submitted catalog's `resources`
   array (by item `id`) against `PriorCatalogState.Catalog` -- the full
   content last published, reconstructed by the caller. No prior state (or
   `ForceBaseline`) always produces a fresh **baseline** (version 1); prior
   state present and the diff empty is a no-op (`Changed: false`); prior
   state present and the diff non-empty produces a **change file**
   (`{version, added, updated, removed}`) and bumps the version. `Publish`
   itself holds no storage-backed state of its own -- see
   `definition.PriorCatalogState`'s doc comment -- it only ever diffs what
   it's given.
4. Builds one index record per valid catalog, carrying forward the
   `Baseline` part reference unchanged and appending any new `Changes`
   part; `Parts` flattens both into one list for crawlers (like
   `catalogcrawler` today) that don't yet understand baseline/change
   semantics. Digest is the hex SHA-256 of the exact file bytes, prefixed
   `sha-256:` to match the reference fixture's convention.
5. Signs the index and the manifest with a **real detached JWS**
   (`pkg/security/artifactsigner`): JCS-canonicalize the document with
   `proof` absent, build the RFC 7797 (`b64:false`) signing input, sign
   with Ed25519. This is the exact counterpart to `catalogcrawler`'s fixed
   `verifyDediProof` (see that package's own history: the original
   verification check was self-referential and never actually
   authenticated anything -- fixed alongside this MVP).
6. Returns `PublishResult{Manifest, Index, Catalogs, Errors}` as JSON. No
   I/O happens here -- where these bytes get written and served is a
   separate concern (an `ArtifactStore`-shaped plugin, not yet built).

## Try it yourself: `catalogpublisherctl`

[`cmd/catalogpublisherctl`](../../../../cmd/catalogpublisherctl) is a small
demo CLI that wraps `Publish` end to end: it reads a catalog JSON file,
reconstructs prior state from whatever it previously wrote to an output
directory, calls `Publish`, and writes the resulting manifest/index/
baseline/change files to that directory. It owns a demo-only local signing
key and all state reconstruction -- none of that lives in this package.
Run everything below from the repo root.

### 1. Publish a fresh catalog (baseline)

```bash
go run ./cmd/catalogpublisherctl \
  -catalog pkg/plugin/implementation/catalogpublisher/testdata/sample-catalog-v1.json \
  -out /tmp/catalog-demo
```

Expected output:
```
catalog CAT-DEMO-1: published baseline, version 1
  digest: sha-256:...
artifacts written to /tmp/catalog-demo
```

Inspect what got written:
```bash
find /tmp/catalog-demo -type f
cat /tmp/catalog-demo/catalog/CAT-DEMO-1/baseline.json
cat /tmp/catalog-demo/index.json      # records[].details.version == 1, baseline part + digest
cat /tmp/catalog-demo/manifest.json   # keys[] + files[] pointing at the index, both signed
```

### 2. Publish an update to the same catalog (change file + version bump)

`sample-catalog-v2.json` updates `ITEM-1`'s price, removes `ITEM-2`, and
adds `ITEM-3` -- publish it to the **same** output directory so the tool
can diff against what it wrote in step 1:

```bash
go run ./cmd/catalogpublisherctl \
  -catalog pkg/plugin/implementation/catalogpublisher/testdata/sample-catalog-v2.json \
  -out /tmp/catalog-demo
```

Expected output:
```
catalog CAT-DEMO-1: published change file, version 2
  +1 added, ~1 updated, -1 removed
  digest: sha-256:...
artifacts written to /tmp/catalog-demo
```

```bash
cat /tmp/catalog-demo/catalog/CAT-DEMO-1/change-2.json   # {"version":2,"added":[...ITEM-3],"updated":[...ITEM-1],"removed":["ITEM-2"]}
cat /tmp/catalog-demo/index.json                          # version now 2; baseline part unchanged; changes[] has one new entry
```

### 3. Publish the same content again (no-op)

```bash
go run ./cmd/catalogpublisherctl \
  -catalog pkg/plugin/implementation/catalogpublisher/testdata/sample-catalog-v2.json \
  -out /tmp/catalog-demo
```
```
catalog CAT-DEMO-1: unchanged, still version 2
  digest:
artifacts written to /tmp/catalog-demo
```
No new file is written -- `find /tmp/catalog-demo/catalog/CAT-DEMO-1 -type f` still shows only `baseline.json` and `change-2.json`.

### 4. Try it with your own catalog file

Any JSON file matching `{id, descriptor, provider, resources: [{id, ...}, ...]}`
works -- point `-catalog` at it and give it a fresh `-out` directory. Edit
the file (change/add/remove entries in `resources`) and rerun against the
same `-out` to see the diff. Delete the `-out` directory to start over as
a fresh baseline.

### Flags

| Flag | Meaning | Default |
|---|---|---|
| `-catalog` | path to a Beckn Catalog JSON file (required) | -- |
| `-catalogId` | catalog id override | the catalog's own top-level `"id"` |
| `-out` | output directory for artifacts and local state | `./catalog-publish-out` |
| `-keyID` | signing key id (auto-generated into `<out>/.keys/` on first use) | `local-publisher-key` |
| `-domain` | publisher domain embedded in the manifest/index | `local.test` |
| `-forceBaseline` | ignore prior state on disk, always publish a fresh baseline | `false` |

### Running the automated tests instead

```bash
go test ./pkg/plugin/implementation/catalogpublisher/... -v
go test ./pkg/plugin/implementation/catalogcrawler/... -v -run TestPublisherToCrawler_RoundTrip
```

## Deliberately not done in this phase

- **No compaction.** Change files accumulate indefinitely; folding them
  into a fresh baseline is a follow-up phase.
- **No storage wiring.** `Config.IndexURL`/`CatalogBaseURL` are read
  straight from config; when unset, a `pending-artifact-store://...`
  placeholder URL is used so the plugin can still be exercised and tested
  before a real storage backend exists (`catalogpublisherctl` fills these
  in with `file://` paths for its own demo purposes).
- **No restricted-catalog auth-method wire format.** `AuthMethod` values
  pass straight through unencoded for now; the design doc's signed-
  challenge scheme has no settled index-entry encoding yet (tracked as an
  open item on both this plugin and the crawler).
- **`Visibility` encoding is a placeholder string** (`"public"` or
  `"networks:a,b"`) -- `catalogcrawler` only passes this string through
  today (`CatalogResult.Visibility`), it does not parse or filter on it.
- **Change files are not yet crawler-consumable.** `catalogcrawler` fetches
  every `parts[]` entry independently and expects each to look like a full
  Beckn `Catalog` (`id` + `descriptor`); a change file's `{added, updated,
  removed}` shape fails that shallow check today (delta-file support is an
  explicit open item on the crawler side). `Parts` still flattens
  baseline+changes for forward compatibility once that lands.

## Round-trip verification

`pkg/plugin/implementation/catalogcrawler/roundtrip_test.go` is the load-
bearing test for this plugin: it publishes with `catalogpublisher`, serves
the resulting manifest/index/catalog bytes over a real `httptest` server,
and crawls them back with `catalogcrawler.CrawlSubscriber`, asserting the
manifest signature verifies, the digest matches, and the catalog body
round-trips exactly. This is what actually proves the two plugins agree on
the wire format, rather than each side just testing its own assumptions
about it.

## Config

| Key | Meaning | Default |
|---|---|---|
| `keyID` | publisher's registered signing key id (`kid`), also the `KeyManager.Keyset` lookup key | required |
| `domain` | publisher's own domain, embedded as the index's `publisher.domain` | none |
| `indexURL` | where the index will be reachable once published | `pending-artifact-store://index.json` |
| `catalogBaseURL` | URL prefix for catalog part files (`{catalogBaseURL}/{catalogId}/{baseline.json \| change-<version>.json}`) | `pending-artifact-store://catalog/{catalogId}/...` |

## Known open items

- Compaction support (phase 3 of the plan).
- `ArtifactStore` wiring once a storage plugin exists, to replace the
  placeholder URLs with real published locations.
- Restricted-catalog `AuthMethod`/`Visibility` wire encoding -- currently a
  placeholder pending a settled convention.
- A `catalog/publish`-style handler (mirroring `catalogPullHandler`) for
  DS-internal HTTP triggering, and/or a CLI entry point -- this package's
  `Publisher.Publish` is a plain in-process call with no ONIX-runtime
  dependency, so either caller can be added without touching this package.
