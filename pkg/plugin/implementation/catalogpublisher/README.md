# catalogpublisher

Implements `definition.CatalogPublisher`: given a publisher's catalog
submissions, produces a catalog index whose wire shape matches
**"Decentralized Catalog file spec.md"** exactly. This is the producing
side of the index -> catalog chain a crawler walks and verifies -- see the
file spec for the full background, and the decentralized-catalog design
doc's "Publisher tooling" section for why this exists as a plugin rather
than bespoke per-publisher code.

This package does not produce a DeDi manifest. The catalog index's
location is declared directly in the publisher's own DeDi registry
record (`meta.catalog_index_url`), not via a separate node-manifest
document's `catalog.catalogIndexes` indirection -- see "Node-manifest
link check" below for how `catalogPublishHandler.go` checks and reports
this. An earlier version of this package computed and signed a manifest
as part of every `Publish` call, but nothing ever consumed it -- see git
history for that version.

**`catalogcrawler` has not been updated for this shape yet** (tracked as
the immediate next step -- it still expects the earlier, now-superseded
DeDi-wrapper-shaped index this package used to produce). The round-trip
test that used to prove the two plugins agree is temporarily skipped; see
"Known open items." This package now also tracks the file spec's v2
revision (`nodeId` instead of `participantId`, per-catalog-entry and
per-file self-signing instead of a per-file signed tuple, no restricted
catalogs) -- widening the gap until `catalogcrawler` catches up.

## What this package does

