# SchemaValidator Plugin

A JSON schema validation plugin for beckn-onix that validates incoming request payloads against pre-defined JSON schemas based on domain, version, and endpoint.

## Overview

The SchemaValidator plugin provides robust JSON schema validation for beckn-onix requests. It automatically loads and caches JSON schemas from a directory structure and validates incoming payloads based on the context information (domain and version) and the API endpoint.

## Features

- **Automatic Schema Loading**: Recursively loads all JSON schema files from a specified directory
- **Context-Aware Validation**: Validates payloads based on domain, version, and endpoint extracted from the request
- **Schema Caching**: Caches compiled schemas in memory for fast validation
- **Detailed Error Reporting**: Provides specific validation errors with field paths and messages
- **Flexible Directory Structure**: Supports nested directory structures for organizing schemas
- **Domain Normalization**: Handles domain names with colons (e.g., converts `nic2004:52110` to `nic2004_52110`)

## Configuration

### Plugin Configuration

In your beckn-onix configuration file:

```yaml
plugins:
  schemaValidator:
    id: schemavalidator
    config:
      schemaDir: ./schemas  # Path to directory containing JSON schema files
```

### Configuration Options

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `schemaDir` | string | Yes | Path to the directory containing JSON schema files |

## Schema Directory Structure

The plugin expects a specific directory structure for organizing schemas:

```
schemas/
├── domain1/
│   ├── v1.0/
│   │   ├── search.json
│   │   ├── select.json
│   │   └── init.json
│   └── v2.0/
│       ├── search.json
│       └── select.json
├── nic2004_52110/
│   └── v1.0/
│       ├── search.json
│       └── on_search.json
└── mobility/
    └── v1.0/
        ├── search.json
        ├── init.json
        └── confirm.json
```

### Schema File Naming Convention

Schemas are organized in a three-level hierarchy:
1. **Domain**: The domain from the request context (e.g., `retail`, `mobility`, `nic2004_52110`)
2. **Version**: The version prefixed with 'v' (e.g., `v1.0`, `v2.0`)
3. **Endpoint**: The API endpoint name (e.g., `search.json`, `on_search.json`)

### Schema Key Generation

The plugin generates cache keys using the format: `{domain}_{version}_{endpoint}`

For example:
- Domain: `nic2004:52110` → normalized to `nic2004_52110`
- Version: `1.0` → prefixed to `v1.0`  
- Endpoint: `search`
- **Final cache key**: `nic2004_52110_v1.0_search`

## Schema Validation Process

### 1. Request Analysis
The plugin extracts validation parameters from:
- **Request payload**: `context.domain` and `context.version`
- **Request URL**: endpoint name from the URL path

### 2. Schema Selection
Based on the extracted information, it:
1. Normalizes the domain name (replaces `:` with `_`)
2. Prefixes version with `v`
3. Constructs the schema key: `{domain}_{version}_{endpoint}`
4. Retrieves the corresponding schema from cache

### 3. Validation
Validates the entire request payload against the selected schema using the [jsonschema/v6](https://github.com/santhosh-tekuri/jsonschema) library.

## Example Schema Files

### Basic Schema Structure
All schemas should validate the context object:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "context": {
      "type": "object",
      "properties": {
        "domain": {
          "type": "string"
        },
        "version": {
          "type": "string"
        },
        "action": {
          "type": "string"
        },
        "bap_id": {
          "type": "string"
        },
        "bpp_id": {
          "type": "string"
        },
        "transaction_id": {
          "type": "string",
          "format": "uuid"
        },
        "message_id": {
          "type": "string",
          "format": "uuid"
        },
        "timestamp": {
          "type": "string",
          "format": "date-time"
        }
      },
      "required": ["domain", "version", "action"]
    }
  },
  "required": ["context"]
}
```

### Search Endpoint Schema Example
`schemas/retail/v1.0/search.json`:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "context": {
      "type": "object",
      "properties": {
        "domain": {"type": "string"},
        "version": {"type": "string"},
        "action": {"const": "search"}
      },
      "required": ["domain", "version", "action"]
    },
    "message": {
      "type": "object",
      "properties": {
        "intent": {
          "type": "object",
          "properties": {
            "item": {
              "type": "object",
              "properties": {
                "descriptor": {
                  "type": "object",
                  "properties": {
                    "name": {"type": "string"}
                  }
                }
              }
            }
          }
        }
      }
    }
  },
  "required": ["context", "message"]
}
```

## Request Payload Format

The plugin expects incoming requests to have this structure:

```json
{
  "context": {
    "domain": "retail",
    "version": "1.0",
    "action": "search",
    "bap_id": "example-bap",
    "bpp_id": "example-bpp",
    "transaction_id": "uuid-here",
    "message_id": "uuid-here",
    "timestamp": "2023-06-15T09:30:00.000Z"
  },
  "message": {
    // endpoint-specific payload
  }
}
```

