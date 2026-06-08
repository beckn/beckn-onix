# Schemav2Validator Plugin

Validates Beckn protocol requests against OpenAPI 3.1 specifications using kin-openapi library.

## Features

- Validates requests against OpenAPI 3.1 specs
- Supports remote URL and local file loading
- Automatic external $ref resolution
- TTL-based caching with automatic refresh
- Generic path matching (no hardcoded paths)
- Direct schema validation without router overhead
- Extended schema validation for domain-specific objects with `@context` references

## Configuration

```yaml
schemaValidator:
  id: schemav2validator
  config:
    type: url
    location: https://example.com/openapi-spec.yaml
    cacheTTL: "3600"
    extendedSchema_enabled: "true"
    extendedSchema_cacheTTL: "86400"
    extendedSchema_maxCacheSize: "100"
    extendedSchema_downloadTimeout: "30"
    extendedSchema_allowedDomains: "beckn.org,example.com"
```

### Configuration Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `type` | string | Yes | - | Primary spec source type: `"url"`, `"file"`, or `"dir"` |
| `location` | string | Yes | - | URL or file path to the primary OpenAPI 3.1 spec |
| `cacheTTL` | string | No | `"3600"` | Cache TTL in seconds before reloading all specs (primary + auxiliary) |
| `auxiliaryTypes` | string | No | - | Comma-separated source types for auxiliary specs (`"url"`, `"file"`, or `"dir"`) |
| `auxiliaryLocations` | string | No | - | Comma-separated locations for auxiliary specs — must have the same number of entries as `auxiliaryTypes` |
| `extendedSchema_enabled` | string | No | `"false"` | Enable extended schema validation for `@context` objects |
| `extendedSchema_cacheTTL` | string | No | `"86400"` | Domain schema cache TTL in seconds |
| `extendedSchema_maxCacheSize` | string | No | `"100"` | Maximum number of cached domain schemas |
| `extendedSchema_downloadTimeout` | string | No | `"30"` | Timeout for downloading domain schemas |
| `extendedSchema_allowedDomains` | string | No | `""` | Comma-separated domain whitelist (empty = all allowed) |
| `extendedSchema_localSchemaPath` | string | No | `""` | Path to local schema directory; schemas preloaded at startup, network used as fallback |

### Auxiliary Specs

Auxiliary specs allow operators to extend schema validation with additional action verbs beyond those defined in the primary Beckn spec. Both `auxiliaryTypes` and `auxiliaryLocations` must be set together as matching comma-separated lists.

**Rules:**
- Auxiliary specs are loaded after the primary spec; their actions are merged into a single index
- An auxiliary spec may only **add** new actions — any action already defined in the primary spec causes a hard error at startup
- If an auxiliary spec fails to load, it is skipped with an error logged; the primary spec remains active
- The `dir` type loads all top-level `*.yaml`, `*.yml`, and `*.json` files in the specified directory (no recursion); duplicate action definitions across files within the same dir are a hard error — the entire dir spec is rejected and startup fails
- All specs (primary and auxiliary) share the same `cacheTTL` refresh cycle

### Local Schema Directory Layout

When `extendedSchema_localSchemaPath` is set, schemas are preloaded from that directory at startup. Each schema must follow this structure:

```
config/
 schema/
  <TypeName>/
    attributes.yaml
  <AnotherType>/
    attributes.yaml
```

A startup warning is logged if no schemas are found, which usually means the directory path is wrong or the layout doesn't match the expected structure.

## How It Works

### Initialization (Load Time)

**Spec Loading**:
1. **Load Primary Spec**: Loads the primary spec from `location` (URL, file, or dir) with external `$ref` resolution and builds its action index
2. **Load Auxiliary Specs**: Each auxiliary spec is loaded independently in order; its action index is merged into the shared map. Hard error if any action collides with the primary — the adapter will not start
3. **Merged Action Index**: A single flat `action → schema` map is built from all specs for O(1) lookup at runtime
4. **Start Background Refresh**: Launches goroutine; every `cacheTTL` seconds the entire index is rebuilt from scratch (primary first, then auxiliaries). On failure the previous valid index is retained and an error is logged

**Extended Schema Setup** (if `extendedSchema_enabled: "true"`):
5. **Initialize Schema Cache**: Creates LRU cache with `maxCacheSize` (default: 100)
6. **Extended schema cleanup** ticker runs every `extendedSchema_cacheTTL` seconds (default: 86400)

### Request Validation (Runtime)

**Core Protocol Validation** (always runs):
1. **Parse Request**: Unmarshal JSON and extract `context.action`
2. **Lookup Schema**: O(1) lookup in action index (built at load time)
3. **Validate**: Call `schema.Value.VisitJSON()` with:
   - Required fields validation
   - Data type validation (string, number, boolean, object, array)
   - Format validation (email, uri, date-time, uuid, etc.)
   - Constraint validation (min/max, pattern, enum, const)
   - Nested object and array validation
4. **Return Errors**: If validation fails, format and return errors

