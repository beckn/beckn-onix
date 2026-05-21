# OPA Policy Checker Plugin

This document describes the OPA Policy Checker plugin: what it enforces, how to configure it for a specific network, how to write and publish Rego policies, and how to diagnose common misconfigurations.

---

## Table of Contents

1. [Two Audiences, One Policy Layer](#two-audiences-one-policy-layer)
2. [Architecture](#architecture)
3. [Plugin Configuration Reference](#plugin-configuration-reference)
4. [Network Policy Config File](#network-policy-config-file)
5. [Writing Your First Policy](#writing-your-first-policy)
6. [Common Policy Patterns](#common-policy-patterns)
7. [Structuring Policies for Hierarchical Evaluation](#structuring-policies-for-hierarchical-evaluation)
8. [Authoring Best Practices](#authoring-best-practices)
9. [Publishing Policies as a Network Facilitator](#publishing-policies-as-a-network-facilitator)
10. [Manifest-Backed Policies](#manifest-backed-policies)
11. [Signature Verification](#signature-verification)
12. [Policy Hot-Reload](#policy-hot-reload)
13. [Troubleshooting](#troubleshooting)
14. [Relationship with Schema Validator](#relationship-with-schema-validator)
15. [Dependencies](#dependencies)
16. [Known Limitations](#known-limitations)

---

## Two Audiences, One Policy Layer

The OPA Policy Checker plugin sits at the junction of two distinct teams.

**Operators** (BAP and BPP teams running adapter instances) configure which policy source to load for each network they participate in. They do not write the policy rules themselves — they point the plugin at the policy files or bundles their networks have published. Their concern is correct wiring: the right policy for the right network, with the right reload cadence.

**Network Facilitating Organizations (NFOs)** author the network policies themselves. They define the business rules that govern what a valid Beckn message looks like for their domain — required fields, allowed values, configurable thresholds. They publish policies as `.rego` files or signed OPA bundles, and publish a manifest that lets the adapter discover and verify those policies automatically.

Both teams share a single configuration contract: the **network policy config file**, which maps network IDs to policy sources. Operators fill in the config; NFOs provide the policy content. This document covers both sides.

---

## Architecture

```
                         Incoming Beckn Message
                                  │
                                  ▼
         ┌────────────────────────────────────────────────┐
         │                OPA Policy Checker               │
         │                                                  │
         │  ① Extract context.networkId from message body  │
         │           │                                      │
         │  ② Select policy evaluator                      │
         │     ┌──────────────────────────────────────┐    │
         │     │  exact match on networkId?            │    │
         │     │    → use that policy (even if         │    │
         │     │      enabled: false, skips default)   │    │
         │     │  no exact match, default configured?  │    │
         │     │    → use default policy               │    │
         │     │  no match and no default?             │    │
         │     │    → skip evaluation (allow)          │    │
         │     └──────────────────────────────────────┘    │
         │           │                                      │
         │  ③ Check action filter (if actions: set)        │
         │     not in list? → skip evaluation (allow)      │
         │           │                                      │
         │  ④ Evaluate OPA query                           │
         │     input        = full Beckn message body       │
         │     data.config  = adapter plugin config keys    │
         │           │                                      │
         │  ⑤ Interpret result                             │
         │     {"valid": true,  "violations": []}  → allow │
         │     {"valid": false, "violations": […]} → 400   │
         │     empty / undefined result            → 400   │
         │     (fail-closed: bad config = denied)           │
         └────────────────────────────────────────────────┘
```

### Policy resolution at startup

At startup the plugin reads the network policy config file and resolves every configured entry:

| Source type | Resolution steps |
|---|---|
| `file` | Fetch `.rego` file from path or URL; optionally verify detached signature |
| `bundle` | Fetch `.tar.gz` bundle from path or URL; optionally verify embedded signature |
| `dir` | Load all `.rego` files in a local directory (no signature support) |
| `manifest` | Ask `manifestloader` for the verified network manifest; resolve to `file` or `bundle` |

One compiled OPA evaluator is stored per network ID. At request time, evaluator selection is O(1) — there is no per-request file I/O.

### Fail-closed guarantee

If the OPA query returns an undefined or empty result, the plugin treats it as a violation. This means a misconfigured `query:` path or an incomplete policy file causes requests to be rejected rather than silently passed. This is the correct default for a security-critical enforcement layer.

---

## Plugin Configuration Reference

`opapolicychecker` is a module-level plugin compiled as a `.so` and loaded by the plugin manager. It runs as one step in a module's `steps` list alongside the schema validator, signer, and router. The plugin ID (`opapolicychecker`, used in `id:`) and the step name (`checkPolicy`, used in `steps:` and as the YAML config key) are distinct — both must appear in the module config.

> **Migrating from an older config format.** Previous versions accepted top-level keys such as `type`, `location`, `query`, `actions`, and `refreshIntervalSeconds` directly in the plugin config. These keys are no longer used as plugin parameters — unrecognised keys are forwarded to Rego as `data.config.<key>` rather than silently dropped, so leftover keys from the old format will appear as Rego data values instead of configuring the plugin.

```yaml
manifestLoader:
  id: manifestloader
  config:
    cacheTTL: 24h
    fetchTimeoutSeconds: "30"

checkPolicy:
  id: opapolicychecker
  config:
    networkPolicyConfig: ./config/opa-network-policies.yaml
    refreshInterval: "5m"

steps:
  - checkPolicy
  - addRoute
```

> `manifestLoader` is required only when any entry in the network policy config uses `type: manifest`. It must be configured in the same handler/module as `checkPolicy`.

### Config parameters

| Parameter | Type | Required | Default | Description |
|---|---|---|---|---|
| `networkPolicyConfig` | string | Yes | — | Path to the YAML file containing `networkPolicies` keyed by network ID |
| `enabled` | string | No | `"true"` | Set to `"false"` to disable the plugin entirely (emits a startup warning) |
| `debugLogging` | string | No | `"false"` | Enable verbose OPA evaluation logging |
| `refreshInterval` | string | No | — | Go duration (`30s`, `5m`, `24h`) for periodic policy hot-reload |
| *any other key* | string | No | — | Forwarded to all policies as `data.config.<key>` at evaluation time |

### Runtime config forwarding

Every config key that is not a recognized plugin parameter is forwarded to OPA as `data.config.<key>`. This lets NFOs write policies that reference operator-supplied thresholds or domain values without hardcoding them in the policy file:

```yaml
checkPolicy:
  id: opapolicychecker
  config:
    networkPolicyConfig: ./config/opa-network-policies.yaml
    minDeliveryLeadHours: "2"    # accessible in Rego as data.config.minDeliveryLeadHours
    maxItemsPerOrder: "50"
```

---

## Network Policy Config File

The file at `networkPolicyConfig` maps each network ID to a policy source. All entries are loaded at startup; at request time the plugin matches `context.networkId` (with `context.network_id` as fallback) to select the right evaluator.

```yaml
networkPolicies:
  nfh.global/testnet:
    type: manifest              # resolved through manifestloader

  nfo.example.org/mobility-network:
    type: file
    location: https://nfo.example.org/policies/mobility.rego
    query: "data.mobility.policy.result"

  nfo.example.org/logistics-network:
    type: bundle
    location: https://nfo.example.org/policies/logistics.tar.gz
    query: "data.logistics.policy.result"

  default:
    type: file
    location: ./policies/default.rego
    query: "data.default.policy.result"
```

### Per-entry fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `type` | string | Yes | — | `file`, `bundle`, `dir`, or `manifest` |
| `location` | string | Yes (except `manifest`) | — | Local path or remote URL for the policy source |
| `query` | string | Yes (except `manifest`) | — | OPA query path that returns the policy result |
| `actions` | string | No | — | Comma-separated list of actions; if set, skip evaluation for actions not in this list |
| `enabled` | bool | No | `true` | Set to `false` to skip this network while keeping it in config |
| `fetchTimeoutSeconds` | string | No | `"30"` | Timeout for fetching remote policy sources |
| `verification.enabled` | bool | No | `false` | Enable signature verification for `file` or `bundle` |
| `verification.publicKeyLookupUrl` | string | Yes (if verifying) | — | DeDi public-key record endpoint |
| `verification.signatureLocation` | string | Yes (if verifying `file`) | — | Path or URL to the detached `.sig` file |
| `verification.algorithm` | string | No (`bundle` only) | `ES256` | Signing algorithm for bundle verification |

### Network selection semantics

- Exact match on `context.networkId` wins, even when `enabled: false` on that entry.
- An exact match with `enabled: false` does **not** fall through to `default` — it intentionally skips evaluation for that network.
- If no exact match is found, `default` is used when configured.
- If neither a network-specific entry nor `default` matches, OPA evaluation is skipped and the request is allowed.
- Policy enforcement is opt-in by network ID. Unmatched networks are skipped unless `default` is defined.

### Supported query output formats

| Rego output | Behavior |
|---|---|
| `{"valid": true, "violations": []}` | Allowed — recommended structured format |
| `{"valid": false, "violations": ["msg1", "msg2"]}` | Rejected — violation messages returned to caller |
| `set()` / `[]string` | Each string is a violation message |
| `bool` `true` | Allowed |
| `bool` `false` | Rejected |
| `string` (non-empty) | Rejected — the string is the violation message |
| Empty / undefined | **Rejected** — fail-closed; indicates a misconfigured query path |

---

## Writing Your First Policy

This section walks through creating a policy from scratch and wiring it into the adapter.

### Step 1 — Create a Rego file

Save the following as `policies/my-network.rego`. The package name and query path are up to you — they just need to match the `query:` you configure in the next step.

```rego
package mypolicy

import rego.v1

# Default: pass everything unless a rule fires.
default result := {
  "valid": true,
  "violations": []
}

# Accumulate violations, then produce the final result.
result := {
  "valid": count(violations) == 0,
  "violations": violations
}

# Require provider details on confirm.
violations contains "confirm: missing provider in order" if {
    input.context.action == "confirm"
    not input.message.order.provider
}

# Require search intent.
violations contains "search: missing intent" if {
    input.context.action == "search"
    not input.message.intent
}
```

### Step 2 — Add the policy to the network policy config

```yaml
# config/opa-network-policies.yaml
networkPolicies:
  my.network/production:
    type: file
    location: ./policies/my-network.rego
    query: "data.mypolicy.result"
```

The `query` value must match the package path exactly: `data.<package>.<rule>`.

### Step 3 — Wire the plugin into the adapter config

```yaml
checkPolicy:
  id: opapolicychecker
  config:
    networkPolicyConfig: ./config/opa-network-policies.yaml

steps:
  - validateSign
  - validateSchema
  - checkPolicy      # runs after schema validation
  - addRoute
```

### Step 4 — Verify locally

Send a `confirm` request with `message.order.provider` absent. The adapter should return `400` with `"confirm: missing provider in order"`.

Send a valid `confirm` request with all required fields. It should pass through.

See [`testdata/example.rego`](./testdata/example.rego) for a complete working policy covering `confirm`, `search`, and more.

### What's available inside a policy

| Source | What's there |
|---|---|
| `input` | The full JSON body of the Beckn request as the adapter received it. So `input.context.action`, `input.context.network_id`, `input.message.order.…`, `input.message.intent.…`, etc. |
| `data.config` | Every config key on the plugin block that is not a recognised parameter (`networkPolicyConfig`, `enabled`, `debugLogging`, `refreshInterval`). Use this for tunable thresholds without rebuilding the bundle. |

A policy never sees HTTP headers, signatures, or routing metadata — only the body. If a rule needs to discriminate by transport-level state, it must read it from the body (`input.context.*`).

---

## Common Policy Patterns

These patterns are ready to drop into any Rego policy file.

### Require a field on a specific action

```rego
violations contains "confirm: missing billing info" if {
    input.context.action == "confirm"
    not input.message.order.billing
}
```

### Use a configurable threshold from adapter config

The operator sets `minDeliveryLeadHours: "2"` in the plugin config; the policy reads it at evaluation time.

```rego
violations contains "delivery lead time too short" if {
    input.context.action == "confirm"
    lead := input.message.order.fulfillments[_].start.time.duration
    to_number(lead) < to_number(data.config.minDeliveryLeadHours)
}
```

### Restrict allowed domain values

```rego
allowed_domains := {"ONDC:RET10", "ONDC:RET11", "ONDC:RET12"}

violations contains "domain not allowed" if {
    not allowed_domains[input.context.domain]
}
```

### Limit evaluation to specific actions (config-level)

If your policy only makes sense for `confirm` and `init`, set `actions` in the network policy config rather than adding action guards to every rule:

```yaml
my.network/production:
  type: file
  location: ./policies/confirm-init.rego
  query: "data.policy.result"
  actions: confirm,init
```

### Skip a specific network without removing it from config

```yaml
legacy.network/v1:
  type: file
  location: ./policies/legacy.rego
  query: "data.legacy.result"
  enabled: false   # evaluation skipped; other networks unaffected
```

### Global catch-all (single policy for all networks)

If a single policy should apply to every incoming message regardless of network ID:

```yaml
networkPolicies:
  default:
    type: file
    location: ./policies/global.rego
    query: "data.global.result"
```

---

## Structuring Policies for Hierarchical Evaluation

When a top-level discriminator (action name, message shape, network ID) does not apply, the rules underneath it should not run. OPA does this for you, **but only if you write the discriminator the engine can see**. The patterns below show how to make that work in practice — they matter once your policy grows past a handful of rules.

### How short-circuiting actually works in Rego

OPA evaluates each rule body left-to-right and short-circuits as soon as any expression is undefined or false. So in:

```rego
violations contains "confirm: bad quantity" if {
    input.context.action == "confirm"          # cheap discriminator first
    some item in input.message.order.items     # iteration is skipped on non-confirm
    item.quantity.count <= 0
}
```

…for `init`, `search`, `status`, etc., OPA never iterates over `input.message.order.items`. The first expression fails, the rule body is dropped, the iterator never runs. This applies *per rule body*. It does **not** apply across independent `contains` rules: every `violations contains … if { … }` rule body is evaluated independently. So you need the discriminator inside each body — or push the discrimination one level up (see Pattern 1 below).

Two important consequences:

1. **Cheapest discriminator first.** Action checks (`input.context.action == "x"`), shape checks (`input.message.order`), and config flags belong at the top of the rule body. Iteration, time parsing, and `sprintf` belong at the bottom.
2. **Missing-path access counts as a false guard.** Writing `item := input.message.order["beckn:orderItems"][i]` as the *first* expression of a rule body is fine: if `input.message.order` is missing, the assignment is undefined and the rule body short-circuits before any other work runs. This is the cheapest possible structural gate.

### Pattern 1: Helper buckets gated at the top

Group rules by what they apply to (a Beckn action, a message shape, a sub-domain) into a *helper* set named `_<scope>_violations`. Then expose the public `violations` set behind a single gate per scope. This is the structure used by the production-style DEG policy referenced below.

```rego
# Helper buckets — each rule body short-circuits on missing path,
# and the public `violations` rule only reifies them when its gate passes.

_order_violations contains msg if {
    item := input.message.order["beckn:orderItems"][i]
    item["beckn:quantity"].unitQuantity < 0
    msg := sprintf("order item [%d]: negative quantity", [i])
}

_publish_violations contains msg if {
    item := input.message.catalogs[_]["beckn:items"][i]
    not item["beckn:provider"]
    msg := sprintf("catalog item [%d]: missing provider", [i])
}

# Public surface — top-level gates decide which bucket gets reified.

violations contains msg if {
    input.message.order
    input.context.action != "status"
    some msg in _order_violations
}

violations contains msg if {
    input.context.action == "catalog_publish"
    some msg in _publish_violations
}
```

The discriminator (`input.message.order`, `input.context.action == "catalog_publish"`) is checked once at the top. When it fails, OPA never enumerates the bucket and the underlying rule bodies don't materialize values. When it passes, OPA computes the bucket once and the values flow into `violations`. This is the most common, most maintainable structure for large policies.

### Pattern 2: Action dispatch with `else` chains

If you want strict one-of dispatch — exactly one branch runs for any given action — use an `else` chain. This gives the engine an explicit ordering rather than relying on guard-mutual-exclusion:

```rego
result := data.retail.confirm.result if input.context.action == "confirm"

else := data.retail.search.result if input.context.action == "search"

else := data.retail.init.result if input.context.action == "init"

else := {"valid": true, "violations": []}
```

Unreached branches are not evaluated. Combine this with per-action sub-packages (`package retail.confirm`, `package retail.search`) so each branch only pulls in the rules relevant to its action.

### Pattern 3: Sub-packages by domain

For larger networks, partition Rego by concern, not by rule number. Each sub-package owns one bucket of rules and exports a single value. A thin top-level router imports them:

```
policies/
  retail.rego              # top-level router; exports the public `result`
  retail/
    common.rego            # rules that always apply (domain, version)
    order.rego             # rules that need message.order
    catalog.rego           # rules that need message.catalogs
    helpers.rego           # @type / @context helpers, accessors
```

`retail.rego` then looks like:

```rego
package retail

import rego.v1

result := {
    "valid": count(all_violations) == 0,
    "violations": all_violations,
}

all_violations contains msg if {
    some msg in data.retail.common.violations
}

all_violations contains msg if {
    input.message.order
    some msg in data.retail.order.violations
}

all_violations contains msg if {
    input.context.action == "catalog_publish"
    some msg in data.retail.catalog.violations
}
```

`query: data.retail.result`

Sub-packages are also OPA's unit of test scoping — a `retail/order_test.rego` only re-runs when `retail/order.rego` or its dependencies change.

### Pattern 4: Named helper rules and functions

Within a bucket, share work using *named* helper rules and functions instead of repeating accessors and computations:

```rego
# Accessor that hides field-name variations across versions.
_delivery_window(offer_attrs) := object.get(
    offer_attrs,
    "deliveryWindow",
    object.get(offer_attrs, "beckn:timeWindow", null),
)

# Reusable predicate function — takes a location label, an object, and
# the expected @type, and returns a violation string (or is undefined).
_wrong_type(path, obj, expected) := sprintf(
    "%s: @type is %q; must be %q",
    [path, obj["@type"], expected],
) if {
    obj["@type"]
    obj["@type"] != expected
}
```

A function only computes once per distinct argument set within a query, so chained calls are cheap. They also keep rule bodies readable — a body that ends in `msg := _wrong_type("order.buyer", obj, "Buyer")` reads as one assertion.

### Anti-patterns to avoid

| Anti-pattern | Why it hurts | Better |
|---|---|---|
| Repeating `input.context.action == "confirm"` inside every rule | Hard to maintain; easy to forget on new rules. | Put the action gate at the public `violations` rule and use a helper bucket (Pattern 1). |
| Deep iteration as the first expression with no prior guard on the parent path | Still works — but harder to read and easy to break by accidentally referencing a sibling path first. | Lead the rule body with the cheapest guard (`input.message.order`, `input.context.action`). |
| One giant Rego file with hundreds of rules | Reviewers can't scope changes; tests rerun everything. | Split per sub-domain (Pattern 3). |
| Different `package` names per file with no router | Caller has to know N query paths instead of one. | Keep one public `result` rule; routers do the fan-in. |
| Using `not <some iteration>` as a guard | Negation-over-iteration semantics are easy to get wrong and can hide bugs. | Pull the predicate into a named rule that returns a bool, then negate the rule. |

### Reference example

The Beckn DEG repository ships a production-style policy at [`DEG/specification/policies/p2p-trading-interdiscom.rego`](https://github.com/beckn/DEG/tree/main/specification/policies). It demonstrates helper buckets (`_common_violations`, `_order_violations`, `_publish_violations`, `_test_consistency_violations`), top-level gates per action and per message shape, shared helper functions for `@type`/`@context` dual enforcement, a configurable `min_lead_hours` rule overridable via `data.config`, and a parallel `*_test.rego` test suite.

---

## Authoring Best Practices

The patterns above are about how the engine sees your policy. The practices below are about how *humans* see it.

### Readability

- One concept per rule. If a rule body has more than ~6 expressions, extract sub-predicates as named helper rules.
- Use `sprintf` for every violation message and include the input location (`order item [%d]: …`). When a NACK reaches a participant, the offending location is the single most useful field.
- Mirror Rego packages to directory paths (`package retail.order` lives at `retail/order.rego`). This is the OPA-bundle convention and survives `opa build`.
- Comment the *intent* of each rule above the rule body, not what the body does. Future readers can see the body — they need to know *why*.

### Efficiency

- Place the cheapest discriminator first in every rule body (see Pattern 1).
- Prefer deep-path accessors as the first expression rather than `not <path>` — the former short-circuits on missing parents; the latter doesn't.
- Cache repeated values in helper rules. `_buyer_meter_id := input.message.order["beckn:buyer"]["beckn:buyerAttributes"].meterId` is computed once per query, not once per rule.
- Avoid unbounded iteration when a single key lookup is enough. `some item in input.message.order.items; item.id == x` is O(n); if `id` is unique, model the catalog as a map indexed by id.
- Don't call `time.parse_rfc3339_ns` per rule. Parse once into a helper rule (`trade_time := time.parse_rfc3339_ns(input.context.timestamp)`) and reference the helper.
- Use sets, not arrays, for membership: `_allowed_utility_ids := {"TPDDL", "PVVNL", "BRPL"}`. `id in _allowed_utility_ids` is O(1).

### Modularity

- One sub-package per Beckn action or per structural domain. Tests then scope cleanly.
- Helpers go in `<domain>/helpers.rego` and re-export via package — never duplicate accessor functions across files.
- The top-level `result` rule is the *only* public surface. Internal buckets stay underscore-prefixed (`_order_violations`) so consumers never depend on them.
- Externalize tunables into `data.config` — never hard-code thresholds. Use `default min_lead_hours := 4` and override from adapter YAML.

### Maintainability

- Ship `*_test.rego` next to every policy file. Run `opa test . -v` in CI for the bundle.
- Version policies explicitly. Use `release_id` in the network manifest and bump it on every change so participants can see which policy version they are enforcing.
- Sign every bundle. Unsigned policies are not safe to fetch from any URL the adapter does not control — see [Signature Verification](#signature-verification).
- Document the input contract at the top of the policy file. List the Beckn fields the policy relies on; that doc is what changes when the underlying message schema changes.
- Keep policy artifacts at immutable URLs whenever possible. Mutable URLs work (the plugin will pick up changes on hot reload), but they are harder to audit.
- Test the full bundle, not just one file. Module interactions (shared helpers, shadowed rules) only surface when everything is loaded together.

---

## Publishing Policies as a Network Facilitator

A Network Facilitator Organization (NFO) publishes policies that every participant on a given `network_id` then enforces locally via this plugin. The end-to-end flow is owned by NFH Fabric documentation; this section covers the build / sign / publish steps and points at the authoritative source for the rest.

**Authoritative reference:** [Configuring Network Policies — docs.nfh.global](https://docs.nfh.global/beckn/creating-an-open-network/configuring-network-policies)

### High-level flow

1. Author the Rego policy.
2. Test it with `opa test`.
3. Build it as an OPA bundle (or keep it as a single signed `.rego` file for small networks).
4. Sign the bundle.
5. Publish the bundle (or file) and the detached signature at stable URLs.
6. Publish the verifying public key in DeDi.
7. Reference the bundle, signature, and key in a network manifest published by the NFO.
8. Publish the manifest URL, manifest signature URL, and key lookup URL as metadata on the NFO's network registry in DeDi.
9. Participants configure `type: manifest` keyed by `network_id` — the plugin resolves the manifest, verifies the signature chain, and loads the bundle.

The plugin sits at step 9. Everything above it is NFO operational work; the plugin assumes it has been done correctly and treats anything missing or unverifiable as a hard failure.

### Bundle vs single file

| Distribution | When to use |
|---|---|
| OPA bundle (`.tar.gz`) | One or more `.rego` files, optional `data.json`, signed `.manifest`. Recommended default. |
| Single `.rego` file | Small policies. Signature is a separate detached file. No `data.json`, no sub-modules. |
| Local directory (`type: dir`) | Development only. Not signable. Do not use in production. |

### Building an OPA bundle

Install the OPA CLI from <https://www.openpolicyagent.org/docs#1-download-opa>.

Repository layout (matches Pattern 3 above):

```
policies/
  retail/
    order.rego
    catalog.rego
    helpers.rego
    order_test.rego
  data.json          # optional structured data
```

Build a signed bundle:

```bash
opa build \
  --bundle policies/retail \
  --signing-key private.pem \
  --signing-alg ES256 \
  -o retail-bundle.tar.gz
```

This packages the modules and data, generates a `.manifest`, signs the bundle, and writes `.signatures.json` inside it. See the [OPA bundle reference](https://www.openpolicyagent.org/docs/management-bundles) for the full toolchain.

### Generating a signing key

The plugin supports `ES256`, `ES384`, `ES512`, `RS256`, `RS384`, `RS512`, `PS256`, `PS384`, `PS512` for signed bundles. `ES256` is the recommended default.

Generate an ECDSA P-256 keypair compatible with `ES256`:

```bash
openssl ecparam -name prime256v1 -genkey -noout -out private.pem
openssl pkey -in private.pem -pubout -out public.pem
```

For single-file signing, the plugin auto-selects the verifier (RSA PKCS#1 v1.5 with SHA-256, ECDSA with SHA-256, or Ed25519) from the DeDi public-key record's `keyType`. EdDSA is not supported for bundle verification.

### Publishing the public key in DeDi

In your DeDi namespace, create a public key registry with the Public Key schema. For `ES256` keys, set:

- `keyType`: `ECDSA`
- `keyFormat`: `base64`
- `publicKey`: the Base64-encoded contents of `public.pem` excluding the `-----BEGIN PUBLIC KEY-----` and `-----END PUBLIC KEY-----` lines

Once the record is live, copy its lookup URL. That URL goes into the bundle's `signing_public_key_lookup_url` on the network manifest, and (mirrored) into `verification.publicKeyLookupUrl` if a participant configures `type: file` or `type: bundle` directly. With `type: manifest`, the plugin reads it from the manifest automatically.

### Publishing the bundle and referencing it from a manifest

Host the bundle at a stable URL — GitHub releases, object storage, or a CDN. Immutable per-release URLs are recommended (`/releases/v1.2.0/retail.tar.gz`); mutable latest URLs also work, in which case bump `release_id` in the manifest on every change.

A minimal manifest:

```yaml
manifest_version: "1.0"
manifest_type: "network-manifest"
network_id: "nfo.com/production"
release_id: "2026.05"

publisher:
  role: "NFO"
  domain: "nfo.example.org"

policies:
  type: "rego"
  source: "bundle"
  bundle:
    id: "retail-policy-bundle"
    url: "https://nfo.example.org/policies/retail-bundle.tar.gz"
    policy_query_path: "data.retail.result"
    signed: true
    signing_public_key_lookup_url: "https://api.dedi.global/dedi/lookup/example-nfo.com/public_key/retail-key"

governance:
  effective_from: "2026-05-15T00:00:00Z"
  effective_until: "2027-05-15T00:00:00Z"
  signed: true
```

Sign the manifest itself as a detached signature alongside the YAML — see the [docs.nfh.global signing guide](https://docs.nfh.global/beckn/creating-an-open-network/configuring-network-policies/signing-a-single-file). Then publish:

- `manifest.yaml` at a stable URL
- `manifest.yaml.sig` (detached signature) at a stable URL
- the manifest URL, signature URL, and signing key lookup URL as **registry metadata** on the NFO's DeDi network registry

### Testing policies before publishing

```bash
# Build the bundle
opa build --bundle policies/retail \
  --signing-key private.pem --signing-alg ES256 \
  -o retail-bundle.tar.gz

# Evaluate against a sample input
opa eval -b retail-bundle.tar.gz -i input.json \
  --format=raw data.retail.result

# Run unit tests
cd policies/retail && opa test . -v
```

Participants can also stand the plugin up directly against a local bundle to smoke-test it end-to-end before the manifest is published — see [`type: bundle`](#network-policy-config-file).

### Updating policies

Two supported flows:

- **New version.** Build a new bundle at a new immutable URL. Update the manifest. Bump `release_id`. This is the recommended default.
- **In-place at the same URL.** Update the bundle at the existing URL. Bump `release_id` in the manifest so participants see the change. The plugin's hot reload (`refreshInterval`) will pick up the new bundle without an adapter restart, subject to manifest cache TTL.

For the in-place flow, ensure `manifestloader.cacheTTL` is short enough relative to the policy refresh cadence you want to support, or use `disableCache` while debugging.

---

## Manifest-Backed Policies

`type: manifest` decouples the adapter operator from the policy source. Instead of hardcoding a `.rego` URL, the operator configures the network ID and the adapter fetches the verified network manifest through the `manifestloader` plugin, which resolves and loads the actual policy.

This is the recommended setup for production networks. NFOs publish their policy source and signature alongside the manifest (see [Publishing Policies as a Network Facilitator](#publishing-policies-as-a-network-facilitator)); operators only need to configure the network ID.

### Operator config

```yaml
manifestLoader:
  id: manifestloader
  config:
    cacheTTL: 24h
    fetchTimeoutSeconds: "30"
    forceRefreshOnStartup: false
    disableCache: false

checkPolicy:
  id: opapolicychecker
  config:
    networkPolicyConfig: ./config/opa-network-policies.yaml
    refreshInterval: "5m"
```

```yaml
# config/opa-network-policies.yaml
networkPolicies:
  nfh.global/testnet:
    type: manifest   # location, query, and verification must NOT be set here
```

> `location`, `query`, and `verification` must not appear on `type: manifest` entries — the manifest carries all of that. The `default` policy key cannot use `type: manifest`; the manifest resolver requires a specific network ID to fetch the right manifest.

### What the manifest must contain

All fields below are validated by the adapter at startup and on every hot-reload cycle. A manifest that fails validation causes the policy for that network to fail loading.

**Top-level fields (always required)**

| Field | Required value / constraint |
|---|---|
| `manifest_version` | Non-empty string |
| `manifest_type` | `"network-manifest"` |
| `network_id` | Must match the network policy config key exactly |
| `release_id` | Non-empty (any scalar) |
| `publisher.role` | Non-empty string |
| `publisher.domain` | Non-empty string |
| `policies.type` | `"rego"` |
| `policies.source` | `"file"` or `"bundle"` |
| `governance.signed` | Must be present (boolean) |
| `governance.effective_from` | ISO 8601 timestamp; must be in the past |
| `governance.effective_until` | Optional; if present, must be after `effective_from` and not expired |

**When `policies.source: bundle`**

| Field | Required value / constraint |
|---|---|
| `policies.bundle.id` | Non-empty string |
| `policies.bundle.url` | URL to the `.tar.gz` bundle |
| `policies.bundle.policy_query_path` | OPA query path (e.g. `data.policy.result`) |
| `policies.bundle.signed` | Boolean; when `true`, the field below is also required |
| `policies.bundle.signing_public_key_lookup_url` | Required when `policies.bundle.signed: true` |

`policies.file` must **not** be present when `policies.source: bundle`.

**When `policies.source: file`**

| Field | Required value / constraint |
|---|---|
| `policies.file.id` | Non-empty string |
| `policies.file.url` | URL to the `.rego` file |
| `policies.file.policy_query_path` | OPA query path (e.g. `data.policy.result`) |
| `policies.file.signed` | Boolean; when `true`, the two fields below are also required |
| `policies.file.signature_url` | Required when `policies.file.signed: true` |
| `policies.file.signing_public_key_lookup_url` | Required when `policies.file.signed: true` |

`policies.bundle` must **not** be present when `policies.source: file`.

### Manifest cache and reload interaction

When `refreshInterval` is set on the OPA plugin, each reload cycle asks `manifestloader` for the manifest again. Whether the manifest is actually re-fetched from the network depends on the manifest loader cache, which is controlled entirely by `manifestloader` configuration — not by the OPA plugin:

- If the manifest cache entry is still valid (within `cacheTTL`), `manifestloader` returns the cached manifest regardless of how short the OPA `refreshInterval` is.
- Set `manifestLoader.cacheTTL` ≤ `refreshInterval` to ensure each OPA reload cycle can pick up a freshened manifest.
- Set `forceRefreshOnStartup: true` on the manifest loader to force a network re-fetch of the manifest when the adapter starts, bypassing any cached entry from a previous run.
- Use `disableCache: true` during debugging to bypass the cache entirely on every fetch.

---

## Signature Verification

Verification is optional and configured per policy entry. When enabled, the plugin fetches the public key from the configured DeDi endpoint and verifies the policy content before loading it into OPA.

### Single-file policy with detached signature

```yaml
networkPolicies:
  retail.network/production:
    type: file
    location: ./policies/retail.rego
    query: data.policy.result
    verification:
      enabled: true
      publicKeyLookupUrl: https://api.dedi.global/dedi/lookup/example-nfo.com/public_key_test/retail-key
      signatureLocation: ./policies/retail.rego.sig
```

### Signed bundle

```yaml
networkPolicies:
  retail.network/production:
    type: bundle
    location: ./policies/retail-bundle.tar.gz
    query: data.retail.validation.result
    verification:
      enabled: true
      publicKeyLookupUrl: https://api.dedi.global/dedi/lookup/example-nfo.com/public_key_test/retail-key
      algorithm: ES256   # optional, defaults to ES256
```

> `verification.signatureLocation` must **not** be set for `type: bundle` — the signature is embedded inside the bundle archive itself.

### Key formats accepted from DeDi lookup

The public-key lookup response is read from these JSON fields in priority order:

| Priority | JSON path | Format |
|---|---|---|
| 1 | `data.details.publicKey` | Controlled by `data.details.keyFormat`; `"base64"` → standard padded base64, absent/empty → PEM |
| 2 | `data.details.signing_public_key` | `data.details.keyFormat` if set, otherwise defaults to `"base64"` |
| 3 | `data.details.public_key` | `data.details.keyFormat` if set, otherwise defaults to `"base64"` |
| 4 | Top-level `signing_public_key` | Always base64 |
| 5 | Top-level `public_key` | Always base64 |
| 6 | Top-level `publicKey` | Always base64 |

If none of these fields are present, the entire response body is parsed as PEM, DER, or a PEM certificate. Formats `base58`, `hex`, and JWK are not supported.

### Detached signature verification (file)

For `type: file`, the signature algorithm is determined automatically from the key type returned by DeDi — no `algorithm` field is needed:

| DeDi key type | Algorithm used |
|---|---|
| `RSA` | RSA PKCS#1 v1.5 with SHA-256 |
| `ECDSA` | ECDSA with SHA-256 |
| `Ed25519` | Ed25519 |

The detached signature file (`signatureLocation`) may contain raw bytes, base64-encoded text, or a JSON object with a top-level `signature` field.

### Bundle verification algorithms

For `type: bundle`, the `verification.algorithm` field selects the signing algorithm. Supported values:

| Family | Algorithms |
|---|---|
| ECDSA | `ES256`, `ES384`, `ES512` |
| RSA PKCS#1 v1.5 | `RS256`, `RS384`, `RS512` |
| RSA-PSS | `PS256`, `PS384`, `PS512` |

`EdDSA` is not currently supported for bundle verification.

---

## Policy Hot-Reload

When `refreshInterval` is set, a background goroutine periodically reloads and recompiles all configured policy sources without restarting the adapter.

```yaml
config:
  networkPolicyConfig: ./config/opa-network-policies.yaml
  refreshInterval: "5m"
```

### Reload guarantees

| Guarantee | Detail |
|---|---|
| **Atomic swap** | The old evaluator stays fully active until the new one is compiled — no gap in enforcement during reload |
| **Non-fatal errors** | If a reload fails (file temporarily unreachable, parse error), the error is logged and the previous policy stays active |
| **Manifest cache boundary** | For `type: manifest`, each reload asks `manifestloader` for the manifest, but the manifest is only re-fetched from the network if its cache entry has expired |
| **Goroutine lifecycle** | The reload loop stops when the adapter context is cancelled or `Close()` is invoked during shutdown |

### Choosing a refresh interval

| Scenario | Recommended setting |
|---|---|
| Production — stable policies | `"1h"` or unset |
| Staging — frequent policy updates | `"5m"` |
| Debugging manifest changes | `manifestLoader.disableCache: true` + `"30s"` |
| Emergency policy revocation | Short interval + `manifestLoader.cacheTTL` ≤ `refreshInterval` |

---

## Troubleshooting

The scenarios below cover the most common misconfiguration patterns. Each entry follows the same shape: what you observe, what causes it, and the exact change that fixes it. Enable `debugLogging: "true"` in the plugin config to get verbose OPA evaluation logs, which will show the selected policy, the evaluated query, and the raw result for every request.

### Policy evaluation returns empty/undefined result — requests rejected

**Symptom:** All requests are rejected even when the message looks valid. Logs show the OPA query returning an empty or undefined result.

**Cause:** The `query:` value in the network policy config does not match the package path in the Rego file.

**Fix:** Verify that `query: "data.<package>.<rule>"` matches the `package` declaration and the rule name exactly.

```rego
package mypolicy       # must match data.mypolicy in query

result := { ... }     # must match .result in query
```

```yaml
query: "data.mypolicy.result"   # ✓ correct
query: "data.policy.result"     # ✗ wrong if package is mypolicy
```

---

### Policy applies to unexpected actions

**Symptom:** The policy rejects `search` requests that should not be affected.

**Cause:** The `actions:` filter is not set, so the policy evaluates for every action.

**Fix:** Either set `actions:` in the network policy config to limit evaluation, or add explicit action guards in the Rego rules:

```rego
violations contains "confirm: missing provider" if {
    input.context.action == "confirm"   # guards this rule to confirm only
    not input.message.order.provider
}
```

---

### Wrong network ID matched — unexpected policy selected

**Symptom:** Requests from one network are evaluated against a different network's policy.

**Cause:** The `network_id` value in the Beckn message does not exactly match the key in `networkPolicies`. The plugin does an exact-string match.

**Fix:** Log the raw `context.networkId` from an incoming request and compare it character-for-character with the key in `opa-network-policies.yaml`. Common issues: trailing slashes, `network_id` vs `networkId` field name, mixed case.

---

### Manifest not refreshing after policy update

**Symptom:** The operator updated the manifest and set a short `refreshInterval` but the adapter keeps using the old policy.

**Cause:** The `manifestloader` cache TTL is longer than the OPA `refreshInterval`. The manifest is being served from cache on every OPA reload cycle.

**Fix:**

```yaml
manifestLoader:
  id: manifestloader
  config:
    cacheTTL: 5m   # set ≤ refreshInterval, or use disableCache: true for debugging
```

---

### Explicit `enabled: false` not falling through to `default`

**Symptom:** Requests for a specific network that has `enabled: false` are not being evaluated against the `default` policy.

**Cause:** This is intentional. An explicit `enabled: false` entry means the network is knowingly opted out of policy enforcement. It does not fall through to `default` because doing so would silently apply a global policy to a network the operator has explicitly excluded.

**Fix:** If you want `default` to apply to that network, remove the explicit entry from `networkPolicies` entirely.

---

## Relationship with Schema Validator

`opapolicychecker` and `schemav2validator` serve complementary but distinct roles. Both run as steps in the same module pipeline and can be configured side-by-side.

**`schemav2validator`** validates message **structure** — field types, required fields, enum constraints, and format rules as defined in an OpenAPI 3.x specification. It rejects structurally malformed messages before they reach business rule evaluation.

**`opapolicychecker`** evaluates **business rules** — domain-specific logic that cannot be expressed in a schema, such as "a confirm order must have a provider", "delivery lead time must exceed a configurable threshold", or "this domain value is not permitted on this network".

Configure schema validation before policy checking in your steps list so that structurally invalid messages are rejected early:

```yaml
steps:
  - validateSign
  - validateSchema   # structure check — rejects bad field types, missing required fields
  - checkPolicy      # business rules — rejects domain-logic violations
  - addRoute
```

---

## Dependencies

- `github.com/open-policy-agent/opa/v1` — OPA Go SDK for policy evaluation, bundle loading, and module compilation

---

## Known Limitations

These are known constraints in the current implementation. None affect the correctness of the policy evaluation engine itself — they are boundary conditions around specific source types and key formats.

- **Signed directories are not supported.** If signature verification is needed for multiple Rego files, package them as a signed OPA bundle (`type: bundle`) instead of using `type: dir`.
- **Non-standard route shapes.** URL-based action extraction assumes the standard Beckn adapter route `/{participant}/{direction}/{action}` (e.g. `/bpp/caller/confirm`) and falls back to `context.action` from the JSON body for other path layouts.
- **`EdDSA` not supported for bundle verification.** Only `ES*`, `RS*`, and `PS*` algorithms are supported for OPA signed bundles. Ed25519 is supported only for detached single-file signature verification.
- **Size limits.** Remote `.rego` files are limited to 1 MB; bundles are limited to 10 MB. Requests for larger artifacts will fail at startup or reload.
- **Cleartext HTTP without signing is allowed but warned for manifest-backed policies only.** If a `type: manifest` entry resolves to an unsigned `http://` policy source, the adapter logs a startup warning that a MITM can inject arbitrary Rego. Direct `type: file` and `type: bundle` entries pointing at `http://` URLs do not trigger this warning. Use `https://` or enable signature verification for any remote policy source.