## Error Handling

The plugin provides detailed error reporting for validation failures:

### Schema Validation Errors
When validation fails, the plugin returns a `SchemaValidationErr` with detailed information:

```json
{
  "type": "SCHEMA_VALIDATION_ERROR",
  "errors": [
    {
      "path": "context.action",
      "message": "missing property 'action'"
    },
    {
      "path": "message.intent.item",
      "message": "property 'item' is required"
    }
  ]
}
```

### Common Error Types
- **Missing Context Fields**: `missing field Domain in context` or `missing field Version in context`
- **Schema Not Found**: `schema not found for domain: {domain}`
- **JSON Parse Error**: `failed to parse JSON payload`
- **Validation Failure**: Detailed field-level validation errors

## Usage Examples

### 1. Valid Request
**Request URL**: `POST /search`
**Payload**:
```json
{
  "context": {
    "domain": "retail",
    "version": "1.0",
    "action": "search"
  },
  "message": {
    "intent": {
      "item": {
        "descriptor": {
          "name": "laptop"
        }
      }
    }
  }
}
```
**Schema Used**: `schemas/retail/v1.0/search.json`
**Result**: ✅ Validation passes

### 2. Domain with Special Characters
**Request URL**: `POST /search`
**Payload**:
```json
{
  "context": {
    "domain": "nic2004:52110",
    "version": "1.0",
    "action": "search"
  },
  "message": {}
}
```
**Schema Used**: `schemas/nic2004_52110/v1.0/search.json`
**Result**: ✅ Domain normalized automatically

### 3. Missing Required Field
**Request URL**: `POST /search`
**Payload**:
```json
{
  "context": {
    "domain": "retail",
    "version": "1.0"
    // missing "action" field
  },
  "message": {}
}
```
**Result**: ❌ Validation fails with detailed error

## Setup Instructions

### 1. Directory Setup
Create your schema directory structure:

```bash
mkdir -p schemas/retail/v1.0
mkdir -p schemas/mobility/v1.0
mkdir -p schemas/nic2004_52110/v1.0
```

### 2. Add Schema Files
Create JSON schema files for each endpoint in the appropriate directories.

### 3. Configure Plugin
Add the schemavalidator plugin to your beckn-onix configuration:

```yaml
plugins:
  schemaValidator:
    id: schemavalidator
    config:
      schemaDir: ./schemas
```

### 4. Start beckn-onix
The plugin will automatically:
- Load all schema files during initialization
- Cache compiled schemas for fast validation
- Validate incoming requests against appropriate schemas

## Best Practices

### Schema Design
1. **Always validate context**: Ensure all schemas validate the required context fields
2. **Use specific constraints**: Use `const` for exact matches (e.g., action names)
3. **Define required fields**: Clearly specify which fields are mandatory
4. **Use format validation**: Leverage built-in formats like `uuid`, `date-time`, `email`

### Directory Organization
1. **Consistent naming**: Use lowercase, underscore-separated names for domains
2. **Version management**: Keep different versions in separate directories
3. **Clear endpoint names**: Use descriptive names that match your API endpoints

### Error Handling
1. **Specific schemas**: Create endpoint-specific schemas for better validation
2. **Meaningful error messages**: Design schemas to provide clear validation feedback
3. **Test schemas**: Validate your schemas against sample payloads

## Testing

Run the plugin tests:

```bash
cd pkg/plugin/implementation/schemavalidator
go test -v ./...
```

## Performance Considerations

- **Schema Caching**: Schemas are compiled once during initialization and cached in memory
- **Fast Validation**: Uses efficient jsonschema library for validation
- **Memory Usage**: Schemas are kept in memory for the lifetime of the application

## Troubleshooting

### Common Issues

1. **Schema not found**
   - Verify directory structure matches `domain/version/endpoint.json`
   - Check domain normalization (`:` becomes `_`)
   - Ensure version is prefixed with `v`

2. **Schema compilation failed**
   - Validate JSON schema syntax
   - Check for circular references
   - Ensure schema follows JSON Schema Draft 7

3. **Validation fails unexpectedly**
   - Compare request payload with schema requirements
   - Check for typos in field names
   - Verify data types match schema expectations

### Debug Mode

Enable debug logging to see detailed validation information:

```yaml
log:
  level: debug
```

This will show:
- Schema loading process
- Cache key generation
- Validation attempts
- Error details

## Dependencies

- [jsonschema/v6](https://github.com/santhosh-tekuri/jsonschema): JSON Schema validation library
- Standard Go libraries for file system operations

## License

This plugin follows the same license as the main beckn-onix project.