1. Takes a `PublishRequest`: one `CatalogSubmission` per `catalogId`
   (participant-scoped, e.g. `"open-economy.nfh.global/electronics-2026"`),
   each carrying a plain Beckn `Catalog` object (no `context`/`message`
   envelope) plus publisher-declared `NetworkIds`. Catalogs are public,
   unconditionally (file spec v2, "Catalog access is public") --
   `NetworkIds` is a Discovery-service relevance filter only, never an
   access control; there is no restricted-catalog concept.
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
5. **Two independent, per-catalog signature layers** (RFC NFH-014), not a
   whole-index signature: every catalog **file** (baseline and change file
   alike) self-signs its own content -- a plain Ed25519 signature
   (`pkg/security/artifactsigner.SignJSON`) over the JCS-canonicalized file
   document with its own `signature` field removed ("avoiding circular
   signing") -- and every catalog-index **entry** separately self-signs
   itself as a whole (`catalogId`, `entryVersion`, `catalogType`,
   `dependencies`, `networkIds`, `schemaTypes`, `isActive`, every
   `baseline`/`changes[]` file reference, `retiredAt`, and `crawlHint`
   together), the same non-circular convention one level up. Neither
   signature carries an expiry (no `validUntil`); the index's own
   `next_update` bounds staleness instead. The catalog index document
   itself is still **not signed as a whole** -- NFH-014 deliberately has no
   whole-index version field either: a DS detects index change via
   conditional HTTP, not a forgeable document-level counter.
6. Tracks an **entry-level version** (`entryVersion`, on every
   `CatalogPublishOutcome.EntryVersion`), distinct from `baseline`/
   `changes[]`'s own file-lineage versions: it bumps on *any* entry
   change, content or metadata (`networkIds`/`schemaTypes`/`catalogType`/
   `dependencies`/`isActive`/`crawlHint`), so a caller can detect and
   propagate even a metadata-only edit (`CatalogPublishOutcome.Mode ==
   "metadata"`) without a new file being published.
7. Retires catalogs on request (`PublishRequest.Retire`): a tombstone
   entry (`{catalogId, entryVersion, catalogType, networkIds,
   schemaTypes, retiredAt}`, no `isActive`/files) replaces whatever was
   there -- its prior metadata survives retirement, only `isActive` and
   the file references are dropped -- so crawlers can tell "gone" apart
   from "never existed" (NFH-014 §10.4, "a retired catalog stays as a
   tombstone"). If `PriorCatalogState.LatestPublished` was set for that
   catalog, also makes one final write to its "latest" URL (via
   `PublishResult.RetiredLatest`), a self-signed `CatalogFile` now
   carrying `retiredAt` -- so a consumer that only ever fetches `latest`
   directly, never revisiting the index, can still learn the catalog is
   gone (NFH-014 CON-TBD-38). Independent of whether `Config.PublishLatest`
   is on for this call.
8. Carries forward every other catalog untouched by this call
   (`PublishRequest.CarryForward`, raw entries the caller supplies) --
   the catalog index lists every catalog a publisher has, not just the
   ones touched in one `Publish` call.
9. On a forced re-baseline (compaction), keeps the pre-compaction change
   files **listed**, not just hosted (NFH-014 CON-TBD-32) -- `Publish`
   never resets `changes[]` to empty on its own; a caller decides how
   long to keep passing them back in `PriorCatalogState.ChangeFiles`
   (`Publish` holds no timer or storage of its own). `localstore`'s own
   policy (see below) implements CON-TBD-32's concrete minimum: it keeps
   listing them until the compacted baseline's own `next_update` has
   passed, then stops.
10. Maintains a **`latest` pointer** (`Config.PublishLatest`) -- a full,
    self-signed `CatalogFile` overwritten in place at one stable,
    non-versioned URL, for consumers who want fully-current content
    without ever applying `changes[]`. Regenerated from the submitted
    catalog on every call regardless of `Mode`, unlike `baseline`/
    `changes[]` which are only written when content actually changes.
    Explicitly exempt from the immutable-URL rule every other catalog
    file here follows (NFH-014 CON-TBD-36). On by default for the
    `catalogPublish` plugin config (opt out with `publishLatest:
    "false"`); `catalogpublisher.Config`'s own zero value stays off for
    direct programmatic callers.
11. Serves every catalog file **gzip-compressed** (`Config.Gzip`), signaled
    purely by a `.json.gz`/`.changes.json.gz` URL extension (NFH-014
    §10.1) -- never a header or content negotiation. Digest and signature
    are always computed over the canonical, decompressed bytes
    (`CatalogPublishOutcome.Content`/`LatestContent`); the compressed
    bytes to actually write are `ServedContent`/`LatestServedContent`, and
    the index entry's reported `size` is the compressed, actually-served
    size. `localstore` reads a stored file's own declared URL to decide
    whether to decompress it, not the current `Config.Gzip` -- so mixed
    compression history (some files compressed, some not, across
    different past publishes) reconstructs correctly regardless. On by
    default for the CLI and plugin config (opt out with `-gzip=false` /
    `gzip: "false"`); `catalogpublisher.Config`'s own zero value stays off
    for direct programmatic callers.
12. Returns `PublishResult{Index, Catalogs, Errors}` as JSON. No I/O
    happens here -- where these bytes get written and served is a
    separate concern (an `ArtifactStore`-shaped plugin, not yet built).

## Wire shapes, at a glance

**Catalog index** (a plain Beckn file; **not** a DeDi file, and not signed
as a whole -- but every entry self-signs itself; no whole-index `version`
field, see point 5 above):
```json
{
  "nodeId": "open-economy.nfh.global",
  "next_update": "...",
  "catalogs": [
    {
      "catalogId": "open-economy.nfh.global/electronics-2026",
      "entryVersion": 7,
      "catalogType": "REGULAR",
      "schemaTypes": ["..."],
      "isActive": true,
      "dependencies": { "masters": [ { "catalogId": "...", "version": 12, "indexUrl": "..." } ] },
      "baseline": { "version": 1, "url": "...", "size": 413, "digest": "sha-256:..." },
      "changes": [ { "fromVersion": 1, "toVersion": 2, "url": "...", "size": 336, "digest": "sha-256:..." } ],
      "latest": { "version": 2, "url": "...CAT-DEMO-1.latest.json", "size": 420, "digest": "sha-256:..." },
      "signature": { "keyId": "key-1", "value": "..." }
    },
    { "catalogId": "...", "entryVersion": 21, "catalogType": "REGULAR", "retiredAt": "...", "signature": { "keyId": "key-1", "value": "..." } }
  ]
}
```
`baseline`/`changes[]` entries carry no signature of their own -- their
`digest`/`size` describe the already self-signed file they point at (see
below); the catalog-**entry**'s own `signature` covers everything shown
above it, `signature` itself excluded. `changes[]` entries carry
`fromVersion`/`toVersion` (mirroring the change file's own fields), not a
single `version` -- so a DS can confirm the chain is contiguous with its
stored cursor directly from the index, before fetching anything.
`dependencies.masters[].version` is the MASTER's `baseline.version` last
validated against, kept current by the caller as that changes.

**Baseline file** (published at a `baseline` entry's `url`, self-signed):
```json
{
  "catalogId": "...", "version": 1, "next_update": "2026-08-05T00:00:00Z",
  "catalog": { "id": "...", "descriptor": {...}, "provider": {...}, "resources": [...], "offers": [] },
  "signature": { "keyId": "key-1", "canonicalization": "JCS", "value": "..." }
}
```

**Change file** (published at a `changes[]` entry's `url`, self-signed):
```json
{
  "catalogId": "...", "fromVersion": 1, "toVersion": 2, "next_update": "2026-08-05T00:00:00Z",
  "resources": { "upserts": [ {"id": "...", "descriptor": {...}} ], "removals": ["..."] },
  "offers": { "upserts": [], "removals": [] },
  "signature": { "keyId": "key-1", "canonicalization": "JCS", "value": "..." }
}
```
`catalogId`/`version`/`next_update` on the file itself mirror the index
entry's own identity/freshness fields, so each file is independently
verifiable without needing the index entry that points at it.
`next_update` here comes from the same `nextUpdateIn` config as the
index's own `next_update`, falling back to a fixed 24h when unset (the
index's is optional and simply omitted in that case; a catalog file's is
required by the schema, so it always gets a value).

### Running the automated tests instead

```bash
go test ./pkg/plugin/implementation/catalogpublisher/... -v
```

## Deliberately not done in this package

- **Compaction scheduling is partially done.** Automatic baseline
  compaction by change-file count (`Config.CompactionChangeCountThreshold`)
  or combined change-file size relative to baseline
  (`Config.CompactionSizeRatioThreshold`) is implemented -- either
  threshold, checked against `PriorCatalogState.ChangeFiles` before a new
  change file would be created, substitutes a fresh baseline (same as
  `ForceBaseline`) for what would otherwise be one more change file. A
  fixed-schedule trigger is not implemented (no timer of its own --
  `Publish` is a pure function). **Change-file-only compaction** (NFH-014
  §10.1's other compaction type: squashing several change files into one
  spanning the same range, without touching the baseline) is also not
  implemented -- only baseline compaction. Grace-period expiry of
  superseded change files after a baseline compaction (CON-TBD-32) *is*
  implemented, in `localstore.Load`/`reconstructState` -- see the package
  doc above and `localstore`'s own tests.
- **No storage wiring.** `Config.PublicBaseURL` is read straight from
  config -- one URL prefix for everything a publish writes, mirroring
  wherever `outputRoot` (see `localstore`) is actually served from
  publicly (e.g. an ngrok tunnel onto that one directory). The index is
  addressed at `{PublicBaseURL}/index/becknCatalogs.index.json`, baselines
  at `{PublicBaseURL}/catalogs/<localName>.v<version>.json`, and change
  files at `{PublicBaseURL}/catalogs/changes/<localName>.v<version>.changes.json`.
  When unset, a `pending-artifact-store://...` placeholder URL is used so
  the plugin can still be exercised and tested before a real public
  location exists.
- **`diffCatalogAttributes` is a best-effort subset**, not a complete
  implementation of the change file's optional `catalog` object. The file
  spec names "name, validity window" as examples of catalog-level
  attribute changes without pinning an exact shape; this package currently
  only detects changes to `descriptor` and `provider`.
- **ES256 support.** The file spec accepts both Ed25519 and ES256 keys;
  this package only implements Ed25519 (matching its own examples).
- **The signatures' exact encoding.** Both the catalog-entry and the
  catalog-file self-signatures are plain Ed25519 values, not a detached
  JWS -- the simpler of the file spec's allowed encodings.
- **Whole-index signature.** The file spec allows an optional whole-file
  signature for publishers who want membership/ordering covered too; not
  implemented here.
- **Restricted catalogs.** Not a gap -- file spec v2 removed the concept
  entirely. Catalogs are public, unconditionally; there is no restricted
  catalog, no download gate, and no per-catalog authentication method.

## Local persistence: `localstore`

[`localstore`](localstore) is the shared "write a `Publish` result to a
local directory, read it back as prior state" logic -- the same layout
`catalogpublisherctl` used to write in full (`index/becknCatalogs.index.json`,
flat `catalogs/<name>.v<version>.json` (baselines) and
`catalogs/changes/<name>.v<version>.changes.json` (change files)),
extracted so both the CLI and the `catalogPublish` HTTP handler
(below) use one implementation instead of two. `Publish` itself still
holds no storage-backed state -- `localstore.Load`/`Write` are one
concrete, filesystem-backed way to supply and persist
`definition.PriorCatalogState`/`PublishResult`, not part of the core
plugin's own logic. `localstore.Load` reads the index once and returns
prior state for every catalogId asked for, plus every other catalog's raw
entry to carry forward untouched -- see its doc comments for the exact
contract.

## HTTP handler: `catalog/publish`

`core/module/handler/catalogPublishHandler.go` (`NewCatalogPublishHandler`)
exposes this plugin's publish capability as a DS-internal, unsigned
trigger -- mirroring `catalogPullHandler` exactly: no
`validateSign`/`addRoute`/`signAck` pipeline, since the caller is the
operator's own tooling, not another network participant. Request body
matches beckn.yaml's real `CatalogPublishAction` envelope shape
(`context`/`message.catalogs[]`/`message.publishDirectives[]`) as closely
as possible, so `schemaValidator`/`policyChecker` (below) can validate the
raw request directly with no synthesized wrapper:
```json
{
  "context": { "action": "catalog/publish" },
  "message": {
    "catalogs": [ { "id": "ds.local.dev/CAT-1", "descriptor": {...}, "provider": {...}, "resources": [...] } ],
    "publishDirectives": [ { "catalogId": "ds.local.dev/CAT-1", "catalogType": "REGULAR", "visibleTo": ["retail-network"] } ]
  },
  "retire": ["..."], "forceBaseline": false
}
```
`context` carries only `action` -- the real spec leaves every other
`Context` field optional, and none of them (`bapId`/`bapUri`/`messageId`/
...) are meaningful for this unsigned, same-operator trigger.
`publishDirectives[]` is beckn.yaml's own construct (matched to a catalog
by `catalogId`, alongside `updateMode`/`resourceDirectives` which this
handler doesn't act on yet). `catalogType` (`MASTER`/`REGULAR`) is
required by the schema (unlike `CatalogSubmission.CatalogType`, which
defaults to `"REGULAR"` on its own when empty -- callers must still set it
explicitly here once `schemaValidator` is configured) and `visibleTo` is
wired onto `CatalogSubmission.NetworkIds`; both map directly onto the
same-named fields (`catalogType`/`networkIds`) in the published index.
`retire`/`forceBaseline` have no beckn.yaml equivalent and stay as this
handler's own siblings of `context`/`message`. Each catalog's own top-level `"id"` is used verbatim
as its `catalogId` -- the handler does not prefix or derive it from a
domain; submit the full id you want. Response borrows beckn.yaml's
`CatalogPublishAction`/`CatalogProcessingResult` vocabulary
(`ACCEPTED`/`REJECTED` per catalog) but as one synchronous call, not an
Ack-now/`on_publish`-later pair -- beckn.yaml's async, signed
`CatalogPublishAction` is a materially larger scope (routing, subscriber
lookup, callback signing) that this handler deliberately does not
implement; see "Known open items."

Note: beckn.yaml's `schemaTypes` concept only exists on `catalog/
subscription`'s `CatalogSubscribeAction` (a subscriber declaring interest
in updates) -- there is no publish-time directive for it, so
`CatalogSubmission.SchemaTypes` is not wired to any request field yet and
is never inferred from catalog content either.

Config: a new `outputRoot` field on the handler config (not a plugin
`config:` map -- there was no precedent for a handler-level scalar besides
`role`/`subscriberId`/`basePath`, so `outputRoot` joins them) is the
common local directory every generated file goes under; `plugins.catalogPublisher`
wires the core plugin the same way `plugins.crawler` wires `catalogcrawler`
for `catalogPull`. See `config/local-beckn-one-bap.yaml`'s and
`config/local-beckn-one-bpp.yaml`'s `catalogPublish` module blocks for
full working examples (the bpp one also has `schemaValidator` active and
`checkCatalogIndexLink: true` set, matching starter-kit's
`generic-bpp.yaml`).

**Config has one field: `subscriberId`.** There is no `keyID`/`domain` in
`catalogpublisher.Config` -- `subscriberId` is only the `KeyManager.Keyset`
lookup key (the same one every other `Keyset` caller uses, e.g.
`pkg/security/artifactfetcher`, `core/module/handler/responsestep.go`).
Every signature's `keyId` and the index's `nodeId` are read
from the returned `Keyset`'s `UniqueKeyID`/`SubscriberID` fields instead --
whatever the KeyManager plugin's own config (e.g. `simplekeymanager`'s
`subscriberId`/`keyId`) already populated them with, not duplicated here.
This used to be a real gotcha: `simplekeymanager`'s config-loaded keyset
is indexed by `subscriberId` (see `loadKeysFromConfig` in
`simplekeymanager.go`), so the old `catalogPublisher.config.keyID` had to
be set to the *subscriberId* value, not the `keyId` string sitting right
next to it in the same config block -- easy to get backwards. Removing
the duplicate field removes the chance to get it backwards.

**The `catalogPublish` handler goes one step further: you don't even set
`subscriberId` under `catalogPublisher.config` at all.**
`NewCatalogPublishHandler` derives it automatically from
`plugins.keyManager.config.subscriberId` (see `withKeyManagerSubscriberID`
in `catalogPublishHandler.go`) -- one declaration, in the one place that
actually needs to know it, instead of two config blocks that have to be
kept in sync by hand. An explicit `subscriberId` under
`catalogPublisher.config` is still honored untouched, for the rare case it
must legitimately differ from the KeyManager's.

**Relevance filtering**: `visibleTo` (via `publishDirectives`, see above)
maps onto `NetworkIds` -- a Discovery-service relevance filter, not access
control (file spec v2, "Catalog access is public"); there is no
restricted-catalog concept to wire up.

**Optional schema/policy validation, reusing existing plugins as-is, on
the real request body.** `plugins.schemaValidator` (`schemav2validator`)
and `plugins.checkPolicy` (`opapolicychecker`) can both be wired under the
`catalogPublish` module, exactly like any `std` handler already does.
Both are unconfigured by default -- validation is skipped entirely until
you add one. Neither plugin is modified or wrapped in a new interface to
support this: both already validate/police an arbitrary raw JSON body
keyed off a bare `context.action` field (`schemav2Validator.Validate`
looks up its pre-loaded OpenAPI schema by `context.action` alone;
`opapolicychecker.CheckPolicy` only ever reads `ctx.Body`, tolerantly).
Because the request body already matches beckn.yaml's real envelope shape
(see above), the handler passes the raw incoming bytes straight to both
plugins -- no synthesized wrapper. If a `schemaValidator` is configured,
it validates against `catalog/publish`'s real `message.catalogs[]:
Catalog[]` schema as defined in beckn.yaml
(`components.schemas.CatalogPublishAction`) -- so it validates for real.
`signValidator` (needed once signed inbound `GET` requests are added) is a
follow-up, not yet wired. Validation runs once for the whole submitted
batch (not per catalog); a failure rejects the entire request with `400`
before `Publish` is ever called, and skips entirely for a retire-only
request (no catalogs submitted, nothing to validate).

**Optional registry catalog-index link check, warn-only.**
`catalogPublisher.config.checkCatalogIndexLink: true` enables it -- no
separate plugin block; this reuses whatever `Registry` plugin is already
configured, type-asserted to `RegistryMetadataLookup` (no `ManifestLoader`
plugin instance is ever built for this check). When enabled, after every
successful publish the handler reads this node's own DeDi registry record
directly --
`RegistryMetadataLookup.LookupNode(ctx, syntheticNodeID)`, where
`syntheticNodeID` is a `subscriberId/subscribers.beckn.one/keyId` path built
from `keyManager.config.subscriberId` and the `keyId` resolved fresh on
every check from `keyManager.Keyset(ctx, subscriberId)` (re-resolved
per-check rather than cached, so a signing-key rotation is picked up
immediately, matching `catalogpublisher.Publish`'s own per-request
`Keyset` resolution) -- and checks whether the record's
`meta.catalog_index_url` already matches this publisher's own index URL
(`CatalogPublisher.IndexURL()`, part of the plugin's exported interface
precisely so callers like this can ask for it without knowing
`PublicBaseURL` internals). This directly declares the catalog index in
the subscriber's own DeDi record instead of the earlier three-level
indirection (DeDi record -> node manifest -> catalog index) -- there is no
node-manifest document involved in this check at all anymore, so no
manifest is fetched, signature-verified, or cached, and `ManifestLoader`
is never built for this handler. If the link is missing or doesn't match,
the handler returns a `warnings[]` entry naming the missing meta key and
the URL it should point at -- the publish itself still succeeds; this
never blocks it. There is nothing to stage locally: `dediregistry` has no
write path either way, so getting a value onto a DeDi record's meta is,
and remains, an external, manual operator action via your own DeDi
registration tooling. `checkCatalogIndexLink` defaults to `false`, which
skips this check entirely.

**Why a synthetic lookup path, not a hand-configured one.**
`RegistryMetadataLookup.LookupNode` (the method behind this check) takes a
DeDi-native `namespace/registry/recordName` path, a different shape than
the plain Beckn `subscriberId` `KeyManager.Keyset` is indexed by. Verified
directly against a real DeDi registry, though: a lookup addressed by plain
`subscriberId` + `keyId` via the registry's wildcard-registry value
(`subscribers.beckn.one` -- the same one `RegistryLookup.Lookup` already
uses to resolve signing keys during ordinary transactions) resolves to the
exact same underlying record a three-part path does -- the record's
`record_id` *is* its `keyId`. So `catalogPublishHandler.go` builds that
synthetic `subscriberId/subscribers.beckn.one/keyId` path itself (with
`keyId` derived from `keyManager.Keyset(ctx, subscriberId)`, the same
keyset `catalogpublisher.Publish` itself signs with) and passes it to
`LookupNode`, instead of asking the operator to separately discover and
configure an equivalent identifier. Note this duplicates the DeDi
wildcard-registry value as a local constant in the handler, rather than
giving `RegistryMetadataLookup` a proper subscriberId+keyId-shaped lookup
method -- a smaller-footprint stopgap; doing that properly is a separate,
better-scoped follow-up.

Both halves of the synthetic path are validated before use, since a
malformed one would otherwise silently and permanently break this
warn-only check rather than surfacing clearly: `subscriberId` is checked
for an embedded `"/"` once at handler construction (it can't change
afterward); `keyId` is checked for both emptiness and an embedded `"/"`
on every check, since it's re-resolved fresh each time.

## Migrating from the old catalog/publish API to the decentralized catalog

If you're publishing catalogs today via `catalog/publish` with ACK/NACK
responses, subscription CRUD (`catalog/subscription`), or a central
Cataloging Service, this section is for you. The model this plugin
implements is a different shape entirely: you publish plain files to your
own storage, and DeDi + a crawler do the rest. Nothing about your actual
catalog *content* (the `Catalog`, `Resource`, `Offer` schemas) changes --
what changes is how it gets from you to a Discovery Service.

### The conceptual shift

**Before:** you called a network API (`catalog/publish`) and got an
ACK/NACK back. A central Cataloging Service stored your catalog, handled
subscriptions, and served `catalog/pull`/`catalog/search` to consumers.

**Now:** you publish immutable JSON files to storage you already control
(any CDN, object store, or static host) via this plugin's `Publish` call,
exposed here as a DS-internal `catalog/publish` trigger with no ACK/NACK
envelope at all -- see "HTTP handler: `catalog/publish`" above. Once your
files are on your storage and your DeDi record's `meta.catalog_index_url`
points at your index, crawlers discover and pull your catalogs on their
own schedule. There is no central service to call, subscribe to, or wait
on.

### What you need to do

Short version: **pick some storage, call `catalog/publish` against your
own adapter instead of a central service, and set one field on a record
you already have.** That's the whole migration -- there's no server to
stand up, no subscription list to manage, and no ACK/NACK handshake to
get right.

1. **Pick storage you already have.** Any static host works -- S3, a CDN,
   GitHub Pages, even an ngrok tunnel for local testing. You're not
   building a new service; you're pointing this plugin at a folder.
2. **Call `catalog/publish` -- but against your own adapter, not a
   central Cataloging Service.** The request body (your catalog JSON) is
   unchanged, but the endpoint you hit is now this DS-internal,
   same-operator trigger on your own node instead of a network call to
   someone else's service, and there's no ACK/NACK to parse in response:
   a synchronous call returns the catalog files and index, ready to
   upload. No MERGE/FULL mode to pick either -- the plugin looks at what
   you last published and figures out on its own whether this is a fresh
   baseline or an incremental change; a resubmission of identical content
   is simply a no-op.
3. **Set one field on your existing DeDi Subscriber record:
   `meta.catalog_index_url`.** That's the entire "registration" step --
   no separate pointer file, no new registry to onboard into. The plugin
   can even check this for you after every publish and warn you if it's
   missing (see "Optional registry catalog-index link check" above).

Everything else -- subscriptions, restricted-catalog auth, a central
Cataloging Service, waiting on callbacks -- simply isn't part of this
model anymore, so there's nothing to configure for it, only things to
delete from your existing integration (see "What you no longer need,"
below).

### What you no longer need

- **A `catalog/publish` call to a shared, network-facing Cataloging
  Service, with an ACK/NACK response.** You still call `catalog/publish`
  -- but it's now a DS-internal, same-operator trigger on your own
  adapter, not a network call to someone else's service, and it responds
  synchronously with your catalog files and index instead of an ACK/NACK
  envelope.
- **`catalog/subscription` CRUD.** A crawler's scope is its own
  configuration now -- you don't manage subscriber lists.
- **`catalog/search`.** Removed from the publish/pull surface; a
  Discovery Service may still offer search over its own store, but
  that's not something you interact with as a publisher.
- **`catalog/push`/`/on_pull` callbacks.** Consolidated into the crawler
  pulling from you and pushing into the Discovery Service's own `/push`
  -- you never receive a callback for this.
- **Restricted catalogs, download gates, `authMethods`.** Catalogs are
  public, unconditionally, in this design. If you relied on
  `publishDirectives.visibleTo` as an access gate, note that its
  replacement (`networkIds` in the index) is a **relevance filter only**,
  never an access control -- anyone with a file's URL can fetch it.

### Field-by-field mapping

| Old (CATALG / DISCOVR) | New |
| :---- | :---- |
| `catalog/publish` with ACK/NACK | Files saved to storage; validation happens up front, results in a feedback log |
| `publishDirectives.visibleTo` | Per-catalog `networkIds` in the index -- relevance filter, not access gate |
| `publishDirectives.updateMode: MERGE` | A change file (id-keyed upserts/removals) |
| `publishDirectives.updateMode: FULL` | A fresh baseline |
| `catalog/pull`, mode FULL | The baseline file |
| `catalog/pull`, mode DELTA | Change files after the crawler's cursor |
| `downloadManifest` (sha256, sizeBytes) | `digest`/`size` in the index, verified against each self-signed file |
| Subscription filters (`networkIds`, `schemaTypes`) | Crawler-side filtering on the index |
| Subscription CRUD (`catalog/subscription`) | Not needed -- a crawler's scope is its own config |
| `catalog/search` | Removed from this surface |
| `catalog/push` | Crawler pull, with an optional change signal as an accelerator |
| `/on_pull` callback | Consolidated into the Discovery Service's internal `/push` |
| `subscriberId` | `nodeId`, a domain |
| Restricted catalogs / download gate / `authMethods` | **Removed.** Catalogs are public-only; no per-catalog auth exists |
| Offer-only catalogs, query-time attachment | Unchanged -- still lives behind `/discover` |

### What stays exactly the same

- Your `Catalog`/`Resource`/`Offer` JSON content and its schema.
- `catalogType: MASTER`/`REGULAR` and `resourceDirectives[].extends` --
  unchanged, just resolved by the Discovery Service at index time instead
  of centrally at publish time.
- Offer-only catalogs and query-time attachment behind `/discover`.

## Known open items

- Compaction scheduling (see above) -- grace-period cleanup itself is
  implemented (`localstore`).
- `ArtifactStore` wiring once a storage plugin exists, to replace the
  placeholder URLs with real published locations -- until then, the
  `catalogPublish` handler's `outputRoot` only ever holds local files;
  "moving them to wherever they're actually served from" is left to a
  separate, later deployment step.
- A real, signed, async `catalog/publish` Beckn action per beckn.yaml
  (context/action envelope, `validateSign`/`addRoute`/`sign` pipeline,
  `on_publish` callback with `CatalogProcessingResult`) is a materially
  different, larger scope than the internal trigger built here -- not
  attempted in this phase.
- `signValidator` (`signvalidator`) isn't wired in yet -- needed once
  signed inbound `GET` requests to this handler are added.
