# SchemaVersionMediator Plugin

`schemaversionmediator` mediates schema version differences between Beckn participants. When a BAP and a BPP declare different schema object versions in their node manifests, this plugin fetches translation artifacts from the network's artifact registry, executes them, and patches the payload so that each side receives data in the schema version it expects.

---

## Table of Contents

1. [How It Works](#how-it-works)
2. [Plugin ID and Dependencies](#plugin-id-and-dependencies)
3. [Configuration Reference](#configuration-reference)
4. [Step Ordering](#step-ordering)
5. [Direction Awareness](#direction-awareness)
6. [Cold-Start Behaviour](#cold-start-behaviour)
7. [Node Manifest Schema](#node-manifest-schema)
8. [Error Codes](#error-codes)
9. [Translation Artifacts](#translation-artifacts)
10. [Data-Loss Detection](#data-loss-detection)
11. [Known Limitations](#known-limitations)

---

## How It Works

On every inbound request, `Mediate` runs the following sequence:

1. **Cold-start guard** — if the local node manifest was absent, unreachable, or had no `schemaObjects` at startup (or if `nodeId` was not set in config), every call is rejected immediately with `subscriberNotOnboarded`.
2. **Identity extraction** — reads `networkId`/`network_id` from the payload `context` block; reads the counterparty subscriber ID from `ContextKeyRemoteID` (set by `reqpreprocessor`). If either is empty the payload passes through unchanged.
3. **Target manifest selection** — direction-aware:
   - **Receiver handler** (`bapTxnReceiver`, `bppTxnReceiver`): uses the local node manifest loaded at startup. No network call at request time.
   - **Caller handler** (`bapTxnCaller`, `bppTxnCaller`): calls `ManifestLoader.GetBySubscriberID` to fetch the counterparty's node manifest from DeDi at request time.
4. **Compatibility check** — walks the payload for `@context`+`@type` pairs and compares them against the target manifest's `schemaObjects`. If all objects are at a supported version, the payload passes through unchanged.
5. **Artifact fetch** — for each incompatible schema object, derives the artifact URL from the target manifest's `baseUrl`, canonical version, and the source version, then fetches it over HTTP.
6. **Translation** — composes the fetched artifacts into a single JSONata expression (`$merge([$, patch1, patch2, ...])`) and executes it against the `message` subtree of the payload in one pass.
7. **Data-loss detection** — compares flattened key paths of the source and translated message. If any source keys are absent in the output, the request is rejected with `schemaTranslationDataLoss`.
8. **Patch** — replaces the `message` field in `ctx.Body` with the translated output.

---

## Plugin ID and Dependencies

```yaml
plugins:
  manifestLoader:
    id: manifestloader          # required — backed by dediregistry
  schemaVersionMediator:
    id: schemaversionmediator
    config:
      nodeId: "nfh.global/subscribers.beckn.one/open-kitchen-bpp"  # required
      action: translate
      onFailure: reject
```

**Required dependencies:**

| Dependency | Why |
|---|---|
| `manifestLoader` | Fetches counterparty node manifests from DeDi at request time (caller path) and the local manifest at startup |
| `dediregistry` (backing the manifestLoader) | Resolves subscriber manifest URLs via DeDi |

**`nodeId`** is an **operator-facing config field** set under `schemaVersionMediator.config`. It is the three-part DeDi subscriber identity for this node (`namespace/registry/recordId`, e.g. `nfh.global/subscribers.beckn.one/open-kitchen-bpp`). At startup the plugin calls `ManifestLoader.GetBySubscriberID(nodeId)` to load the local node manifest. If `nodeId` is absent or the manifest cannot be loaded, the mediator marks itself as `notOnboarded` and rejects every request.

---

## Configuration Reference

| Key | Values | Default | Description |
|---|---|---|---|
| `nodeId` | string | — | **Required.** Three-part DeDi subscriber identity for this node (`namespace/registry/recordId`). Used at startup to load the local node manifest. |
| `action` | `translate` \| `reject` | `translate` | What to do when schema objects are incompatible |
| `onFailure` | `reject` \| `passThrough` | `reject` | What to do when `action=translate` but an artifact cannot be fetched |
| `fetchTimeout` | duration string | `"30s"` | HTTP timeout for each artifact fetch (e.g. `"10s"`, `"1m"`) |
| `artifactCacheTTL` | duration string | `"24h"` | How long to cache successfully fetched artifacts |
| `negativeCacheTTL` | duration string | `"5m"` | How long to cache artifact-not-found responses |
| `maxCacheEntries` | integer string | `"500"` | Maximum number of entries in the artifact cache |

**`action` values:**

- `translate` — attempt translation for each incompatible schema object; apply `onFailure` if any artifact is unavailable.
- `reject` — return `schemaIncompatible` immediately without attempting translation.

**`onFailure` values (only evaluated when `action=translate`):**

- `reject` — return `schemaIncompatible` when an artifact cannot be fetched.
- `passThrough` — forward the untranslated payload. Operator escape hatch for false-mismatch situations (e.g. stale local manifest during rollout). Not recommended for production.

---

## Step Ordering

`validateSchema → mediateSchema` is the correct order for **both caller and receiver** handler configs.

```yaml
steps:
  - validateSign
  - validateSchema      # validate source payload in its declared schema version
  - mediateSchema       # translate domain schema objects if versions differ
  - addRoute
```

**Why this order works universally:**

Schema definitions are publicly available and versioned. The schema validator resolves `@context` URLs directly, so it can validate any version payload regardless of what the local node runs. Translating before validation would leave `@context` URLs pointing at the source version while the field structure reflects the target — an inconsistency that extended schema validation would misread.

> **Artifact authoring note:** Translation expressions should update `@context` URLs as part of the translation to keep the translated payload internally consistent. This is a requirement for translation artifact authors, not enforced by the plugin.

---

## Direction Awareness

`Mediate` behaves differently depending on whether the handler is a caller or a receiver. The distinction is carried by `StepContext.IsCallerHandler`.

| Handler type | Examples | Target manifest | Manifest source |
|---|---|---|---|
| **Receiver** | `bapTxnReceiver`, `bppTxnReceiver` | Local node manifest | Loaded once at startup via `nodeId` |
| **Caller** | `bapTxnCaller`, `bppTxnCaller` | Counterparty node manifest | Fetched per-request via `ManifestLoader.GetBySubscriberID` |

**Receiver path:** the plugin checks whether the inbound payload's schema versions are compatible with what *this* node expects. Translation makes the inbound payload match the local node's declared versions.

**Caller path:** the plugin checks whether the outbound payload's schema versions are compatible with what the *counterparty* expects. Translation makes the outbound payload match the counterparty's declared versions before forwarding.

Both paths use the same compatibility check, artifact fetch, and translation logic — only the manifest source differs.

---

## Cold-Start Behaviour

At startup, `New` calls `ManifestLoader.GetBySubscriberID` using the `nodeId` config value to load the local node manifest. The mediator sets an internal `notOnboarded` flag if any of the following occur:

- `nodeId` is absent or empty in config
- The manifest document cannot be fetched or parsed
- The manifest contains no `schemaObjects`

While `notOnboarded` is set, every `Mediate` call returns `subscriberNotOnboarded` immediately. The adapter must be restarted after the local node manifest is published to DeDi and `nodeId` is correctly set in config.

---

## Node Manifest Schema

The node manifest is a YAML file that declares the schema types a participant supports and their accepted versions. It is the primary input to compatibility checking.

### Full structure

```yaml
manifestVersion: "1.0"
manifestType: "node-manifest"
subscriberId: "nfh.global/subscribers.beckn.one/open-kitchen-bpp"

schema:
  defaultVersionPolicy: "latest"   # optional — applied to objects that don't set their own
  schemaObjects:
    - type: "RetailConsideration"
      baseUrl: "https://raw.githubusercontent.com/beckn/local-retail/refs/heads/main/schema/RetailConsideration"
      supportedVersions:
        - "v2.1"
        - "v2.2"
      versionPolicy: "pinned"       # optional — overrides defaultVersionPolicy for this object
      pinnedVersion: "v2.2"         # required when versionPolicy=pinned

    - type: "RetailOffer"
      baseUrl: "https://raw.githubusercontent.com/beckn/local-retail/refs/heads/main/schema/RetailOffer"
      supportedVersions:
        - "v2.1"

governance:
  effectiveFrom: "2024-01-01T00:00:00Z"
  effectiveUntil: "2027-01-01T00:00:00Z"  # optional — omit for indefinite validity
```

### `schema` section

| Field | Required | Description |
|---|---|---|
| `defaultVersionPolicy` | No | Applied to all `schemaObjects` that do not set their own `versionPolicy`. Values: `latest` (default) or `pinned`. |
| `schemaObjects` | Yes | List of schema type declarations. At least one entry required for the mediator to consider the node onboarded. |

### `schemaObjects` entries

| Field | Required | Description |
|---|---|---|
| `type` | Yes | Schema type name as it appears in `@type` in the Beckn payload (e.g. `RetailConsideration`). Must match exactly — case-sensitive. |
| `baseUrl` | Yes | Base URL prefix for this schema type. The mediator appends `/{version}/context.jsonld` to form the context URL, and `/{canonicalVersion}/{Type}_from_{fromVersion}` to derive artifact URLs. |
| `supportedVersions` | Yes | List of schema versions this node handles natively. Payloads at any listed version are considered compatible — no translation triggered. |
| `versionPolicy` | No | Controls which version is the canonical translation target. `latest` selects the highest version in `supportedVersions` by major.minor comparison. `pinned` uses `pinnedVersion`. Defaults to `defaultVersionPolicy`. |
| `pinnedVersion` | No | Required when `versionPolicy: pinned`. Must be one of the versions in `supportedVersions`. |

### `governance` section

| Field | Required | Description |
|---|---|---|
| `effectiveFrom` | Yes | RFC 3339 timestamp from which the manifest is valid. Manifests with a future `effectiveFrom` are rejected at load time. |
| `effectiveUntil` | No | RFC 3339 timestamp at which the manifest expires. Omit for indefinite validity. Expired manifests are rejected at load time. |

### Artifact URL derivation

When a payload carries a schema object at a version not in `supportedVersions`, the mediator constructs the artifact URL as:

```
{baseUrl}/{canonicalVersion}/{type}_from_{fromVersion}
```

For example, if `RetailConsideration` has `baseUrl: https://.../schema/RetailConsideration`, `supportedVersions: [v2.2]`, and the inbound payload declares `v2.1`:

```
https://.../schema/RetailConsideration/v2.2/RetailConsideration_from_v2.1
```

The artifact at that URL must contain a JSONata expression that transforms a `v2.1` message structure into a `v2.2`-compatible one.

---

## Error Codes

All errors returned by `Mediate` are of type `*MediationError`, which carries a camelCase `Code` field. The handler uses this to build a structured Beckn NACK response.

| Code | Cause | Resolution |
|---|---|---|
| `subscriberNotOnboarded` | Local node manifest absent or has no `schemaObjects` at startup | Publish node manifest to DeDi and restart the adapter |
| `schemaIncompatible` | Incompatible schema objects found and `action=reject`, or artifact fetch failed and `onFailure=reject` | Check counterparty manifest in DeDi; verify artifact URLs are reachable |
| `schemaTranslationDataLoss` | Translation dropped fields present in the source payload | Review translation artifact — it must not remove fields from the source |

Plain (non-`MediationError`) errors may also be returned for malformed payloads (e.g. missing `message` field). The handler treats these as HTTP 400 Bad Request with a generic error body — distinct from the structured NACK produced by `MediationError`.

---

## Translation Artifacts

Translation artifacts are external files fetched at runtime from URLs derived from the counterparty's node manifest `contextUrl` field. The artifact URL is constructed by replacing the schema version segment in the `contextUrl` path.

Currently supported content type: `application/jsonata`.

Artifacts are cached in memory with configurable positive and negative TTLs. The cache is per-mediator-instance and is not shared across handlers.

---

## Data-Loss Detection

After translation, the plugin compares the flattened dot-notation key paths of the source `message` subtree against the translated output. Any key present in the source but absent in the output is considered a dropped field.

**Current behaviour:** data loss always causes rejection with `schemaTranslationDataLoss`, listing the dropped field paths. There is no configurable policy — this is intentional. A partially translated payload is as harmful as an incompatible one.

**Array handling:** array elements are treated as opaque leaf values. Element-level drops within an array are not detected — only object key presence is compared.

---

## Known Limitations

- Non-JSONata translator types are not yet supported. The translation dispatch layer is in place; additional content types will be wired in future releases.
- Observed seeding (auto-updating the local node manifest from live traffic) is not implemented. Tracked in [#822](https://github.com/beckn/beckn-onix/issues/822).
- `RunOnResponse` is not implemented — Beckn responses arrive as separate inbound requests and are mediated by `Mediate` on the receiver handler, not via a response hook.
