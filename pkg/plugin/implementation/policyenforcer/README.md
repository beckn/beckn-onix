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
| `policyDir` | One of `policyDir`, `policyFile`, or `policyUrls` required | — | Local directory containing `.rego` files |
| `policyFile` | | — | Single local `.rego` file path |
| `policyUrls` | | — | Comma-separated list of URLs or local paths to `.rego` files |
| `query` | No | `data.policy.violations` | Rego query returning violation strings |
| `actions` | No | `confirm` | Comma-separated beckn actions to enforce |
| `enabled` | No | `true` | Enable/disable the plugin |
| `debugLogging` | No | `false` | Enable verbose logging |
| *any other key* | No | — | Forwarded to Rego as `data.config.<key>` |

### Policy URLs

`policyUrls` accepts both remote URLs and local file paths, separated by commas:

```yaml
config:
  policyUrls: "https://policies.example.com/compliance.rego,/etc/policies/local.rego,https://policies.example.com/safety.rego"
```

### Air-Gapped Deployments

For environments without internet access, replace any URL with a local file path or volume mount:

```yaml
config:
  policyUrls: "/mounted-policies/compliance.rego,/mounted-policies/safety.rego"
```

## Example Config

```yaml
plugins:
  steps:
    - id: policyenforcer
      config:
        policyUrls: "https://policies.example.com/compliance.rego,/local/policies/safety.rego"
        actions: "confirm,init"
        query: "data.policy.violations"
        minDeliveryLeadHours: "4"
        debugLogging: "true"
```

## Relationship with Schema Validator

`policyenforcer` and `schemavalidator`/`schemav2validator` are **separate plugins** with different responsibilities:

- **Schema Validator**: Validates message **structure** against OpenAPI/JSON Schema specs
- **Policy Enforcer**: Evaluates **business rules** via OPA/Rego policies

They use different plugin interfaces (`SchemaValidator` vs `Step`), different engines, and different error types. Configure them side-by-side in your adapter config as needed.
