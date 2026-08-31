# OAN Registry Plugin

A **registry type plugin** for Beckn-ONIX that reads the OAN Registry, a
[SunbirdRC](https://docs.sunbirdrc.dev/) deployment.

## Overview

It answers two questions, and they are deliberately kept apart.

**Who sent this?** `definition.RegistryLookup` — given the `subscriber_id` and
`key_id` carried in an inbound request's `Authorization` header, it returns that
**sender's** public key so the signature can be verified.

**Who do I call next?** `definition.ProviderRecordLookup` — given a capability
binding taken from the request body, it returns the **upstream provider's** call
plan: where to call, how, and which mappings translate in and out.

Different subject, different cache, different meaning of failure. They share
transport and nothing else. A caller reaches the second by type-asserting the
first, the same way `RegistryMetadataLookup` is reached elsewhere.

It is read-only. Onboarding, key publication and status changes all happen
through the registry's own Participant APIs, not through this plugin.

This call sits inside signature validation, so it runs on **every inbound
message**. Its timeout and retry budget are deliberately tighter than the
sibling registry plugins' for that reason: `timeout × (retry_max + 1)` is time a
request spends waiting before it can even be rejected.

## Configuration

```yaml
registry:
  id: oanregistry
  config:
    url: http://registry:8081/api/v1
    entity: Participant
    providerEntity: ProviderSchema
    timeout: 2
    retry_max: 1
    retry_wait_min: 100ms
    retry_wait_max: 500ms
    cacheTTL: 60s
```

| Parameter | Required | Description | Default |
|-----------|----------|-------------|---------|
| `url` | **Yes** | Registry base URL, including the API version prefix. The plugin appends `/{entity}/search`. | — |
| `entity` | No | Registry entity to search. | `Participant` |
| `timeout` | No | Per-attempt request timeout, in seconds. Must be positive. | `2` |
| `retry_max` | No | Retry attempts after the first. `0` means do not retry, and is honoured as such. | `1` |
| `retry_wait_min` | No | Minimum backoff between attempts. | `100ms` |
| `retry_wait_max` | No | Maximum backoff between attempts. Also the ceiling a `Retry-After` header is clamped to. | `500ms` |
| `cacheTTL` | No | How long a resolved participant is reused. **Absent or `0` disables caching entirely.** | off |

A `cache` plugin must also be configured for `cacheTTL` to have any effect.

Startup fails on: a missing `url`, a `url` with no scheme or host, a
non-positive `timeout`, a negative `retry_max` or wait, an unparseable
`cacheTTL`, or `retry_wait_min` exceeding `retry_wait_max`. Catching
`registry:8081` (no scheme) at startup is cheaper than watching every lookup
fail once traffic arrives.

### On `cacheTTL`

The TTL is the **suspension-propagation window**: a cached participant keeps
verifying until the entry expires, even after the Network Operator suspends it.
That is why caching is off by default rather than something a deployment
inherits.

The TTL is never taken from the key's own `validUntil`. That window is
typically a year, which would keep a suspended participant verifying for a year.

Misses and refusals are never cached. Caching a miss would extend an outage;
caching a refusal would delay a reinstatement.

## How a lookup works

```
Authorization: Signature keyId="<subscriber_id>|<key_id>|ed25519", ...
                              │              │
                              ▼              ▼
        POST {url}/{entity}/search    {"filters":{"participantId":{"eq":"<subscriber_id>"}}}
                              │
                              ▼
        walk node.keys[] for one whose osid == key_id, and whose use is signing
                              │
                              ▼
        participant status == "active"
          AND key status  == "active"
          AND key material present?                    →  SUBSCRIBED
        anything else                                  →  UNSUBSCRIBED
```

`key_id` is the **key's** `osid` (`node.keys[].osid`), not the participant's or
the node's. A record carries all three and they look alike; matching either of
the other two resolves the wrong thing, and keeps doing so the moment a second
key is published.

**Only `participantId` is filtered on.** It is the schema's
`uniqueIndexFields`, so the registry already guarantees at most one match.
`osid` is system-generated and not indexed at all — on an Elasticsearch-backed
deployment, filtering on it matches nothing, which would turn every lookup into
a not-found. It is also nested inside the record, which a flat filter could not
reach in any case. The key identity is therefore checked client-side, where it
works on any backend and enforces exactly the same property.

**`status` is not filtered on either.** Excluding suspended participants
server-side would return an empty result, making "suspended" indistinguishable
from "unknown" and losing the reason the caller reports.

## How a provider record resolves

```
message.offer.provider.id             ─┐
message.resourceAttributes["@type"]   ─┴─▶ bindingKey "<participantId>|<capabilityCode>"
                              │
                              ▼
        POST {url}/{providerEntity}/search   {"filters":{"bindingKey":{"eq":"..."}}}
                              │
                              ▼   the binding names its owner
        POST {url}/{entity}/search           {"filters":{"participantId":{"eq":"..."}}}
                              │
                              ▼
        both statuses "active" and an upstream url present?  →  a call plan
        anything else                                        →  ErrProviderRecordNotFound
```

Two reads, joined into one `model.ProviderRecord`: `baseUrl` from the
participant, a **call plan per action** from the binding.

```json
{
  "bindingKey": "mausamgram|openagrinet:WeatherObservation",
  "participantId": "mausamgram",
  "capabilityCode": "openagrinet:WeatherObservation",
  "status": "active",
  "actions": [
    { "action": "select", "method": "GET", "path": "/get-daily",
      "mappings": "https://.../mausamgram/weather-observation.select.yaml",
      "timeoutMs": 30000, "retryMax": 3, "status": "active" },
    { "action": "confirm", "method": "POST", "path": "/book",
      "mappings": "https://.../mausamgram/weather-observation.confirm.yaml",
      "status": "inactive" }
  ]
}
```

A capability serves several actions and they rarely share an endpoint, a method
or a mapping — a `confirm` that commits does not post where a `select` that reads
gets — so all of it is per action. An action absent from `actions` is one the
capability does not serve, and a binding serving none at all is refused outright
rather than failing one action at a time.

**An array rather than a keyed object, for two reasons.** A per-action `status`
is how one action is retired while the capability and every other action stay
live, and an entry that is not `active` is skipped exactly as if it were absent.
And the registry treats every nested object as an entity and injects an `osid`
into it, which a keyed map cannot carry.

`mappings` is one reference per action carrying **both directions**, because the
response mapping usually depends on what the request mapping did. It is the
published file's URL, passed through verbatim — this plugin does not interpret
or resolve it.

The owning participant is the one the **binding names**, not one parsed out of
the binding key — the registry owns that relationship, not the key format.

Mapping references are carried **verbatim**. They are URLs the mapper resolves;
this plugin does not read, fetch or interpret them.

`timeoutMs` and `retryMax` are zero when the registry omits them, meaning "the
caller applies its own default" — not "no timeout and no retries".

Every way of saying *this capability cannot be served* — absent, withdrawn,
suspended, unroutable — returns `ErrProviderRecordNotFound`, because a caller
does the same thing with all of them. A registry that could not be **consulted**
returns its own error instead: that is an outage, not an answer. The two are
separated in metrics, never in the returned type.

## What a caller gets back

| Situation | Result |
|---|---|
| Registered, active, has a key | One `Subscription`, `Status: SUBSCRIBED` |
| Registered but suspended, or has no key | One `Subscription`, `Status: UNSUBSCRIBED` |
| Not registered | Empty slice, `nil` error |
| Registry unreachable or unreadable | `nil`, error |

"Not found" is a legitimate answer, not an error — the caller turns an empty
slice into its own not-found. A participant that exists but may not sign comes
back with a status the caller rejects, so that **"unknown" and "suspended" stay
distinguishable** instead of collapsing into the same empty result.

### Deny by default

`model.IsKeyStatusUsable` is a deny-list: any status it does not recognise
counts as usable. Passing the registry's own `"inactive"` through unchanged
would therefore let a suspended participant's signature verify. Status mapping
here is a **security control, not a formatting step** — everything denies unless
explicitly allowed.

Status is checked at **both** levels. A participant stays active while one of its
keys is retired, so a key carrying its own non-active status is refused even
though the participant is trading normally.

The key validity window (`validFrom` / `validUntil`) is deliberately **not**
enforced. The Network Operator takes a participant off the network by setting
`status`, not by this plugin timing a key out. Both fields are mapped onto the
result for a caller to read, and nothing acts on them.

Key material is published with an encoding label, e.g.
`"key": "base64:xq4+..."`. The label is stripped before the value reaches
`model.Subscription`, which carries the bare base64 that `signvalidator` hands
straight to `base64.StdEncoding.DecodeString`. Left on, it fails every
verification with a decode error pointing nowhere near the registry.

## Observability

Every lookup emits its duration and, when it did not resolve a key, the shared
plugin error counter. The `error_type` dimension is one of:

Provider-record lookups report under `operation=provider_record`, with their own
outcomes: `binding_not_found` · `binding_inactive` · `binding_unowned` ·
`participant_not_found` · `participant_inactive` · `no_upstream_url` ·
`no_binding_key`. Each refusal is kept distinct: they all deny the call, but a
withdrawn capability and a suspended provider are different operational events.

Signing-key lookups report under `operation=lookup`:

`found` · `cache_hit` · `not_found` · `key_id_mismatch` · `key_not_signing` ·
`inactive` · `key_inactive` ·
`no_key` · `timeout` · `registry_error` · `decode_error` · `transport_error`

Split on `error_type` when alerting. "Not a success" includes outcomes that are
the plugin working correctly — refusing a suspended participant is a successful
denial, and a routine suspension should not read as an incident.

Two of these are worth watching separately. `not_found` means the caller is not
registered. `key_id_mismatch` means the participant **is** registered and the
key identity model is wrong — a sustained rate of that is a total outage that
would otherwise hide inside routine misses.

## Notes for operators

- **Service name, not `localhost`.** In a container the registry is reached by
  its service name; `localhost` resolves to the adapter itself.
- **`Retry-After` is clamped** to `retry_wait_max`. The retry library honours it
  unclamped, so a registry — or any ingress in front of one — answering
  `Retry-After: 3600` would otherwise park a goroutine for an hour inside
  signature validation, with no deadline on the inbound request to cut it short.
- **A record declaring an algorithm other than `ed25519` is logged as a
  warning**, not refused. The header's algorithm is validated upstream, so a
  disagreement cannot let a bad signature through — but it means the record and
  the caller disagree about the key, which is worth seeing before it becomes a
  verification failure nobody can explain.
- **More than one record for a `participantId`** is a registry integrity fault.
  It is logged at error level and the lookup carries on, since the key check
  still decides.
