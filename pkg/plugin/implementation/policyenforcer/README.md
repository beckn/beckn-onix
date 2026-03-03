# Policy Enforcer Plugin

OPA/Rego-based policy enforcement for beckn-onix adapters. Evaluates incoming beckn messages against configurable policies and NACKs non-compliant requests.

## Overview

The `policyenforcer` plugin is a **Step plugin** that:
- Loads `.rego` policy files from local directories, files, URLs, or local paths
- Evaluates incoming messages against compiled OPA policies
- Returns a `BadReqErr` (NACK) when policy violations are detected
- Fails closed on evaluation errors (treats as NACK)
- Is strictly **opt-in** — adapters that don't reference it are unaffected

## Configuration

All config keys are passed via `map[string]string` in the adapter YAML config.

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `policyUrls` | At least one of `policyUrls`, `policyDir`, or `policyFile` required | — | Comma-separated list of URLs, local file paths, or directory paths to `.rego` files |
| `policyPaths` | | `./policies` | Local directory or path containing `.rego` files |
| `policyFile` | | — | Single local `.rego` file path |
| `query` | No | `data.policy.violations` | Rego query returning violation strings |
| `actions` | No | *(empty — all actions)* | Comma-separated beckn actions to enforce. When omitted, all actions are evaluated and the Rego policy itself decides which to gate. |
| `enabled` | No | `true` | Enable/disable the plugin |
| `debugLogging` | No | `false` | Enable verbose logging |
| *any other key* | No | — | Forwarded to Rego as `data.config.<key>` |

### Policy Sources

`policyUrls` is the primary configuration key. It accepts a comma-separated list of:
- **Remote URLs**: `https://policies.example.com/compliance.rego`
- **Local file paths**: `/etc/policies/local.rego`
- **Directory paths**: `/etc/policies/` (all `.rego` files loaded, `_test.rego` excluded)

```yaml
config:
  policyUrls: "https://policies.example.com/compliance.rego,/etc/policies/,/local/safety.rego"
```

When specifying many URLs, use the YAML folded scalar (`>-`) to keep the config readable:

```yaml
config:
  policyUrls: >-
    https://policies.example.com/compliance.rego,
    https://policies.example.com/safety.rego,
    https://policies.example.com/rate-limit.rego,
    /local/policies/,
    https://policies.example.com/auth.rego
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
    policyUrls: "./policies/compliance.rego"
```

### Air-Gapped Deployments

For environments without internet access, use local file paths or volume mounts:

```yaml
config:
  policyUrls: "/mounted-policies/compliance.rego,/mounted-policies/safety.rego"
```

## Example Config

```yaml
plugins:
  policyEnforcer:
    id: policyenforcer
    config:
      policyUrls: "https://policies.example.com/compliance.rego,/local/policies/"
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
