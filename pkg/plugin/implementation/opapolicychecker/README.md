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
    type: file
    location: ./pkg/plugin/implementation/opapolicychecker/testdata/example.rego
    query: "data.policy.result"
    actions: "confirm,search"
steps:
  - checkPolicy
  - addRoute
```

### Configuration Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `type` | string | Yes | - | Policy source type: `url`, `file`, `dir`, or `bundle` |
| `location` | string | Yes | - | Path or URL to the policy source (`.tar.gz` for bundles) |
| `query` | string | Yes | - | Rego query path to evaluate (e.g., `data.policy.result`) |
| `actions` | string | No | *(all)* | Comma-separated beckn actions to enforce |
| `enabled` | string | No | `"true"` | Enable or disable the plugin |
| `debugLogging` | string | No | `"false"` | Enable verbose OPA evaluation logging |
| `fetchTimeoutSeconds` | string | No | `"30"` | Timeout in seconds for fetching remote `.rego` files or bundles |
| `refreshIntervalSeconds` | string | No | - | Reload policies every N seconds (0 or omit = disabled) |
| *any other key* | string | No | - | Forwarded to Rego as `data.config.<key>` |



## Policy Hot-Reload

When `refreshIntervalSeconds` is set, a background goroutine periodically re-fetches and recompiles the policy source without restarting the adapter:

- **Atomic swap**: the old evaluator stays fully active until the new one is compiled — no gap in enforcement
- **Non-fatal errors**: if the reload fails (e.g., file temporarily unreachable or parse error), the error is logged and the previous policy stays active
- **Goroutine lifecycle**: the reload loop stops when the adapter context is cancelled or when plugin `Close()` is invoked during shutdown

```yaml
config:
  type: file
  location: ./policies/compliance.rego
  query: "data.policy.result"
  refreshIntervalSeconds: "300"  # reload every 5 minutes
```

## How It Works

### Initialization (Load Time)

1. **Load Policy Source**: Fetches `.rego` files from the configured `location` — URL, file, directory, or OPA bundle
2. **Compile Policies**: Compiles all Rego modules into a single optimized `PreparedEvalQuery`
3. **Set Query**: Prepares the OPA query from the configured `query` path (e.g., `data.policy.result`)

### Request Evaluation (Runtime)

1. **Check Action Match**: If `actions` is configured, skip evaluation for non-matching actions. The plugin assumes standard adapter routes look like `/{participant}/{direction}/{action}` such as `/bpp/caller/confirm`; non-standard paths fall back to `context.action` from the JSON body.
2. **Evaluate OPA Query**: Run the prepared query with the full beckn message as `input`
3. **Handle Result**:
   - If the query returns no result (undefined) → **violation** (fail-closed)
   - If result is `{"valid": bool, "violations": []string}` → use structured format
   - If result is a `set` or `[]string` → each string is a violation
   - If result is a `bool` → `false` = violation
   - If result is a `string` → non-empty = violation
4. **Reject or Allow**: If violations are found, NACK the request with all violation messages

### Supported Query Output Formats

| Rego Output | Behavior |
|-------------|----------|
| `{"valid": bool, "violations": ["string"]}` | Structured result format (recommended) |
| `set()` / `[]string` | Each string is a violation message |
| `bool` (`true`/`false`) | `false` = denied, `true` = allowed |
| `string` | Non-empty = violation |
| Empty/undefined | **Violation** (fail-closed) — indicates misconfigured query path |

## Example Usage

### Local File

```yaml
checkPolicy:
  id: opapolicychecker
  config:
    type: file
    location: ./pkg/plugin/implementation/opapolicychecker/testdata/example.rego
    query: "data.policy.result"
```

### Remote URL

```yaml
checkPolicy:
  id: opapolicychecker
  config:
    type: url
    location: https://policies.example.com/compliance.rego
    query: "data.policy.result"
    fetchTimeoutSeconds: "10"
```

### Local Directory (multiple `.rego` files)

```yaml
checkPolicy:
  id: opapolicychecker
  config:
    type: dir
    location: ./policies
    query: "data.policy.result"
```

### OPA Bundle (`.tar.gz`)

```yaml
checkPolicy:
  id: opapolicychecker
  config:
    type: bundle
    location: https://nfo.example.org/policies/bundle.tar.gz
    query: "data.retail.validation.result"
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
-   **Network-level scoping**: Policies apply to all messages handled by the adapter instance. Per-network policy mapping (by `networkId`) is tracked for follow-up.
-   **Non-standard route shapes**: URL-based action extraction assumes the standard Beckn adapter route shape `/{participant}/{direction}/{action}` and falls back to `context.action` for other path layouts.
