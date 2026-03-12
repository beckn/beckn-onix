# Policy Enforcer Plugin

OPA/Rego-based policy enforcement for beckn-onix adapters. Evaluates incoming beckn messages against configurable policies and NACKs non-compliant requests.

## Overview

The `policyenforcer` plugin is a **Step plugin** that:
- Loads `.rego` policy files from URLs, local directories, or local files
- Evaluates incoming messages against compiled OPA policies
- Returns a `BadReqErr` (NACK) when policy violations are detected
- Fails closed on evaluation errors (treats as NACK)
- Is strictly **opt-in** — adapters that don't reference it are unaffected

## Configuration

All config keys are passed via `map[string]string` in the adapter YAML config.

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `policyPaths` | Yes (at least one source required) | `./policies` (if dir exists) | Comma-separated list of policy sources — each entry is auto-detected as a **URL**, **directory**, or **file** |
| `query` | No | `data.policy.violations` | Rego query returning violation strings |
| `actions` | No | *(empty — all actions)* | Comma-separated beckn actions to enforce. When omitted, all actions are evaluated and the Rego policy itself decides which to gate. |
| `enabled` | No | `true` | Enable/disable the plugin |
| `debugLogging` | No | `false` | Enable verbose logging |
| *any other key* | No | — | Forwarded to Rego as `data.config.<key>` |

### Policy Sources

`policyPaths` is the single configuration key for all policy sources. Each comma-separated entry is **auto-detected** as:
- **Remote URL** (`http://` or `https://`): fetched via HTTP at startup
- **Local directory**: all `.rego` files loaded (`_test.rego` excluded)
- **Local file**: loaded directly

```yaml
# Single directory
config:
  policyPaths: "./policies"

# Single remote URL
config:
  policyPaths: "https://policies.example.com/compliance.rego"

# Mix of URLs, directories, and files
config:
  policyPaths: "https://policies.example.com/compliance.rego,./policies,/local/safety.rego"
```

When specifying many sources, use the YAML folded scalar (`>-`) to keep the config readable:

```yaml
config:
  policyPaths: >-
    https://policies.example.com/compliance.rego,
    https://policies.example.com/safety.rego,
    ./policies,
    /local/overrides/rate-limit.rego
```

The `>-` folds newlines into spaces, so the value is parsed as a single comma-separated string.

### Minimal Config

By default, the plugin loads `.rego` files from `./policies` and uses the query `data.policy.violations`. A zero-config setup works if your policies are in the default directory:

```yaml
policyEnforcer:
  id: policyenforcer
  config: {}
```

Or specify a custom policy location:

```yaml
policyEnforcer:
  id: policyenforcer
  config:
    policyPaths: "./policies/compliance.rego"
```

### Air-Gapped Deployments

For environments without internet access, use local file paths or volume mounts:

```yaml
config:
  policyPaths: "/mounted-policies/compliance.rego,/mounted-policies/safety.rego"
```

## Example Config

```yaml
plugins:
  policyEnforcer:
    id: policyenforcer
    config:
      policyPaths: >-
        /local/policies/,
        https://policies.example.com/compliance.rego
      minDeliveryLeadHours: "4"
      debugLogging: "true"
steps:
  - policyEnforcer
  - addRoute
```

## Relationship with Schema Validator

`policyenforcer` and `schemavalidator`/`schemav2validator` are **separate plugins** with different responsibilities:

- **Schema Validator**: Validates message **structure** against OpenAPI/JSON Schema specs
- **Policy Enforcer**: Evaluates **business rules** via OPA/Rego policies

They use different plugin interfaces (`SchemaValidator` vs `Step`), different engines, and different error types. Configure them side-by-side in your adapter config as needed.