**Extended Schema Validation** (if `extendedSchema_enabled: "true"` AND core validation passed):
5. **Scan for @context**: Recursively traverse `message` field for objects with `@context` and `@type`
6. **Filter Core Schemas**: Skip objects with `/schema/core/` in `@context` URL
7. **Validate Each Domain Object**:
   - Check domain whitelist (if `allowedDomains` configured)
   - Transform `@context` URL: `context.jsonld` → `attributes.yaml`
   - Load schema from URL/file (check cache first, download if miss)
   - Find schema by `@type` (direct match or `x-jsonld.@type` fallback)
   - Strip `@context` and `@type` metadata from object
   - Validate remaining data against domain schema
   - Prefix error paths with object location (e.g., `message.order.field`)
8. **Return Errors**: Returns first validation error (fail-fast)

## Action-Based Matching

The validator uses action-based schema matching, not URL path matching. It searches for schemas where the `context.action` field has an enum constraint containing the request's action value.

### Example OpenAPI Schema

```yaml
paths:
  /beckn/search:
    post:
      requestBody:
        content:
          application/json:
            schema:
              properties:
                context:
                  properties:
                    action:
                      enum: ["search"]  # ← Matches action="search"
```

### Matching Examples

| Request Action | Schema Enum | Match |
|----------------|-------------|-------|
| `search` | `enum: ["search"]` | ✅ Matches |
| `select` | `enum: ["select", "init"]` | ✅ Matches |
| `discover` | `enum: ["search"]` | ❌ No match |
| `on_search` | `enum: ["on_search"]` | ✅ Matches |

## External References

The validator automatically resolves external `$ref` references in OpenAPI specs:

```yaml
# Main spec at https://example.com/api.yaml
paths:
  /search:
    post:
      requestBody:
        content:
          application/json:
            schema:
              $ref: 'https://example.com/schemas/search.yaml#/SearchRequest'
```

The loader will automatically fetch and resolve the external reference.

## Example Usage

### Remote URL (Primary only)

```yaml
schemaValidator:
  id: schemav2validator
  config:
    type: url
    location: https://raw.githubusercontent.com/beckn/protocol-specifications-v2/refs/tags/core-v2.0.0-lts/api/v2.0.0/beckn.yaml
    cacheTTL: "7200"
```

### Local File (Primary only)

```yaml
schemaValidator:
  id: schemav2validator
  config:
    type: file
    location: ./validation-scripts/l2-config/mobility_1.1.0_openapi_3.1.yaml
    cacheTTL: "3600"
```

### Primary with Auxiliary Specs

Extends the Beckn core spec with additional action verbs from a local file and a directory of domain schemas:

```yaml
schemaValidator:
  id: schemav2validator
  config:
    type: url
    location: https://raw.githubusercontent.com/beckn/protocol-specifications-v2/refs/tags/core-v2.0.0-lts/api/v2.0.0/beckn.yaml
    cacheTTL: "3600"
    auxiliaryTypes: "file,dir"
    auxiliaryLocations: "/etc/onix/schemas/energy-verbs.yaml,/etc/onix/schemas/domain/"
```

### With Extended Schema Validation

```yaml
schemaValidator:
  id: schemav2validator
  config:
    type: url
    location: https://raw.githubusercontent.com/beckn/protocol-specifications-v2/refs/tags/core-v2.0.0-lts/api/v2.0.0/beckn.yaml
    cacheTTL: "3600"
    extendedSchema_enabled: "true"
    extendedSchema_cacheTTL: "86400"
    extendedSchema_maxCacheSize: "100"
    extendedSchema_downloadTimeout: "30"
    extendedSchema_allowedDomains: "raw.githubusercontent.com,schemas.beckn.org"
```

**At Load Time**:
- Creates LRU cache for domain schemas (max 100 entries)
- Starts background goroutine for cache cleanup every 24 hours

**At Runtime** (after core validation passes):
- Scans `message` field for objects with `@context` and `@type`
- Skips core Beckn schemas (containing `/schema/core/`)
- Downloads domain schemas from `@context` URLs (cached for 24 hours)
- Validates domain-specific data against schemas
- Returns errors with full JSON paths (e.g., `message.order.chargingRate`)
- Fail-fast: returns on first validation error

## Dependencies

- `github.com/getkin/kin-openapi` - OpenAPI 3 parser and validator

## Error Messages

| Scenario | Error Message |
|----------|---------------|
| Action is number | `"failed to parse JSON payload: json: cannot unmarshal number into Go struct field .context.action of type string"` |
| Action is empty | `"missing field Action in context"` |
| Action not in spec | `"unsupported action: <action>"` |
| Invalid URL | `"Invalid URL or unreachable: <url>"` |
| Schema validation fails | Returns detailed field-level errors |
| Auxiliary action collides with primary | `"auxiliary spec[N] (<location>) defines action \"<action>\" which is already defined in a previously loaded spec — auxiliary specs may only add new actions"` |
| Within-dir action collision | Error logged; dir spec skipped; startup fails with `"no actions indexed"` if no other spec is available |
| No specs loaded / all specs failed | `"schemav2validator: no actions indexed after loading all specs — configure at least one valid primary or auxiliary spec"` |
| `auxiliaryTypes` set without `auxiliaryLocations` | `"auxiliaryTypes is set but auxiliaryLocations is missing"` |
| `auxiliaryLocations` set without `auxiliaryTypes` | `"auxiliaryLocations is set but auxiliaryTypes is missing"` |
| Mismatched auxiliary list lengths | `"auxiliaryTypes and auxiliaryLocations must have the same number of comma-separated entries"` |

