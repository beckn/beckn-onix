# OPA Policy Checker Plugin

Validates incoming Beckn messages against network-defined business rules using [Open Policy Agent (OPA)](https://www.openpolicyagent.org/) and the Rego policy language. Non-compliant messages are rejected with a `BadRequest` error code.

## Features

- Evaluates business rules defined in Rego policies
- Supports multiple policy sources: remote URL, local file, directory, or OPA bundle (`.tar.gz`)
- Structured result format: `{"valid": bool, "violations": []string}`
- Fail-closed on empty/undefined query results — misconfigured policies are treated as violations
- Runtime config forwarding: adapter config values are accessible in Rego as `data.config.<key>`
- Action-based enforcement: apply policies only to specific beckn actions (e.g., `confirm`, `search`)
- Configurable fetch timeout for remote policy and bundle sources
- Warns at startup when policy enforcement is explicitly disabled

## Configuration

```yaml
checkPolicy:
  id: opapolicychecker
  config:
    networkPolicyConfig: ./config/opa-network-policies.yaml
    refreshIntervalSeconds: "300"
steps:
  - checkPolicy
  - addRoute
```

### Configuration Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `networkPolicyConfig` | string | Yes | - | Path to a YAML file containing `networkPolicies` keyed by `network_id` |
| `enabled` | string | No | `"true"` | Enable or disable the plugin |
| `debugLogging` | string | No | `"false"` | Enable verbose OPA evaluation logging |
| `refreshIntervalSeconds` | string | No | - | Reload all configured policies every N seconds (0 or omit = disabled) |
| *any other key* | string | No | - | Forwarded to Rego as `data.config.<key>` |

### Network Policy Config File

The plugin loads all configured policies at startup and selects the correct one at request time using `context.networkId` or `context.network_id`.

Top-level plugin config:

```yaml
checkPolicy:
  id: opapolicychecker
  config:
    networkPolicyConfig: ./config/opa-network-policies.yaml
    refreshIntervalSeconds: "300"
```

Structured config file:

```yaml
networkPolicies:
  nfo.example.org/mobility-network:
    type: url
    location: https://nfo.example.org/policies/mobility.rego
    query: "data.mobility.policy.result"
    actions: "confirm"

  nfo.example.org/logistics-network:
    type: bundle
    location: https://nfo.example.org/policies/logistics.tar.gz
    query: "data.logistics.policy.result"

  default:
    type: file
    location: ./policies/default.rego
    query: "data.default.policy.result"
```

Behavior in network mode:

- all configured policies are loaded at startup
- request-time selection uses exact match on `context.networkId` and falls back to `context.network_id`
- if no network-specific policy matches, `default` is used when configured
- if neither a network-specific policy nor `default` matches, OPA evaluation is skipped
- if you want one global policy only, define just `default`

Each entry under `networkPolicies` supports:

- `type`: `url`, `file`, `dir`, or `bundle`
- `location`
- `query`
- optional `actions`
- optional `enabled`
- optional `debugLogging`
- optional `fetchTimeoutSeconds`

## Policy Hot-Reload

When `refreshIntervalSeconds` is set, a background goroutine periodically re-fetches and recompiles all configured policy sources without restarting the adapter:

- **Atomic swap**: the old evaluator stays fully active until the new one is compiled — no gap in enforcement
- **Non-fatal errors**: if the reload fails (e.g., file temporarily unreachable or parse error), the error is logged and the previous policy stays active
- **Goroutine lifecycle**: the reload loop stops when the adapter context is cancelled or when plugin `Close()` is invoked during shutdown

```yaml
config:
  networkPolicyConfig: ./config/opa-network-policies.yaml
  refreshIntervalSeconds: "300"  # reload every 5 minutes
```

## How It Works

### Initialization (Load Time)

1. **Load Policy Config**: Reads the structured `networkPolicyConfig` file
2. **Load Policy Sources**: Fetches `.rego` files or bundles for each configured network policy entry
3. **Compile Policies**: Compiles one evaluator per configured `network_id` plus optional `default`

### Request Evaluation (Runtime)

1. **Select Policy**: Match `context.networkId` exactly, fall back to `context.network_id`, then `default`
2. **Check Action Match**: If `actions` is configured on the selected policy, skip evaluation for non-matching actions. The plugin assumes standard adapter routes look like `/{participant}/{direction}/{action}` such as `/bpp/caller/confirm`; non-standard paths fall back to `context.action` from the JSON body.
3. **Evaluate OPA Query**: Run the selected policy with the full beckn message as `input`
4. **Handle Result**:
   - If the query returns no result (undefined) → **violation** (fail-closed)
   - If result is `{"valid": bool, "violations": []string}` → use structured format
   - If result is a `set` or `[]string` → each string is a violation
   - If result is a `bool` → `false` = violation
   - If result is a `string` → non-empty = violation
5. **Reject or Allow**: If violations are found, NACK the request with all violation messages

### Supported Query Output Formats

| Rego Output | Behavior |
|-------------|----------|
| `{"valid": bool, "violations": ["string"]}` | Structured result format (recommended) |
| `set()` / `[]string` | Each string is a violation message |
| `bool` (`true`/`false`) | `false` = denied, `true` = allowed |
| `string` | Non-empty = violation |
| Empty/undefined | **Violation** (fail-closed) — indicates misconfigured query path |

## Example Usage

### Default-only Policy

```yaml
checkPolicy:
  id: opapolicychecker
  config:
    networkPolicyConfig: ./config/opa-network-policies.yaml
```

```yaml
networkPolicies:
  default:
    type: file
    location: ./pkg/plugin/implementation/opapolicychecker/testdata/example.rego
    query: "data.policy.result"
```

## Writing Policies

Policies are written in [Rego](https://www.openpolicyagent.org/docs/latest/policy-language/). The plugin passes the full beckn message body as `input` and any adapter config values as `data.config`:

```rego
package policy

import rego.v1

# Default result: valid with no violations.
default result := {
  "valid": true,
  "violations": []
}

# Compute the result from collected violations.
result := {
  "valid": count(violations) == 0,
  "violations": violations
}

# Require provider on confirm
violations contains "confirm: missing provider" if {
    input.context.action == "confirm"
    not input.message.order.provider
}

# Configurable threshold from adapter config
violations contains "delivery lead time too short" if {
    input.context.action == "confirm"
    lead := input.message.order.fulfillments[_].start.time.duration
    to_number(lead) < to_number(data.config.minDeliveryLeadHours)
}
```

See [`testdata/example.rego`](./testdata/example.rego) for a full working example.

## Relationship with Schema Validator

`opapolicychecker` and `schemav2validator` serve different purposes:

- **Schemav2Validator**: Validates message **structure** against OpenAPI/JSON Schema specs
- **OPA Policy Checker**: Evaluates **business rules** via OPA/Rego policies

Configure them side-by-side in your adapter steps as needed.

## Plugin ID vs Step Name

- **Plugin ID** (used in `id:`): `opapolicychecker` (lowercase, implementation-specific)
- **Step name** (used in `steps:` list and YAML key): `checkPolicy` (camelCase verb)

## Dependencies

-   `github.com/open-policy-agent/opa` — OPA Go SDK for policy evaluation and bundle loading

## Known Limitations

-   **No bundle signature verification**: When using `type: bundle`, bundle signature verification is skipped. This is planned for a future enhancement.
-   **Non-standard route shapes**: URL-based action extraction assumes the standard Beckn adapter route shape `/{participant}/{direction}/{action}` and falls back to `context.action` for other path layouts.
