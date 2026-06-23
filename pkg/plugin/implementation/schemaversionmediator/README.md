# SchemaVersionMediator Plugin

`schemaversionmediator` mediates schema version differences between Beckn participants. When a BAP and a BPP declare different schema object versions in their node manifests, this plugin fetches translation artifacts from the network's artifact registry, executes them, and patches the payload so that each side receives data in the schema version it expects.

---

## Table of Contents

1. [How It Works](#how-it-works)
2. [Plugin ID and Dependencies](#plugin-id-and-dependencies)
3. [Configuration Reference](#configuration-reference)
4. [Step Ordering](#step-ordering)
5. [Cold-Start Behaviour](#cold-start-behaviour)
6. [Error Codes](#error-codes)
7. [Translation Artifacts](#translation-artifacts)
8. [Data-Loss Detection](#data-loss-detection)
9. [Known Limitations](#known-limitations)

---

## How It Works

On every inbound request, `Mediate` runs the following sequence:

1. **Cold-start guard** — if the local node manifest was absent or had no `schemaObjects` at startup, every call is rejected immediately with `subscriberNotOnboarded`.
2. **Identity extraction** — reads `networkId`/`network_id` from the payload `context` block; reads the counterparty subscriber ID from `ContextKeyRemoteID` (set by `reqpreprocessor`).
3. **Counterparty manifest lookup** — calls `ManifestLoader.GetBySubscriberID` to fetch the counterparty's node manifest from DeDi.
4. **Compatibility check** — walks the payload for `@context`+`@type` pairs and compares them against the counterparty manifest's `schemaObjects`. If all match, the payload passes through unchanged.
5. **Artifact fetch** — for each incompatible schema object, fetches a translation artifact from the URL derived from the counterparty manifest's `contextUrl`.
6. **Translation** — composes the fetched artifacts into a single JSONata expression and executes it against the `message` subtree of the payload.
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
      action: translate         # see Configuration Reference
      onFailure: reject
```

**Required dependencies:**

| Dependency | Why |
|---|---|
| `manifestLoader` | Fetches counterparty node manifests from DeDi for compatibility checks |
| `dediregistry` (backing the manifestLoader) | Resolves subscriber manifest URLs |

`nodeId` is **not** an operator-facing config field. The handler injects the adapter's `subscriberId` automatically before calling `New` so the cold-start manifest lookup can run at boot time.

---

## Configuration Reference

| Key | Values | Default | Description |
|---|---|---|---|
| `action` | `translate` \| `reject` | `translate` | What to do when schema objects are incompatible |
| `onFailure` | `reject` \| `passThrough` | `reject` | What to do when `action=translate` but an artifact cannot be fetched |
| `fetchTimeoutMs` | integer | `5000` | HTTP timeout for artifact fetch in milliseconds |
| `positiveCacheTTL` | duration string | `1h` | How long to cache successfully fetched artifacts |
| `negativeCacheTTL` | duration string | `5m` | How long to cache artifact-not-found responses |
| `maxCacheEntries` | integer | `1000` | Maximum number of entries in the artifact cache |

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

## Cold-Start Behaviour

At startup, `New` calls `ManifestLoader.GetBySubscriberID` for the local node's subscriber ID. If the manifest is absent, unreachable, or contains no `schemaObjects`, the mediator sets an internal `notOnboarded` flag.

While `notOnboarded` is set, every `Mediate` call returns `subscriberNotOnboarded` immediately. The adapter must be restarted after the local node manifest is published to DeDi and becomes resolvable.

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
