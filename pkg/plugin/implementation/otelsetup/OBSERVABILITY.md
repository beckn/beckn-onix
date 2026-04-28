# Beckn-ONIX Observability

This document describes the observability architecture of Beckn-ONIX: how telemetry is produced, who consumes it, how it is collected and routed, and how to run the reference stack included in the repository.

---

## Table of Contents

1. [Two Consumers, One Signal Stream](#two-consumers-one-signal-stream)
2. [OpenTelemetry Alignment](#opentelemetry-alignment)
3. [Architecture](#architecture)
4. [OtelSetup Plugin](#otelsetup-plugin)
5. [Metrics Reference](#metrics-reference)
6. [Traces Reference](#traces-reference)
7. [Audit Logs](#audit-logs)
8. [Network Orchestrator Visibility](#network-orchestrator-visibility)
9. [OTEL Collector Configuration](#otel-collector-configuration)
10. [Reference Stack Setup](#reference-stack-setup)

---

## Two Consumers, One Signal Stream

Every adapter node in a Beckn network produces observability data that is relevant to two distinct consumers:

**The node operator** is the network participant — a BAP or BPP — running their own adapter instance. They care about the health and performance of their specific node: request rates, step latencies, error rates, cache behaviour, and the traces that help them debug their own transaction flows. They choose their own monitoring backends and dashboards.

**The network observer** is the central governing entity — a Network Orchestrator (NO) or network-level monitoring system — that watches all nodes simultaneously. They care about cross-node traffic flows, participant compliance (which plugins each node is running), and the ability to correlate a single Beckn transaction across the BAP and BPP that handled it. They operate a shared monitoring infrastructure that no individual node controls.

A single OTLP Collector deployed alongside each adapter instance serves both consumers from the same signal stream. It receives every metric, trace, and log from the adapter and routes them through two parallel pipelines:

- A **node pipeline** that sends the full signal stream to the node operator's chosen backends.
- A **network pipeline** that filters the stream down to cross-node signals and forwards them to the network-level collector, which aggregates across all nodes for the network observer.

Neither consumer requires a separate instrumentation agent, and neither sees data that belongs exclusively to the other. This design is the foundation of the architecture described in the rest of this document.

---

## OpenTelemetry Alignment

Beckn-ONIX is built on the [OpenTelemetry](https://opentelemetry.io) specification end-to-end. Every signal — metrics, traces, and logs — is emitted using the OTel Go SDK and exported over the standard OTLP protocol.

- **No vendor lock-in.** Any OTLP-compatible backend works for either consumer: open-source (Prometheus, Jaeger, Loki, Grafana, Zipkin), commercial (Datadog, New Relic, Honeycomb, Dynatrace, Elastic, Google Cloud Ops, AWS X-Ray, Azure Monitor), or any other product that speaks OTLP. The reference stack in this repository uses open-source tools purely as illustrative defaults.
- **Standard collector pipeline.** The OpenTelemetry Collector is the intermediary for both the node pipeline and the network pipeline. Filtering, batching, transformation, and fan-out to multiple backends are all done in the collector — the adapter code does not change when backends change.
- **Standard semantic conventions.** Span attributes and metric labels follow OTel semantic conventions where applicable (`http.response.status_code`, `server.address`, `user_agent.original`, `service.name`, etc.).
- **Instrumentation scope.** All adapter-originated instruments are tagged with scope `beckn-onix` (version `v2.0.0`) so they can be filtered from third-party library metrics in any backend.
- **Automatic instrumentation.** Go runtime metrics (`go_*`) are emitted automatically via `otelcontrib/instrumentation/runtime`. Redis client metrics are emitted automatically via `redisotel`. Both are included in the node pipeline.

The adapter exports over **OTLP/gRPC** by default.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│                     Beckn-ONIX Adapter  (single process)                 │
│                                                                          │
│   OtelSetup Plugin — wires MeterProvider · TracerProvider · LogProvider  │
│                                                                          │
│   Metrics ◄── handler, steps, plugins, cache, runtime, redis            │
│   Traces  ◄── request spans, step spans, cache spans, key lookup spans  │
│   Logs    ◄── structured JSON + PII-masked audit records                │
└───────────────────────────────┬──────────────────────────────────────────┘
                          OTLP/gRPC
                                │
          ┌─────────────────────▼──────────────────────┐
          │   OTLP Collector  (one per adapter node)    │
          │                                             │
          │  NODE PIPELINE          NETWORK PIPELINE    │
          │  ─────────────          ────────────────    │
          │  all metrics       →   onix_http_request_  │
          │  all traces        →   count only           │
          │  (node operator's  →   spans with           │
          │   own backends)    →   sender.id only       │
          │                    →   all audit logs       │
          └────────┬────────────────────────┬───────────┘
                   │                        │
       ┌───────────▼────────┐   ┌───────────▼──────────────────┐
       │ Node operator's    │   │ Network-level OTLP Collector  │
       │ chosen backends    │   │ (aggregates across all nodes) │
       │                    │   │                               │
       │ Any OTLP-compat.   │   │  metrics → any metrics backend│
       │ metrics backend    │   │  traces  → any trace backend  │
       │ Any OTLP-compat.   │   │  (tx_id → TraceID rewrite)   │
       │ trace backend      │   │  logs    → any log backend    │
       │ Any OTLP-compat.   │   │                               │
       │ log backend        │   │ → consumed by Network Observer│
       └────────────────────┘   └───────────────────────────────┘
```

### The companion collector

Each adapter node runs one OTLP Collector instance alongside it — the same process or container that receives the adapter's OTLP stream. This companion collector is the key architectural element: it is the single point that simultaneously serves both consumers from one signal source.

The companion collector runs parallel pipelines on the same received data:

| Pipeline group | Consumer | Signal content |
|---|---|---|
| `metrics/app`, `traces/app`, `logs/app` | Node operator | All metrics, all traces, audit logs — full fidelity |
| `metrics/network`, `traces/network`, `logs/network` | Network observer | `onix_http_request_count` only; spans that carry `sender.id`; all audit logs |

The node operator configures the app-pipeline exporters to point at their own infrastructure. The network-pipeline exporters always point at the shared network-level collector — this is the only shared endpoint between a node and the network.

### The network-level collector

The network-level collector is operated by the network governing entity. It receives the filtered network-pipeline signals from every companion collector in the network and provides the network observer with:

- A unified cross-node metrics view (traffic flows, participant counts)
- Distributed traces that stitch together the BAP and BPP sides of the same Beckn transaction
- Audit log records from all nodes in one place

---

## OtelSetup Plugin

`otelsetup` is an application-level plugin that runs once at startup before any module initialises. It wires the global OTel `MeterProvider`, `TracerProvider`, and `LoggerProvider`. All other components — handler, steps, plugins — acquire instruments from these global providers automatically.

### Config reference

```yaml
plugins:
  otelsetup:
    id: otelsetup
    config:
      # Required
      serviceName: "beckn-onix"        # OTel resource: service.name
      otlpEndpoint: "localhost:4317"   # OTLP/gRPC endpoint of the companion collector

      # Optional — defaults shown
      serviceVersion: "1.0.0"          # OTel resource: service.version
      environment: "development"       # OTel resource: environment
      domain: ""                       # OTel resource: domain (e.g. "ONDC:TRV10")
      deviceID: "beckn-onix-device"    # OTel resource: device_id

      # Signal toggles (each defaults to false)
      enableMetrics: "true"
      enableTracing: "true"
      enableLogs: "true"

      # Metrics export interval in seconds (default: 5)
      timeInterval: "5"

      # Audit log field configuration (required when enableLogs: "true")
      auditFieldsConfig: "/app/config/audit-fields.yaml"

      # Network-level resource labels — used by the network observer for attribution
      producer: "bap.example.com"      # subscriber ID of this node
      producerType: "bap"              # "bap" or "bpp"
```

### OTel resource attributes

Every OTLP signal carries these resource attributes. The `producer` and `producerType` fields are specifically used by the network observer to attribute signals to a particular network participant.

| Attribute | Config field | Notes |
|---|---|---|
| `service.name` | `serviceName` | Required |
| `service.version` | `serviceVersion` | |
| `environment` | `environment` | |
| `domain` | `domain` | |
| `device_id` | `deviceID` | |
| `producer` | `producer` | Subscriber ID — enables network-level attribution |
| `producerType` | `producerType` | `bap` or `bpp` |

### Shutdown

At `SIGINT`/`SIGTERM`, the adapter calls the `otelsetup` closer, which flushes all pending spans, metric data points, and log records before the process exits. The adapter waits up to 10 s for in-flight signals to drain.

---

## Metrics Reference

### Instrumentation scopes

| Scope | Version | Produced by |
|---|---|---|
| `beckn-onix` | `v2.0.0` | Handler, steps, plugins, cache, node-info gauge |
| `github.com/beckn-one/beckn-onix/handler` | `1.0.0` | Validation and routing counters |

---

### HTTP traffic

| Metric | Type | Unit |
|---|---|---|
| `onix_http_request_count` | Counter | `1` |

**Description:** Total HTTP requests processed by the adapter.

**Labels:**

| Label | Values / notes |
|---|---|
| `http_status_code` | `2xx`, `3xx`, `4xx`, `5xx` |
| `action` | Beckn action (`search`, `on_confirm`, …) |
| `role` | `bap` or `bpp` |
| `sender.id` | Sender subscriber ID from Beckn context |
| `recipient.id` | Recipient subscriber ID from Beckn context |
| `metric.code` | `<action>_api_total_count` |
| `metric.category` | `Discovery` for search/discovery actions; `NetworkHealth` otherwise |
| `metric.granularity` | Configurable aggregation granularity (default `10min`) |
| `metric.frequency` | Configurable reporting frequency (default `10min`) |

> **This is the primary network-observer metric.** The companion collector's network pipeline forwards only `onix_http_request_count` to the network-level collector. With `sender.id` and `recipient.id` labels, the network observer can track traffic flows between specific participants across all nodes without receiving any application-internal metrics.

---

### Processing steps

| Metric | Type | Unit | Description |
|---|---|---|---|
| `onix_step_executions_total` | Counter | `{execution}` | Total executions of each processing step |
| `onix_step_execution_duration_seconds` | Histogram | `s` | Per-step latency distribution |
| `onix_step_errors_total` | Counter | `{error}` | Step-level failures |

**Histogram buckets (`onix_step_execution_duration_seconds`):** 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5 s

**Labels:**

| Label | Description |
|---|---|
| `module` | Transaction module (`bapTxnReceiver`, `bapTxnCaller`, `bppTxnReceiver`, `bppTxnCaller`) |
| `step` | Step name (`validateSign`, `addRoute`, `validateSchema`, `sign`, `cache`, `publish`, …) |
| `action` | Beckn action |
| `error_type` | Error classification (`onix_step_errors_total` only) |

> **Node operator use.** Step metrics are internal to each node and remain in the node pipeline. They help the node operator identify which step is slow or failing.

---

### Plugins

| Metric | Type | Unit | Description |
|---|---|---|---|
| `onix_plugin_execution_duration_seconds` | Histogram | `s` | Per-plugin call latency |
| `onix_plugin_errors_total` | Counter | `{error}` | Per-plugin call failures |
| `onix_plugin_info` | Observable Gauge | `{plugin}` | Loaded plugin inventory; value is always 1 |

**Histogram buckets (`onix_plugin_execution_duration_seconds`):** 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1 s

**Labels on `onix_plugin_execution_duration_seconds` and `onix_plugin_errors_total`:**

| Label | Description |
|---|---|
| `plugin_id` | Plugin implementation ID as configured (e.g. `schemavalidator`) |
| `plugin_type` | Plugin slot type (e.g. `schema_validator`, `router`, `cache`) |
| `action` | Beckn action |

**Labels on `onix_plugin_info`:**

| Label | Description |
|---|---|
| `module` | Transaction module |
| `plugin_type` | Plugin slot type |
| `plugin_id` | Plugin implementation ID |
| `subscriber_id` | Subscriber ID from module config (omitted when not configured) |

> **`onix_plugin_info` is the network observer's node inventory signal.** At startup, each module emits one permanent gauge series per loaded plugin with value 1. From the network-level collector the observer can query which plugins are loaded on every node in the network, detect non-standard implementations, and verify compliance — without reading any node's config file.

---

### Cache

| Metric | Type | Unit | Description |
|---|---|---|---|
| `onix_cache_operations_total` | Counter | — | All cache operations (get, set, delete) |
| `onix_cache_hits_total` | Counter | — | Cache get hits |
| `onix_cache_misses_total` | Counter | — | Cache get misses |

**Labels:** `operation` (get/set/delete), `key_pattern`

---

### Protocol validation and routing

| Metric | Type | Unit | Description |
|---|---|---|---|
| `beckn_signature_validations_total` | Counter | `{validation}` | Incoming signature validation attempts |
| `beckn_schema_validations_total` | Counter | `{validation}` | JSON/OpenAPI schema validation attempts |
| `onix_routing_decisions_total` | Counter | `{decision}` | Routing decisions taken by the handler |

**Labels:**

| Metric | Labels |
|---|---|
| `beckn_signature_validations_total` | `action`, `status` |
| `beckn_schema_validations_total` | `action`, `schema_version`, `status` |
| `onix_routing_decisions_total` | `module`, `action`, `target_type`, `status` |

---

### Automatic instrumentation

The following metric groups are collected without any configuration and flow through the node pipeline:

| Group | Source | Naming prefix |
|---|---|---|
| Go runtime | `otelcontrib/instrumentation/runtime` | `go_` |
| Redis client | `redisotel` (go-redis) | `db_client_` |

These follow OTel semantic conventions.

---

## Traces Reference

All spans use instrumentation scope `beckn-onix` (v2.0.0) and are exported via OTLP/gRPC to the companion collector.

### Span hierarchy per request

```
[request]  SpanKind=Server  (module name, e.g. "bapTxnReceiver")
  ├── [validate-sign]
  │     └── [keyset]                   key retrieval
  │           ├── [redis lookup]       if cached in Redis
  │           └── [registry lookup]   if fetched from Beckn registry
  ├── [<step-name>]                    one child span per configured step
  ├── [sign]
  │     └── [keyset]
  └── [redis_get / redis_set / redis_delete]   cache plugin operations
```

### Request span attributes

**Name:** module name  **Kind:** `SpanKindServer`

| Attribute | Description | Consumer |
|---|---|---|
| `transaction_id` | Beckn `context.transactionId` | Both |
| `message_id` | Beckn `context.messageId` | Both |
| `sender.id` | Sender subscriber ID | Network observer (used by network pipeline filter) |
| `receiver.id` | Receiver subscriber ID | Both |
| `parent_id` | Adapter parent ID (role + subscriberID + pod name) | Node operator |
| `server.address` | HTTP `Host` header | Node operator |
| `user_agent.original` | Request `User-Agent` | Node operator |
| `http.response.status_code` | Final HTTP status code | Both |
| `http.request.error` | Error string if request failed | Node operator |
| `observedTimeUnixNano` | Nanosecond timestamp of response write | Node operator |

The span status is set to `Error` when the HTTP status code is outside 2xx.

### Step spans

**Name:** step name (`validate-sign`, `sign`, `addRoute`, or custom step name)

Each step executes as a child span of the request span. Step metrics (`onix_step_execution_duration_seconds`) are recorded using the same span context so traces and metrics can be correlated.

### Key management spans

| Span name | Description |
|---|---|
| `keyset` | Top-level key retrieval, parent of lookup spans |
| `redis lookup` | Key lookup in Redis cache |
| `registry lookup` | Key lookup via Beckn registry |

### Cache spans

| Span name | Description |
|---|---|
| `redis_get` | Cache read |
| `redis_set` | Cache write |
| `redis_delete` | Cache delete |

### Trace context propagation

The adapter propagates OTel trace context using **W3C TraceContext headers** (`traceparent`, `tracestate`) on all outgoing HTTP requests and reads them from all incoming requests. This allows the node operator to see end-to-end traces that span their backend services.

### Trace correlation in the network pipeline

The companion collector's `traces/network` pipeline filters to spans that have a `sender.id` attribute set — these are the request-level spans that represent inter-participant traffic. The network-level collector then rewrites the `trace_id` to match the Beckn `transaction_id` (stripping hyphens to produce a valid 32-hex TraceID). The network observer can retrieve a single Beckn transaction in any trace backend using only its `transaction_id`, and all spans from the BAP and BPP sides appear under the same trace.

`message_id` is not mapped to `span_id` at the network level because multiple nodes emit distinct spans for the same message; duplicate span IDs would corrupt the trace. `message_id` is available as a span attribute for tag-based search.

### Structured log correlation

When a valid span context is active, every structured log entry automatically includes `trace_id` and `span_id` fields. Log backends that support trace correlation (e.g. Loki with Grafana) can link log lines directly to the relevant span in the trace backend.

---

## Audit Logs

When `enableLogs: "true"` and `auditFieldsConfig` are set, the adapter emits one structured audit record per request via the OTel log SDK. Audit records flow through the `logs/network` pipeline to the network-level log backend, giving the network observer a tamper-evident, PII-safe record of every Beckn transaction that crossed the network.

### Fixed attributes on every audit record

These fields are always present regardless of mode or field selection:

| Attribute | Description |
|---|---|
| `checkSum` | SHA-256 of the raw request body before any processing |
| `log_uuid` | Unique ID for this log record |
| `transaction_id` | Beckn `context.transactionId` |
| `message_id` | Beckn `context.messageId` |
| `parent_id` | Adapter parent ID |
| `http.response.status_code` | HTTP status of the response |
| `http.request.error` | Error string if the request failed |
| `sender.id` | Sender subscriber ID |
| `receiver.id` | Receiver subscriber ID |

### Audit pipeline: modes and masking

The audit configuration file (`config/audit-fields.yaml`) controls a single-pass pipeline applied to every payload before the record is emitted:

**Stage 1 — key-name masking (`maskRules`):** Fields are masked wherever they appear in the payload, regardless of nesting depth, by matching field name. This makes rules portable across Beckn domains, actions, and schema versions.

**Stage 2 — path-override masking (`pathOverrides`):** Field masking bound to a specific dot-path. Use sparingly — key-name masking is preferred because it is schema-agnostic.

**Stage 3 — field selection:** Only active in `selective` mode. The audit body is trimmed to the paths listed in `selectedFields` for the current action.

**Modes:**

| Mode | Behaviour |
|---|---|
| `full` | Emit the entire payload with PII masking applied |
| `selective` | Emit only the fields listed in `selectedFields` for the action, then apply masking |

### Audit fields configuration (`config/audit-fields.yaml`)

```yaml
mode: full   # "full" or "selective"

# Named masking patterns
patterns:
  email:
    maskType: replace
    mask: "***@***.***"
  phone:
    maskType: last4      # retains last 4 characters
  sensitive:
    maskType: replace
    mask: "[REDACTED]"
  account:
    maskType: last4

# Key-name masking — applied wherever the key appears in the payload
maskRules:
  - keys: [email, emailAddress, supportEmail, providerEmail]
    pattern: email
  - keys: [phone, telephone, mobile, supportPhone, providerPhone]
    pattern: phone
  - keys: [displayName, fullName, accountHolderName]
    pattern: sensitive
  - keys: [accountNumber, vpa]
    pattern: account
  - keys: [paymentURL, txnRef, paidAt]
    pattern: sensitive

# Path-override masking — use sparingly, prefer maskRules
pathOverrides: []

# Field selection — only used when mode: selective
# "default" is the fallback for any action not listed
selectedFields:
  default:
    - context.transactionId
    - context.messageId
    - context.action
    - context.domain
    - context.bapId
    - context.bppId

  confirm:
    - context.transactionId
    - context.messageId
    - context.action
    - context.domain
    - context.bapId
    - context.bppId
    - message.order.id
    - message.order.status
    - message.order.payment.status

  init:
    - context.transactionId
    - context.messageId
    - context.action
    - context.domain
    - context.bapId
    - context.bppId
    - message.order.id
    - message.order.value

  update:
    - context.transactionId
    - context.messageId
    - context.action
    - context.domain
    - message.order.id
    - message.order.status
```

### Loki label mapping (reference stack)

The reference Loki configuration maps OTel resource attributes to stream labels so records can be indexed and queried:

| OTel resource attribute | Loki stream label |
|---|---|
| `service.name` | `service_name` |
| `environment` | `environment` |
| `eid` | `eid` (signal type: AUDIT / METRIC / API) |

---

## Network Orchestrator Visibility

The network observer's view is assembled from three signals that the architecture deliberately surfaces at the network level:

### 1. Plugin inventory — `onix_plugin_info`

At startup, each module calls `telemetry.RegisterPluginInfo` after all plugins are initialised. This registers a permanent observable gauge that emits one time series per loaded plugin with value 1 and flows through the standard node metrics pipeline to any Prometheus-compatible backend the network observer queries.

```
onix_plugin_info{
  module="bapTxnReceiver",
  plugin_type="schema_validator",
  plugin_id="schemavalidator",
  subscriber_id="bap.example.com"
} 1

onix_plugin_info{
  module="bapTxnReceiver",
  plugin_type="router",
  plugin_id="router",
  subscriber_id="bap.example.com"
} 1
```

`subscriber_id` is only included when `handler.subscriberId` is set in the module config — this is recommended for all production deployments so the network observer can attribute inventory to specific participants.

The network observer can query this gauge to answer: Which modules are active on each node? Which plugin implementations are loaded? Are any nodes running non-standard (custom) plugins? Which nodes share the same subscriber?

### 2. Traffic flows — `onix_http_request_count`

Forwarded to the network-level collector via the `metrics/network` pipeline. With `sender.id` and `recipient.id` labels, the network observer can track Beckn traffic flows between specific participants across all nodes in aggregate.

### 3. Transaction traces — distributed trace stitching

Spans forwarded via the `traces/network` pipeline, combined with the `transaction_id` → `trace_id` rewrite in the network-level collector, give the network observer end-to-end traces across BAP and BPP nodes for any Beckn transaction — even though those nodes are operated by different participants.

---

## OTEL Collector Configuration

The repository includes three pre-configured collector configs under `install/network-observability/`. The backend exporters in these configs are reference defaults and can be replaced with any OTLP-compatible product.

### Companion collector (BAP / BPP)

One instance alongside each adapter. Receives all signals on OTLP/gRPC `:4317`.

**Node pipeline** — full fidelity to the node operator's backends:

```yaml
metrics/app:
  receivers: [otlp]
  processors: [batch]
  exporters: [<node-operator metrics backend>]

traces/app:
  receivers: [otlp]
  processors: [batch/traces]
  exporters: [<node-operator trace backend>]
```

**Network pipeline** — filtered signals forwarded to the network-level collector:

```yaml
metrics/network:
  receivers: [otlp]
  processors: [filter/network_metrics, batch]
  exporters: [otlp_http/collector-network]

traces/network:
  receivers: [otlp]
  processors: [filter/network_traces, batch/traces]
  exporters: [otlp_http/collector-network]

logs/network:
  receivers: [otlp]
  processors: [batch]
  exporters: [otlp_http/collector-network]
```

**`filter/network_metrics`** — passes only `onix_http_request_count`:
```yaml
filter/network_metrics:
  error_mode: ignore
  metrics:
    metric:
      - 'name != "onix_http_request_count"'
```

**`filter/network_traces`** — passes only spans that carry `sender.id` (inter-participant spans):
```yaml
filter/network_traces:
  error_mode: ignore
  traces:
    span:
      - 'attributes["sender.id"] == nil'
```

### Network-level collector

Operated by the network governing entity. Receives filtered signals from all companion collectors over OTLP/HTTP `:4318`.

**`transform/beckn_ids`** — rewrites `trace_id` from `transaction_id` so cross-node traces are queryable by Beckn transaction ID:
```yaml
transform/beckn_ids:
  error_mode: ignore
  trace_statements:
    - set(span.attributes["_beckn_tx"], span.attributes["transaction_id"])
        where span.attributes["transaction_id"] != nil
    - replace_pattern(span.attributes["_beckn_tx"], "-", "")
        where span.attributes["_beckn_tx"] != nil
    - set(span.trace_id, TraceID(span.attributes["_beckn_tx"]))
        where span.attributes["_beckn_tx"] != nil
```

Pipelines:
```yaml
metrics:   receivers: [otlp]  processors: [batch]                 exporters: [<network metrics backend>]
traces:    receivers: [otlp]  processors: [transform/beckn_ids, batch]  exporters: [<network trace backend>]
logs:      receivers: [otlp]  processors: [batch]                 exporters: [<network log backend>]
```

### Companion collector ports (reference stack)

| Collector | OTLP gRPC | OTLP HTTP | Prometheus scrape |
|---|---|---|---|
| `otel-collector-bap` | 4317 | 4318 | 8889 |
| `otel-collector-bpp` | 4321 | 4322 | 8891 |
| `otel-collector-network` | 4319 | 4320 | 8890 |

---

## Reference Stack Setup

`install/network-observability/` contains a Docker Compose file that brings up a complete, runnable two-tier observability stack. The backend services used (Prometheus, Jaeger, Loki, Grafana, Zipkin) are open-source reference implementations. Any of them can be replaced by editing only the exporter block in the relevant collector config — the adapter does not change.

### Prerequisites

```bash
# Build the adapter image (from repo root)
docker build -f Dockerfile.adapter-with-plugins -t beckn-onix:latest .

# Extract Beckn v1 schemas (required for the schemavalidator plugin)
unzip schemas.zip
```

### Start the stack

```bash
# Run from the repository root
docker compose -f install/network-observability/docker-compose.yml up -d
```

### Services started

| Service | Port(s) | Role |
|---|---|---|
| `onix-bap` | 8081 | BAP adapter (`config/local-beckn-one-bap.yaml`) |
| `onix-bpp` | 8082 | BPP adapter (`config/local-beckn-one-bpp.yaml`) |
| `redis` | 6379 | Caching backend |
| `otel-collector-bap` | 4317, 4318, 8889 | BAP companion collector |
| `otel-collector-bpp` | 4321, 4322, 8891 | BPP companion collector |
| `otel-collector-network` | 4319, 4320, 8890 | Network-level aggregator |
| `prometheus` | 9090 | Reference metrics backend |
| `jaeger` | 16686 | Reference trace backend — node-level (app pipeline) |
| `zipkin` | 9411 | Reference trace backend — network-level |
| `loki` | 3100 | Reference log backend |
| `grafana` | 3000 | Reference dashboards — admin / admin |
| `sandbox-bap` | 3001 | Beckn BAP test sandbox |
| `sandbox-bpp` | 3002 | Beckn BPP test sandbox |

### Network topology

Two Docker networks:

- **`beckn_network`** — adapter-to-adapter and adapter-to-Redis/sandbox traffic
- **`observability`** — all telemetry traffic between adapters, collectors, and backends

Adapter containers are on both networks. All observability services are on `observability` only.

### Replacing a backend

To send traces to a different backend, change only the exporter in the collector config. Example — replacing Jaeger with Honeycomb for the node operator trace pipeline:

```yaml
# install/network-observability/otel-collector-bap/config.yaml
exporters:
  otlp/honeycomb:
    endpoint: api.honeycomb.io:443
    headers:
      x-honeycomb-team: ${HONEYCOMB_API_KEY}

service:
  pipelines:
    traces/app:
      receivers: [otlp]
      processors: [batch/traces]
      exporters: [otlp/honeycomb]   # replaces otlp_grpc/jaeger
```

No changes to the adapter, the network pipeline, or the network-level collector are needed.

### Stopping the stack

```bash
docker compose -f install/network-observability/docker-compose.yml down

# Include -v to also remove stored metrics, traces, and logs
docker compose -f install/network-observability/docker-compose.yml down -v
```
