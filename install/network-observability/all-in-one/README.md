# All-in-One Observability Stack

Runs the complete two-tier observability stack on a single machine. Intended for **local development and demos** where both the network-level and node-level concerns live on the same host.

This is not a production deployment model. For real deployments see:
- [`../network/`](../network/) — central stack operated by the network governing entity
- [`../node/`](../node/) — per-node stack operated by each ONIX participant

## What runs

| Service | Port(s) | Role |
|---|---|---|
| `onix-bap` | 8081 | BAP adapter |
| `onix-bpp` | 8082 | BPP adapter |
| `redis` | 6379 | Shared cache |
| `otel-collector-bap` | 4317, 4318, 8889 | BAP companion collector |
| `otel-collector-bpp` | 4321, 4322, 8891 | BPP companion collector |
| `otel-collector-network` | 4319, 4320, 8890 | Network-level aggregator |
| `prometheus` | 9090 | Metrics backend |
| `jaeger` | 16686 | Trace backend — node-level (app pipeline) |
| `zipkin` | 9411 | Trace backend — network-level |
| `loki` | 3100 | Log backend |
| `grafana` | 3000 | Dashboards — admin / admin |
| `sandbox-bap` | 3001 | Mock BAP application |
| `sandbox-bpp` | 3002 | Mock BPP application |

## Start

```bash
# From the repository root
docker compose -f install/network-observability/all-in-one/docker-compose.yml up -d
```

## Stop

```bash
docker compose -f install/network-observability/all-in-one/docker-compose.yml down
# Add -v to also remove stored metrics, traces, and logs
docker compose -f install/network-observability/all-in-one/docker-compose.yml down -v
```
