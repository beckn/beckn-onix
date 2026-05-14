# Node-Level Stack

Deployed by each **ONIX network participant** (BAP or BPP operator). Runs the adapters and their companion otel-collectors. The collectors forward network-pipeline signals to the central network-level collector operated by the network governing entity.

## Configuration

Set the network collector endpoint before starting. Create a `.env` file or export the variable:

```bash
# Address of the network-level otel-collector (operated by the network governing entity)
NETWORK_COLLECTOR_ENDPOINT=http://<network-host>:4318
```

## Modes

### Default — network forwarding only

Otel-collectors forward only the network pipeline (filtered metrics, cross-node traces, audit logs) to the central collector. No local telemetry backends are started.

```bash
docker compose up -d
```

### With node-level telemetry

Adds local Jaeger, Prometheus, and Grafana so the node operator can inspect their own adapter's full signal stream. Overrides the otel-collector configs to enable the app-level pipelines in addition to the network pipeline.

```bash
docker compose -f docker-compose.yml -f docker-compose.with-telemetry.yml up -d
```

| Service added | Port | Role |
|---|---|---|
| `prometheus-node` | 9090 | Node-level metrics |
| `jaeger` | 16686 | Node-level traces (app pipeline) |
| `grafana-node` | 3000 | Node dashboards — admin / admin |

## What always runs

| Service | Port(s) | Role |
|---|---|---|
| `redis` | 6379 | Shared cache for adapters |
| `onix-bap` | 8081 | BAP adapter |
| `onix-bpp` | 8082 | BPP adapter |
| `otel-collector-bap` | 4317, 4318 | BAP companion collector |
| `otel-collector-bpp` | 4321, 4322 | BPP companion collector |

## Attaching to an existing devkit

The stack joins an **external** `beckn_network`. If you are running a devkit that already created this network, bring up this stack afterwards and the containers will share it automatically.

The adapter config (e.g. `generic-bap.yaml`) must include the `otelsetup` plugin pointing to the companion collector:

```yaml
plugins:
  otelsetup:
    id: otelsetup
    config:
      serviceName: "beckn-onix"
      otlpEndpoint: "otel-collector-bap:4317"
      enableMetrics: "true"
      enableTracing: "true"
      enableLogs: "true"
      producer: "bap.example.com"
      producerType: "bap"
      auditFieldsConfig: "/app/config/audit-fields.yaml"
```

## Stop

```bash
docker compose down
# or, if started with telemetry overlay:
docker compose -f docker-compose.yml -f docker-compose.with-telemetry.yml down
```
