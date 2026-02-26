# Beckn-ONIX Configuration Guide

## Table of Contents
1. [Overview](#overview)
2. [Configuration File Structure](#configuration-file-structure)
3. [Top-Level Configuration](#top-level-configuration)
4. [HTTP Configuration](#http-configuration)
5. [Logging Configuration](#logging-configuration)
6. [Metrics Configuration](#metrics-configuration)
7. [Plugin Manager Configuration](#plugin-manager-configuration)
8. [Module Configuration](#module-configuration)
9. [Handler Configuration](#handler-configuration)
10. [Plugin Configuration](#plugin-configuration)
11. [Routing Configuration](#routing-configuration)
12. [Deployment Scenarios](#deployment-scenarios)
13. [Configuration Examples](#configuration-examples)

---

## Overview

Beckn-ONIX uses YAML configuration files to define its behavior. The plugin based architecture enables beckn enabled network participants to deploy onix adapter in variety of scenarios, all of which can be controlled and configured using the adapter config files and hence, understanding how the configuration is strctured is very critical.

let's start with understanding following key concepts first.

### Module Handler
A Module Handler or simply handler encapsulates execution logic (steps and plugins) for partcular scenario. Scenario could be typically classified in 2 buckets, `txn caller`, outgoing call into Beckn network and `txn receiver`, incoming call from Beckn network. The handler enables flexibility to execute different steps (peformed by different plugins) for both scenarios. A single deployed onix adapter service can run multiple handlers and hence a single adapter can support multiple BAPs and/or BPPs. Each handler defines its own set of steps and plugin configurations.

### Plugins
This is a sub section to the handler, meaning each handler can load a separate set of plugins. Plugins section enlists all plugins to be loaded at the start, and further includes configuration parameters for each of the listed plugin.

### Steps
This is a list of steps that executed by the handler in the same given order.

The configuration file is an input to the onix adapter and it is recommended to use different files for different deployment scenarios (local development, production, BAP-only, BPP-only, or combined).

### Configuration File Locations

```
config/
├── local-dev.yaml                    # Local development (with Vault)
├── local-simple.yaml                 # Local development (embedded keys)
├── local-routing.yaml                # Local routing rules
├── onix/                             # Production combined mode
│   ├── adapter.yaml
│   ├── bapTxnCaller-routing.yaml
│   ├── bapTxnReciever-routing.yaml
│   ├── bppTxnCaller-routing.yaml
│   ├── bppTxnReciever-routing.yaml
│   └── plugin.yaml
├── onix-bap/                         # Production BAP-only mode
│   ├── adapter.yaml
│   ├── bapTxnCaller-routing.yaml
│   ├── bapTxnReciever-routing.yaml
│   └── plugin.yaml
└── onix-bpp/                         # Production BPP-only mode
    ├── adapter.yaml
    ├── bppTxnCaller-routing.yaml
    ├── bppTxnReciever-routing.yaml
    └── plugin.yaml
```

---

## Configuration File Structure

### Main Configuration File (adapter.yaml)

The main configuration file follows this structure:

```yaml
appName: "onix-local"
log: {...}
metrics: {...}
http: {...}
pluginManager: {...}
modules: [...]
```

---

## Top-Level Configuration

### `appName`
**Type**: `string`  
**Required**: Yes  
**Description**: Application identifier used in logging and monitoring.

**Example**:
```yaml
appName: "onix-local"
```

---

## HTTP Configuration

### `http`
**Type**: `object`  
**Required**: Yes  
**Description**: HTTP server configuration including port and timeout settings.

#### Parameters:

##### `port`
**Type**: `string`  
**Required**: Yes  
**Description**: Port number on which the HTTP server listens.  
**Example**: `"8081"`, `"8080"`

##### `timeout`
**Type**: `object`  
**Required**: Yes  
**Description**: HTTP timeout configurations.

###### `timeout.read`
**Type**: `integer` (seconds)  
**Required**: Yes  
**Default**: `30`  
**Description**: Maximum duration for reading the entire request, including the body.

###### `timeout.write`
**Type**: `integer` (seconds)  
**Required**: Yes  
**Default**: `30`  
**Description**: Maximum duration before timing out writes of the response.

###### `timeout.idle`
**Type**: `integer` (seconds)  
**Required**: Yes  
**Default**: `30`  
**Description**: Maximum amount of time to wait for the next request when keep-alives are enabled.

**Example**:
```yaml
http:
  port: 8081
  timeout:
    read: 30
    write: 30
    idle: 30
```

---

## Logging Configuration

### `log`
**Type**: `object`  
**Required**: Yes  
**Description**: Logging configuration for the application.

#### Parameters:

##### `level`
**Type**: `string`  
**Required**: Yes  
**Options**: `debug`, `info`, `warn`, `error`, `fatal`  
**Description**: Sets the minimum log level. Messages below this level will not be logged.

##### `destinations`
**Type**: `array`  
**Required**: Yes  
**Description**: List of log output destinations.

###### `destinations[].type`
**Type**: `string`  
**Options**: `stdout`, `file`  
**Description**: Type of log destination.

##### `contextKeys`
**Type**: `array` of `string`  
**Required**: No  
**Description**: Context keys to include in structured logs for request tracing.  
**Common Values**: `transaction_id`, `message_id`, `subscriber_id`, `module_id`

**Example**:
```yaml
log:
  level: debug
  destinations:
    - type: stdout
  contextKeys:
    - transaction_id
    - message_id
    - subscriber_id
    - module_id
```

---

## Application-Level Plugins Configuration

### `plugins`
**Type**: `object`  
**Required**: No  
**Description**: Application-level plugin configurations. These plugins apply to the entire application and are shared across all modules.

#### `plugins.otelsetup`
**Type**: `object`  
**Required**: No  
**Description**: OpenTelemetry (OTLP) configuration for metrics, traces, and logs. When configured, telemetry is exported to an OTLP collector endpoint. Omit this block to run without telemetry.

##### Parameters:

###### `id`
**Type**: `string`  
**Required**: Yes  
**Description**: Plugin identifier. Must be `"otelsetup"`.

###### `config`
**Type**: `object`  
**Required**: Yes  
**Description**: Plugin configuration parameters.

###### `config.otlpEndpoint`
**Type**: `string`  
**Required**: Yes (when OtelSetup is used)  
**Description**: OTLP gRPC endpoint (host:port) for exporting metrics, traces, and logs. Example: `"localhost:4317"`, `"otel-collector-bap:4317"`.

###### `config.serviceName`
**Type**: `string`  
**Required**: No  
**Default**: `"beckn-onix"`  
**Description**: Sets the `service.name` resource attribute.

###### `config.serviceVersion`
**Type**: `string`  
**Required**: No  
**Description**: Sets the `service.version` resource attribute.

###### `config.environment`
**Type**: `string`  
**Required**: No  
**Default**: `"development"`  
**Description**: Sets the `deployment.environment` attribute (e.g., `development`, `staging`, `production`).

###### `config.domain`
**Type**: `string`  
**Required**: No  
**Default**: `"9090"`  
**Description**: Port on which the metrics HTTP server will listen. The metrics endpoint is hosted on a separate server from the main application.

###### `config.enableMetrics`
**Type**: `string` (boolean)  
**Required**: No  
**Default**: `"false"`  
**Description**: Enables metrics collection and OTLP metric export. Use `"true"` or `"false"` as a string.

###### `config.enableTracing`
**Type**: `string` (boolean)  
**Required**: No  
**Default**: `"false"`  
**Description**: Enables trace export via OTLP. Use `"true"` or `"false"` as a string.

###### `config.enableLogs`
**Type**: `string` (boolean)  
**Required**: No  
**Default**: `"false"`  
**Description**: Enables log export via OTLP (e.g. audit logs). Use `"true"` or `"false"` as a string.

###### `config.timeInterval`
**Type**: `string` (integer)  
**Required**: No  
**Default**: `"5"`  
**Description**: Time interval in seconds used for periodic metric export or batching.

###### `config.auditFieldsConfig`
**Type**: `string`  
**Required**: No  
**Description**: Path to a YAML file that defines which request/response fields are included in audit logs, per action. See [Audit fields configuration](#audit-fields-configuration). Example: `"/app/config/audit-fields.yaml"`.


**Example - OTLP export with audit logs** (e.g. `config/local-beckn-one-bap.yaml`):
```yaml
plugins:
  otelsetup:
    id: otelsetup
    config:
      serviceName: "beckn-one-bap"
      serviceVersion: "1.0.0"
      environment: "development"
      domain: "ev_charging"
      otlpEndpoint: "otel-collector-bap:4317"
      enableMetrics: "true"
      enableTracing: "true"
      enableLogs: "true"
      timeInterval: "5"
      networkMetricsGranularity: "2min"
      networkMetricsFrequency: "4min"
      auditFieldsConfig: "/app/config/audit-fields.yaml"
```



### Audit fields configuration

When `config.auditFieldsConfig` points to a YAML file, audit logs (emitted via OTLP when `enableLogs: "true"`) include only the fields you list per action. The file format:

```yaml
auditRules:
  default:                           # Optional: fallback for actions without a specific list
    - context.transaction_id
    - context.message_id
    - context.action
    - context.domain
    - context.bap_id
    - context.bpp_id
  discover:
    - context.transaction_id
    - context.message_id
    - context.action
    - context.timestamp
    - message.filters
    - message.spatial
  select:
    - context.transaction_id
    - context.message_id
    - context.action
    - message.order.beckn:buyer.beckn:id
    # ... more dot-path fields
```

- **Top-level key**: `auditRules`.
- **Action keys**: Use Beckn action names (e.g. `discover`, `select`, `init`, `confirm`, `update`, `track`, `cancel`, `rating`, `support`). Use `default` for actions that do not have a specific list.
- **Values**: List of dot-path strings into the request/response JSON (e.g. `context.transaction_id`, `message.order.beckn:id`). Namespaced keys use colons (e.g. `beckn:id`).

See `config/audit-fields.yaml` for a full example.

### Metrics Collected

When OtelSetup is configured with `otlpEndpoint`, metrics and traces are exported via OTLP (no separate metrics HTTP server). Scrape metrics from your OTLP collector 

Metrics are organized by module for better maintainability and encapsulation:

#### OTel Setup (from `otelsetup` plugin)
- OTLP export for metrics, traces, and logs (gRPC endpoint). 
- Go runtime instrumentation (`go_*`), resource attributes, and meter/tracer provider wiring.
- When `enableLogs: "true"` and `auditFieldsConfig` is set, audit logs are emitted via OTLP with fields defined in the audit-fields YAML.

#### Step Execution Metrics (from `telemetry` package)
- `onix_step_executions_total`, `onix_step_execution_duration_seconds`, `onix_step_errors_total`

#### Handler Metrics (from `handler` module)
- `onix_http_request_count` – HTTP requests by status class, route, method, role, sender, recipient (and optional network metric attributes).
- `beckn_signature_validations_total` - Signature validation attempts
- `beckn_schema_validations_total` - Schema validation attempts
- `onix_routing_decisions_total` - Routing decisions taken by handler

#### Cache Metrics (from `cache` plugin)
- `onix_cache_operations_total`, `onix_cache_hits_total`, `onix_cache_misses_total`

#### Plugin Metrics (from `telemetry` package)
- `onix_plugin_execution_duration_seconds`, `onix_plugin_errors_total`

#### Runtime Metrics
- Go runtime metrics (`go_*`) and Redis instrumentation via `redisotel`

Each metric includes consistent labels such as `module`, `role`, `action`, `status`, `step`, `plugin_id`, and `schema_version` to enable low-cardinality dashboards.

**Note**: Metric definitions are now located in their respective modules:
- OTel setup: `pkg/plugin/implementation/otelsetup`
- Step metrics: `core/module/handler/step_metrics.go`
- Handler metrics: `core/module/handler/handlerMetrics.go`
- Cache metrics: `pkg/plugin/implementation/cache/cache_metrics.go`
- Plugin metrics: `pkg/telemetry/pluginMetrics.go`

---

## Plugin Manager Configuration

### `pluginManager`
**Type**: `object`  
**Required**: Yes  
**Description**: Configuration for the plugin management system.

#### Parameters:

##### `root`
**Type**: `string`  
**Required**: Yes  
**Description**: Local directory path where plugin binaries (`.so` files) are stored.  
**Example**: `./plugins`, `/app/plugins`

##### `remoteRoot`
**Type**: `string`  
**Required**: No  
**Description**: Path to remote plugin bundle (typically in GCS or S3) for production deployments.  
**Example**: `/mnt/gcs/plugins/plugins_bundle.zip`

**Example**:
```yaml
pluginManager:
  root: ./plugins
  remoteRoot: /mnt/gcs/plugins/plugins_bundle.zip
```

---

## Module Configuration

### `modules`
**Type**: `array`  
**Required**: Yes  
**Description**: List of transaction processing modules. Each module represents an HTTP endpoint handler with its own configuration.

#### Module Types:

There are four main module types in Beckn-ONIX:

1. **bapTxnReceiver**: Receives callback responses at BAP from BPP
2. **bapTxnCaller**: Sends outgoing requests from BAP to BPP/Gateway
3. **bppTxnReceiver**: Receives incoming requests at BPP from BAP
4. **bppTxnCaller**: Sends callback responses from BPP to BAP

#### Parameters:

##### `name`
**Type**: `string`  
**Required**: Yes  
**Description**: Unique identifier for the module.  
**Example**: `bapTxnReceiver`, `bapTxnCaller`, `bppTxnReceiver`, `bppTxnCaller`

##### `path`
**Type**: `string`  
**Required**: Yes  
**Description**: HTTP path prefix for this module's endpoints.  
**Example**: `/bap/receiver/`, `/bap/caller/`, `/bpp/receiver/`, `/bpp/caller/`

##### `handler`
**Type**: `object`  
**Required**: Yes  
**Description**: Handler configuration for processing requests. See [Handler Configuration](#handler-configuration).

**Example**:
```yaml
modules:
  - name: bapTxnReceiver
    path: /bap/receiver/
    handler:
      type: std
      role: bap
      # ... handler configuration
```

---

## Handler Configuration

### `handler`
**Type**: `object`  
**Required**: Yes  
**Description**: Defines how requests are processed by a module.

#### Parameters:

##### `type`
**Type**: `string`  
**Required**: Yes  
**Options**: `std` (standard handler)  
**Description**: Type of handler. Currently only `std` is supported.

##### `role`
**Type**: `string`  
**Required**: Yes  
**Options**: `bap`, `bpp`  
**Description**: Role of this handler in the Beckn protocol.

##### `subscriberId`
**Type**: `string`  
**Required**: No  
**Description**: Subscriber ID for the participant. Used primarily for BPP modules.  
**Example**: `bpp1`

##### `httpClientConfig`
**Type**: `object`  
**Required**: Yes  
**Description**: HTTP client configuration for outgoing requests.

###### `maxIdleConns`
**Type**: `integer`  
**Default**: `1000`  
**Description**: Maximum number of idle connections across all hosts.

###### `maxIdleConnsPerHost`
**Type**: `integer`  
**Default**: `200`  
**Description**: Maximum idle connections to keep per host.

###### `idleConnTimeout`
**Type**: `duration`  
**Default**: `300s`  
**Description**: Maximum time an idle connection remains open.

###### `responseHeaderTimeout`
**Type**: `duration`  
**Default**: `5s`  
**Description**: Time to wait for server response headers.

##### `plugins`
**Type**: `object`  
**Required**: Yes  
**Description**: Plugin configurations. See [Plugin Configuration](#plugin-configuration).

##### `steps`
**Type**: `array` of `string`  
**Required**: Yes  
**Description**: Ordered list of processing steps to execute for each request.  
**Common Steps**:
- `validateSign` - Validate digital signature
- `addRoute` - Determine routing destination
- `validateSchema` - Validate against JSON schema
- `sign` - Sign outgoing request
- `publish` - Publish to message queue

**Example**:
```yaml
handler:
  type: std
  role: bap
  httpClientConfig:
    maxIdleConns: 1000
    maxIdleConnsPerHost: 200
    idleConnTimeout: 300s
    responseHeaderTimeout: 5s
  plugins:
    # ... plugin configurations
  steps:
    - validateSign
    - addRoute
    - validateSchema
```

---

## Plugin Configuration

### Plugin Structure

Each plugin configuration follows this structure:

```yaml
pluginName:
  id: plugin-identifier
  config:
    key1: value1
    key2: value2
```

### Available Plugins

#### 1. Registry Plugin

**Purpose**: Lookup participant information from Beckn registry.

**Configuration**:
```yaml
registry:
  id: registry
  config:
    url: http://localhost:8080/reg
    retry_max: 3
    retry_wait_min: 100ms
    retry_wait_max: 500ms
```

**Parameters**:
- `url`: Registry endpoint URL
- `retry_max`: Maximum number of retry attempts
- `retry_wait_min`: Minimum wait time between retries
- `retry_wait_max`: Maximum wait time between retries

---

#### 2. Dediregistry Plugin

**Purpose**: Lookup participant information from a Decentralized Discovery (DeDi) registry.

**Configuration**:
```yaml
registry:
  id: dediregistry
  config:
    url: "https://dedi-wrapper.example.com/dedi"
    registryName: "subscribers.beckn.one"
    timeout: 30
    retry_max: 3
    retry_wait_min: 1s
    retry_wait_max: 5s
```

**Parameters**:
- `url`: DeDi wrapper API base URL (Required)
- `registryName`: Name of the registry (Required)
- `timeout`: Request timeout in seconds (Optional, default: client default)
- `retry_max`: Maximum number of retry attempts (Optional, default: 4)
- `retry_wait_min`: Minimum wait time between retries in duration format (Optional, default: 1s)
- `retry_wait_max`: Maximum wait time between retries in duration format (Optional, default: 30s)

---

#### 3. Key Manager Plugin

**Purpose**: Manage cryptographic keys for signing and verification.

##### Vault-based Key Manager (Production)

```yaml
keyManager:
  id: keymanager
  config:
    projectID: beckn-onix-local
    vaultAddr: http://localhost:8200
    kvVersion: v2
    mountPath: beckn
```

**Parameters**:
- `projectID`: GCP project ID or identifier
- `vaultAddr`: HashiCorp Vault address
- `kvVersion`: Vault KV secrets engine version (`v1` or `v2`)
- `mountPath`: Vault mount path for secrets

##### Secrets Manager Key Manager (Production)

```yaml
keyManager:
  id: secretskeymanager
  config:
    projectID: ${projectID}
```

**Parameters**:
- `projectID`: GCP project ID (supports environment variable substitution)

##### Simple Key Manager (Development)

```yaml
keyManager:
  id: simplekeymanager
  config: {}
```

**Parameters**: None required. Uses embedded Ed25519 keys stored in the binary.

---

#### 3. Cache Plugin

**Purpose**: Redis-based caching for responses.

```yaml
cache:
  id: cache
  config:
    addr: localhost:6379
```

**Or for production with Redis cluster:**

```yaml
cache:
  id: redis
  config:
    addr: 10.81.192.4:6379
```

**Or with TLS enabled:**

```yaml
cache:
  id: cache
  config:
    addr: redis.example.com:6380
    use_tls: "true"
```

**Parameters**:
- `addr`: Redis server address and port
- `use_tls`: Enable TLS connection to Redis (`"true"` to enable, omit or any other value to disable). Default: disabled.

---

#### 4. Schema Validator Plugin

**Purpose**: Validate requests/responses against JSON schemas.

```yaml
schemaValidator:
  id: schemavalidator
  config:
    schemaDir: ./schemas
```

**Parameters**:
- `schemaDir`: Directory containing JSON schema files organized by domain and version

---

#### 5. Schema2Validator Plugin

**Purpose**: Validate requests against OpenAPI 3.x specifications. Supports core protocol validation and optional extended validation for domain-specific objects with `@context` references.

```yaml
schemaValidator:
  id: schemav2validator
  config:
    type: url
    location: https://raw.githubusercontent.com/beckn/protocol-specifications-new/refs/heads/draft/api-specs/beckn-protocol-api.yaml
    cacheTTL: "3600"
    extendedSchema_enabled: "true"
    extendedSchema_cacheTTL: "86400"
    extendedSchema_maxCacheSize: "100"
    extendedSchema_downloadTimeout: "30"
    extendedSchema_allowedDomains: "beckn.org,example.com"
```

**Or for local files:**

```yaml
schemaValidator:
  id: schemav2validator
  config:
    type: file
    location: ./validation-scripts/l2-config/mobility_1.1.0_openapi_3.1.yaml
    cacheTTL: "3600"
    extendedSchema_enabled: "false"
```

**Parameters**:
- `type`: Source type - `"url"` for remote specs, `"file"` for local files
- `location`: URL or file path to OpenAPI 3.1 specification
- `cacheTTL`: Cache TTL in seconds before reloading spec (default: `"3600"`)
- `extendedSchema_enabled`: Enable extended schema validation for `@context` objects (default: `"false"`)
- `extendedSchema_cacheTTL`: Domain schema cache TTL in seconds (default: `"86400"`)
- `extendedSchema_maxCacheSize`: Max cached schemas (default: `"100"`)
- `extendedSchema_downloadTimeout`: Schema download timeout in seconds (default: `"30"`)
- `extendedSchema_allowedDomains`: Comma-separated domain whitelist (empty = all allowed)

---

#### 6. Sign Validator Plugin

**Purpose**: Validate Ed25519 digital signatures on incoming requests.

```yaml
signValidator:
  id: signvalidator
```

**Parameters**: None required. Uses key manager for public key lookup.

---

#### 7. Router Plugin

**Purpose**: Determine routing destination based on rules.

```yaml
router:
  id: router
  config:
    routingConfig: ./config/local-routing.yaml
```

**Or for production:**

```yaml
router:
  id: router
  config:
    routingConfigPath: /mnt/gcs/configs/bapTxnCaller-routing.yaml
```

**Parameters**:
- `routingConfig` or `routingConfigPath`: Path to routing rules YAML file

---

#### 8. Signer Plugin

**Purpose**: Sign outgoing requests with Ed25519 signature.

```yaml
signer:
  id: signer
```

**Parameters**: None required. Uses key manager for private key.

---

#### 9. Publisher Plugin

**Purpose**: Publish messages to RabbitMQ or Pub/Sub for asynchronous processing.

```yaml
publisher:
  id: publisher
  config:
    project: ${projectID}
    topic: bapNetworkReciever
```

**Parameters**:
- `project`: GCP project ID for Pub/Sub
- `topic`: Pub/Sub topic name

---

#### 10. Middleware Plugin

**Purpose**: Request preprocessing like UUID generation and header manipulation.

```yaml
middleware:
  - id: reqpreprocessor
    config:
      role: bap
      contextKeys: transaction_id,message_id,subscriber_id,module_id
```

**Parameters**:
- `contextKeys`: Comma-separated list of fields to auto-generate UUIDs for if missing
- `role`: BAP or BPP role for request processing

---

#### 11. Reqmapper Plugin

**Purpose**: Transform Beckn payloads between protocol versions or shapes using JSONata before the request continues through the handler. Mount it inside the `middleware` list wherever translation is required.

```yaml
middleware:
  - id: reqmapper
    config:
      role: bap               # Use `bpp` when running inside a BPP handler
      mappingsFile: ./config/mappings.yaml
```

**Parameters**:
- `role`: Required. Determines which JSONata expression is evaluated (`bapMappings` or `bppMappings`) for the current action.
- `mappingsFile`: Required. Absolute or relative path to a YAML file that contains the JSONata expressions for every action.

**Mapping file structure**:
```yaml
mappings:
  <action-name>:
    bapMappings: |
      # JSONata expression applied when `role: bap`
    bppMappings: |
      # JSONata expression applied when `role: bpp`
```
Each action entry is optional—if no mapping exists for the current action, the original request body is passed through unchanged. JSONata expressions receive the entire Beckn request as input (`$`) and must return the full payload that should replace it.

**Sample mapping file**:
```yaml
mappings:
  search:
    bapMappings: |
      {
        "context": {
          "action": "discover",
          "version": "2.0.0",
          "domain": "beckn.one:retail",
          "bap_id": $.context.bap_id,
          "bap_uri": $.context.bap_uri,
          "transaction_id": $.context.transaction_id,
          "message_id": $.context.message_id,
          "timestamp": $.context.timestamp
        },
        "message": {
          "filters": $.message.intent.category ? {
            "type": "jsonpath",
            "expression": "$[?(@.category.code == '" & $.message.intent.category.descriptor.code & "')]"
          } : null
        }
      }
    bppMappings: |
      {
        "context": {
          "action": "search",
          "version": "1.1.0",
          "domain": "retail",
          "bap_id": $.context.bap_id,
          "bap_uri": $.context.bap_uri,
          "transaction_id": $.context.transaction_id,
          "message_id": $.context.message_id,
          "timestamp": $.context.timestamp
        },
        "message": {
          "intent": {
            "category": $.message.filters ? {
              "descriptor": {
                "code": $substringAfter($substringBefore($.message.filters.expression, "'"), "== '")
              }
            } : null
          }
        }
      }
  on_search:
    bapMappings: |
      {
        "context": $.context,
        "message": {
          "catalog": {
            "descriptor": $.message.catalogs[0]."beckn:descriptor" ? {
              "name": $.message.catalogs[0]."beckn:descriptor"."schema:name"
            } : null
          }
        }
      }
    bppMappings: |
      {
        "context": $.context,
        "message": {
          "catalogs": [{
            "@type": "beckn:Catalog",
            "beckn:items": $.message.catalog.providers[].items[].
              {
                "@type": "beckn:Item",
                "beckn:id": id
              }
          }]
        }
      }
```
The sample illustrates how a single mapping file can convert `search` requests and `on_search` responses between Beckn 1.1.0 (BAP) and Beckn 2.0.0 (BPP) payload shapes. You can define as many action entries as needed, and the plugin will compile and cache the JSONata expressions on startup.

---

## Routing Configuration

### Routing Rules File Structure

Routing configuration is stored in separate YAML files referenced by the router plugin.

```yaml
routingRules:
  - domain: "retail:1.1.0"
    version: "1.1.0"
    targetType: "url"
    target:
      url: "http://localhost:9001/beckn"
      excludeAction: false
    endpoints:
      - search
      - select
      - init
      - confirm
```

### Routing Rule Parameters

#### `domain`
**Type**: `string`  
**Required**: Conditional (Required for v1.x.x, Optional for v2.x.x)  
**Description**: Beckn domain identifier (e.g., `retail:1.1.0`, `ONDC:TRV10`, `nic2004:60221`)

**Version-Specific Behavior**:
- **Beckn Protocol v1.x.x**: Domain is **required**. Each rule must specify a domain, and routing uses domain as a key.
- **Beckn Protocol v2.x.x**: Domain is **optional** and ignored during routing. If provided, a warning is logged. All v2 rules are domain-agnostic.
- **Conflict Detection**: For v2, multiple rules with the same version and endpoint (regardless of domain) will cause a configuration error.

**Examples**:
```yaml
# Valid v1 rule - domain required
- domain: "ONDC:TRV10"
  version: "1.1.0"
  targetType: "url"
  target:
    url: "http://backend:3000"
  endpoints:
    - search

# Valid v2 rule - domain optional (omitted)
- version: "2.0.0"
  targetType: "url"
  target:
    url: "http://backend:3000"
  endpoints:
    - search

# Valid v2 rule - domain provided (warning logged, but ignored)
- domain: "ONDC:TRV10"
  version: "2.0.0"
  targetType: "url"
  target:
    url: "http://backend:3000"
  endpoints:
    - search
```

#### `version`
**Type**: `string`  
**Required**: Yes  
**Description**: Protocol version for this domain

#### `targetType`
**Type**: `string`  
**Required**: Yes  
**Options**: `url`, `bpp`, `bap`, `msgq`  
**Description**: Type of routing destination

##### Target Types Explained:

1. **`url`**: Route to a specific URL
   ```yaml
   targetType: "url"
   target:
     url: "http://backend-service:3000/api"
     excludeAction: false  # If true, don't append endpoint to URL
   ```

2. **`bpp`**: Route to BPP specified in request's `bpp_uri`
   ```yaml
   targetType: "bpp"
   target:
     url: "https://gateway.example.com"  # Optional fallback URL
   endpoints:
     - search
   ```

3. **`bap`**: Route to BAP specified in request's `bap_uri`
   ```yaml
   targetType: "bap"
   endpoints:
     - on_search
     - on_select
   ```

4. **`msgq`**: Route to message queue (Pub/Sub)
   ```yaml
   targetType: "msgq"
   target:
     topic_id: "search_requests"
   ```

#### `target`
**Type**: `object`  
**Required**: Depends on `targetType`  
**Description**: Target destination details

##### `target.url`
**Type**: `string`  
**Description**: Target URL for `url` type, or fallback URL for `bpp`/`bap` types

##### `target.excludeAction`
**Type**: `boolean`  
**Default**: `false`  
**Description**: For `url` type, whether to exclude appending endpoint name to URL path

##### `target.topic_id`
**Type**: `string`  
**Description**: Pub/Sub topic ID for `msgq` type

##### `target.publisherId`
**Type**: `string`  
**Description**: Publisher ID for `publisher` type (deprecated in favor of `msgq`)

#### `endpoints`
**Type**: `array` of `string`  
**Required**: Yes  
**Description**: List of Beckn protocol endpoints this rule applies to

**Common Endpoints**:
- BAP Caller: `search`, `select`, `init`, `confirm`, `status`, `track`, `cancel`, `update`, `rating`, `support`
- BPP Caller: `on_search`, `on_select`, `on_init`, `on_confirm`, `on_status`, `on_track`, `on_cancel`, `on_update`, `on_rating`, `on_support`

### Routing Configuration Examples

#### Example 1: Simple URL Routing

```yaml
routingRules:
  - domain: "retail:1.1.0"
    version: "1.1.0"
    targetType: "url"
    target:
      url: "http://backend-service:3000/retail/v1"
    endpoints:
      - search
      - select
      - init
```

**Behavior**: All `search`, `select`, and `init` requests for `retail:1.1.0` will be routed to:
- `http://backend-service:3000/retail/v1/search`
- `http://backend-service:3000/retail/v1/select`
- `http://backend-service:3000/retail/v1/init`

#### Example 2: BPP Dynamic Routing

```yaml
routingRules:
  - domain: "ONDC:TRV10"
    version: "2.0.0"
    targetType: "bpp"
    target:
      url: "https://gateway.example.com"
    endpoints:
      - search
  
  - domain: "ONDC:TRV10"
    version: "2.0.0"
    targetType: "bpp"
    endpoints:
      - select
      - init
      - confirm
```

**Behavior**: 
- For `search`: Route to gateway URL if `bpp_uri` is missing from request
- For other endpoints: Route to `bpp_uri` from request context (required)

#### Example 3: Mixed Routing

```yaml
routingRules:
  - domain: "ONDC:TRV10"
    version: "2.0.0"
    targetType: "url"
    target:
      url: "https://services-backend/trv/v1"
    endpoints:
      - select
      - init
      - confirm

  - domain: "ONDC:TRV10"
    version: "2.0.0"
    targetType: "msgq"
    target:
      topic_id: "trv_search_requests"
    endpoints:
      - search
```

**Behavior**:
- `select`, `init`, `confirm`: Routed to backend URL
- `search`: Published to Pub/Sub topic for asynchronous processing

#### Example 4: URL with excludeAction

```yaml
routingRules:
  - domain: "retail:1.1.0"
    version: "1.1.0"
    targetType: "url"
    target:
      url: "http://backend:3000/webhook"
      excludeAction: true
    endpoints:
      - search
      - select
```

**Behavior**: All endpoints route to exactly `http://backend:3000/webhook` without appending the endpoint name.

#### Example 5: Beckn Protocol v2 Domain-Agnostic Routing

```yaml
routingRules:
  # v2 rule without domain (recommended)
  - version: "2.0.0"
    targetType: "url"
    target:
      url: "https://gateway.example.com/v2"
    endpoints:
      - search
      - select
      - init
      - confirm

  # v1 rules still require domain
  - domain: "ONDC:TRV10"
    version: "1.1.0"
    targetType: "url"
    target:
      url: "https://gateway.example.com/v1/trv"
    endpoints:
      - search
```

**Behavior**: 
- v2 requests (version `2.0.0`) route to gateway regardless of domain in request
- v1 requests (version `1.1.0`) route based on domain matching
- Domain field is ignored for v2 routing decisions

#### Example 6: v2 Conflict Detection

```yaml
# INVALID CONFIGURATION - Will fail at startup
routingRules:
  - domain: "ONDC:TRV10"
    version: "2.0.0"
    targetType: "url"
    target:
      url: "https://backend-a.com"
    endpoints:
      - search
  
  - domain: "ONDC:TRV11"  # Different domain, but same version and endpoint
    version: "2.0.0"
    targetType: "url"
    target:
      url: "https://backend-b.com"
    endpoints:
      - search  # ERROR: Duplicate v2 rule for 'search' endpoint
```

**Error**: Configuration will fail with: `duplicate endpoint 'search' found for version 2.0.0. For v2.x.x, domain is ignored, so you can only define each endpoint once per version. Please remove the duplicate rule`

**Fix**: For v2, use a single rule per endpoint since domain is ignored:
```yaml
routingRules:
  - version: "2.0.0"
    targetType: "url"
    target:
      url: "https://unified-backend.com"
    endpoints:
      - search
```

---

## Deployment Scenarios

### 1. Local Development (Simple Mode)

**File**: `config/local-simple.yaml`

**Characteristics**:
- Uses `simplekeymanager` (no Vault required)
- Embedded Ed25519 keys
- Local Redis
- Simplified routing
- Optional metrics collection (available on separate port when enabled)

**Use Case**: Quick local development and testing

```yaml
appName: "onix-local"
log:
  level: debug
metrics:
  enabled: true
  exporterType: prometheus
  serviceName: onix-local
http:
  port: 8081
modules:
  - name: bapTxnReceiver
    handler:
      plugins:
        keyManager:
          id: simplekeymanager
          config: {}
```

**Metrics Access**: When enabled, access metrics at `http://localhost:9090/metrics` (default metrics port, configurable via `plugins.otelsetup.config.metricsPort`)

### 2. Local Development (Vault Mode)

**File**: `config/local-dev.yaml`

**Characteristics**:
- Uses `keymanager` with local Vault
- Full production-like setup
- Local Redis and Vault

**Use Case**: Testing with production key management

```yaml
modules:
  - name: bapTxnReceiver
    handler:
      plugins:
        keyManager:
          id: keymanager
          config:
            projectID: beckn-onix-local
            vaultAddr: http://localhost:8200
```

### 3. Production Combined Mode

**File**: `config/onix/adapter.yaml`

**Characteristics**:
- Handles both BAP and BPP
- GCP Secrets Manager for keys
- Production Redis
- Remote plugin loading
- Pub/Sub integration
- OpenTelemetry metrics enabled (available on separate port, default: 9090)

**Use Case**: Single deployment serving both roles

```yaml
appName: "onix-production"
log:
  level: info
  destinations:
    - type: stdout
metrics:
  enabled: true
  exporterType: prometheus
  serviceName: beckn-onix
  serviceVersion: "1.0.0"
pluginManager:
  root: /app/plugins
  remoteRoot: /mnt/gcs/plugins/plugins_bundle.zip
modules:
  - name: bapTxnReciever
    handler:
      plugins:
        keyManager:
          id: secretskeymanager
          config:
            projectID: ${projectID}
        cache:
          id: redis
          config:
            addr: 10.81.192.4:6379
        publisher:
          id: publisher
          config:
            project: ${projectID}
            topic: bapNetworkReciever
```

**Metrics Access**: 
- Prometheus scraping: `http://your-server:9090/metrics` (default metrics port, configurable via `plugins.otelsetup.config.metricsPort`)

### 4. Production BAP-Only Mode

**File**: `config/onix-bap/adapter.yaml`

**Characteristics**:
- Only BAP modules (bapTxnReceiver, bapTxnCaller)
- Dedicated BAP deployment
- Production infrastructure

**Use Case**: Separate BAP service for scalability

### 5. Production BPP-Only Mode

**File**: `config/onix-bpp/adapter.yaml`

**Characteristics**:
- Only BPP modules (bppTxnReceiver, bppTxnCaller)
- Dedicated BPP deployment
- Production infrastructure

**Use Case**: Separate BPP service for scalability

---

## Configuration Examples

### Complete BAP Receiver Configuration

```yaml
appName: "onix-bap"
log:
  level: info
  destinations:
    - type: stdout
  contextKeys:
    - transaction_id
    - message_id
    - subscriber_id

http:
  port: 8080
  timeout:
    read: 30
    write: 30
    idle: 30

pluginManager:
  root: /app/plugins

modules:
  - name: bapTxnReceiver
    path: /bap/receiver/
    handler:
      type: std
      role: bap
      httpClientConfig:
        maxIdleConns: 1000
        maxIdleConnsPerHost: 200
        idleConnTimeout: 300
