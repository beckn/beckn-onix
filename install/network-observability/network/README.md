# Network-Level Observability Stack

Operated by the **network governing entity** (Network Orchestrator). Deployed once per network; receives filtered telemetry from every ONIX node's companion collector and provides a unified cross-node view.

Node operators do not run this stack. They run [`../node/`](../node/) instead and point their collectors at this host.

## What runs

| Service | Port(s) | Role |
|---|---|---|
| `otel-collector-network` | 4318 (OTLP/HTTP inbound), 8890 (Prometheus scrape) | Receives network-pipeline signals from all nodes |
| `prometheus` | 9090 | Metrics backend — scrapes `otel-collector-network` only |
| `zipkin` | 9411 | Trace backend — network-level traces with `transaction_id` → `trace_id` rewrite |
| `loki` | 3100 | Log backend — audit records from all nodes |
| `grafana` | 3000 | Network dashboards — admin / admin |

## Prerequisites

Ensure each node's companion collectors are configured to forward their network pipeline to this host on port **4318** (OTLP/HTTP). Set the `NETWORK_COLLECTOR_HOST` variable in a `.env` file or pass it on the command line.

## Start

```bash
# From the repository root
docker compose -f install/network-observability/network/docker-compose.yml up -d
```

## Stop

```bash
docker compose -f install/network-observability/network/docker-compose.yml down
docker compose -f install/network-observability/network/docker-compose.yml down -v
```
