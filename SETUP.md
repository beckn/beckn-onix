# Beckn-ONIX Setup Guide

## Reading Guide

[docs.nfh.global](https://docs.nfh.global) is the primary documentation site for the NFH fabric — it covers the full network, participant roles, protocol lifecycle, and how all components work together. ONIX is the protocol adapter that any network participant (Consumer Node or Provider Node) runs to connect their application to the fabric; start there if you are new to the network.

This document covers only how to get ONIX running. Three companion documents:

- **[README.md](README.md)** — what ONIX is, its architecture, plugin inventory, and API surface
- **[CONFIG.md](CONFIG.md)** — every configuration parameter, module/handler/step/plugin concepts, and deployment scenarios
- **This document** — how to run, build, and deploy ONIX

---

## Table of Contents

1. [The Fastest Path: NFH Fabric Starter Kit](#1-the-fastest-path-nfh-fabric-starter-kit)
2. [Running ONIX with Docker](#2-running-onix-with-docker)
3. [Building Your Own ONIX Image](#3-building-your-own-onix-image)
4. [Production Setup](#4-production-setup)
5. [Observability](#5-observability)
6. [Troubleshooting](#6-troubleshooting)

---

## 1. The Fastest Path: NFH Fabric Starter Kit

The [Beckn Starter Kit](https://github.com/beckn/starter-kit) provisions a complete working network in a single command: two ONIX adapters (Consumer Node + Provider Node), sandbox applications, and the NFH fabric services that tie them together. No prior ONIX experience required.

Follow the starter kit's own README for setup instructions. Everything in this document targets operators who need to go beyond the starter kit — custom deployment topology, production hardening, or custom plugins.

---

## 2. Running ONIX with Docker

### 2.1 Using the Pre-built Image

The starter kit's docker-compose is the canonical reference for how to wire an ONIX container correctly. A minimal service definition for a Consumer Node adapter:

```yaml
services:
  # Consumer Node adapter — handles outbound calls to the network
  # and receives inbound callbacks from network participants.
  # Name this after the role it serves in your deployment.
  onix-cnode:
    image: fidedocker/onix-adapter   # use your own image tag if running a custom-built version
    container_name: onix-cnode
    platform: linux/amd64
    ports:
      - "8081:8081"
    restart: unless-stopped
    depends_on:
      redis:
        condition: service_healthy
    environment:
      REDIS_ADDR: redis:6379          # required only if the config uses the cache plugin
    volumes:
      - ../config:/app/config         # adapter reads its config from this mount
    # The --config flag is the primary entry point into all adapter behaviour:
    # which handlers run, which plugins load, which steps execute, and how
    # each plugin is configured. See CONFIG.md for the full reference.
    command: ["./server", "--config=/app/config/generic-cnode.yaml"]
    networks:
      - beckn_network
```

**Environment variables** — ONIX reads very few. Most configuration lives in the YAML config file mounted above, not in the environment:

| Variable | When required | Example |
|---|---|---|
| `REDIS_ADDR` | `cache` plugin is configured | `redis:6379` |
| `VAULT_ADDR` | `keymanager` plugin is configured | `http://vault:8200` |
| `VAULT_ROLE_ID` | `keymanager` with AppRole auth | `<role-id>` |
| `VAULT_SECRET_ID` | `keymanager` with AppRole auth | `<secret-id>` |
| `RABBITMQ_ADDR` | `publisher` plugin is configured | `rabbitmq:5672` |
| `RABBITMQ_USER` | `publisher` plugin is configured | `admin` |
| `RABBITMQ_PASS` | `publisher` plugin is configured | `admin123` |

No other environment variables are needed. The config path is always passed as the `--config` flag in the command, not as an environment variable.

Verify the adapter is up:

```bash
curl http://localhost:8081/health
# {"status":"ok","service":"beckn-adapter"}
```

Or check the container logs — the adapter logs a listening message when it is ready:

```bash
docker logs onix-cnode 2>&1 | tail -5
# ...
# {"level":"info","message":"Server listening on :8081"}
```

### 2.2 Running Multiple Handlers on One Instance

A single ONIX process can serve multiple handlers simultaneously — for example, acting as both Consumer Node and Provider Node adapter within one container, or serving two Consumer Nodes for different Beckn networks. This works like a reverse proxy: the same port serves different endpoint paths, and each path is handled by a separately configured handler.

Each handler is declared as a separate module in the config file with its own path, plugins, and processing steps:

```
http://localhost:8081/cnode/caller/    →  Consumer Node outbound handler
http://localhost:8081/cnode/receiver/  →  Consumer Node inbound handler
http://localhost:8081/pnode/caller/    →  Provider Node outbound handler
http://localhost:8081/pnode/receiver/  →  Provider Node inbound handler
```

All four paths are served by one process on one port — the path prefix determines which handler and which plugin pipeline processes the request. See [CONFIG.md](CONFIG.md) for how to declare modules and handlers in a config file.

The `config/` directory includes ready-made configurations for common topologies:

```
config/
├── local-simple.yaml        # Combined Consumer + Provider Node, dev, embedded keys
├── local-dev.yaml           # Combined Consumer + Provider Node, dev, Vault keys
├── onix/adapter.yaml        # Combined Consumer + Provider Node, production
├── onix-bap/adapter.yaml    # Consumer Node only
└── onix-bpp/adapter.yaml    # Provider Node only
```

Alternatively, run dedicated containers for each role as the starter kit does:

```yaml
services:
  onix-cnode:                          # Consumer Node adapter
    image: fidedocker/onix-adapter
    command: ["./server", "--config=/app/config/onix-bap/adapter.yaml"]
    ports:
      - "8081:8081"

  onix-pnode:                          # Provider Node adapter
    image: fidedocker/onix-adapter
    command: ["./server", "--config=/app/config/onix-bpp/adapter.yaml"]
    ports:
      - "8082:8082"
```

### 2.3 Redis: One Instance per ONIX Node

Each ONIX instance should have its own dedicated Redis rather than sharing one across multiple adapter instances.

The cache plugin keys cached responses on `message_id`. When two ONIX adapters share a Redis instance, a cached response from one node can be incorrectly served to the other, causing hard-to-diagnose correctness bugs under load.

For local development the starter kit shares a single Redis for simplicity, which is fine when load is low and collisions are unlikely. For production, give each node its own Redis:

```yaml
services:
  redis-cnode:
    image: redis:alpine
    container_name: redis-cnode

  redis-pnode:
    image: redis:alpine
    container_name: redis-pnode

  onix-cnode:
    image: fidedocker/onix-adapter
    environment:
      REDIS_ADDR: redis-cnode:6379
    depends_on:
      redis-cnode:
        condition: service_healthy

  onix-pnode:
    image: fidedocker/onix-adapter
    environment:
      REDIS_ADDR: redis-pnode:6379
    depends_on:
      redis-pnode:
        condition: service_healthy
```

---

## 3. Building Your Own ONIX Image

If neither of the following applies, use the pre-built image from section 2 — there is no benefit to building your own.

Build your own image when:

1. **You have custom plugins** — you have written a new `.so` plugin and need it bundled into the image alongside the standard set.
2. **You want to cherry-pick plugins** — the official image bundles all plugins from this repository. However, various organisations and cloud providers publish their own ONIX plugins (for managed key stores, proprietary registries, custom policy engines, etc.), and a network participant may want an image that combines a specific subset of standard plugins with one or more third-party ones, rather than carrying everything.

### Prerequisites

- **Go** — check `go.mod` at the root of this repository for the required version and install that exact version from [golang.org/dl](https://golang.org/dl/)
- Git
- Docker
- Redis — only needed at runtime if the `cache` plugin is configured

### Build Steps

**Clone and fetch dependencies:**

```bash
git clone https://github.com/beckn/beckn-onix.git
cd beckn-onix
go mod download
```

**Build plugins:**

```bash
# Build all plugins — produces .so files in plugins/
./install/build-plugins.sh
```

For the **cherry-pick** case, open `install/build-plugins.sh` and remove the `go build` lines for plugins you do not need before running it. Each plugin's build line is clearly labelled in the script.

For **custom plugins**, add your plugin's `go build -buildmode=plugin` line to the script, or run it directly:

```bash
go build -buildmode=plugin \
  -o plugins/myplugin.so \
  ./pkg/plugin/implementation/myplugin/cmd/plugin.go
```

**Build the adapter binary:**

```bash
go build -o server cmd/adapter/main.go
```

**Build and tag the Docker image:**

```bash
docker build \
  -f Dockerfile.adapter-with-plugins \
  -t my-org/beckn-onix:1.6.0 .

docker push my-org/beckn-onix:1.6.0
```

**Use your image in docker-compose** by replacing the `image:` field:

```yaml
services:
  onix-cnode:
    image: my-org/beckn-onix:1.6.0
```

---

## 4. Production Setup

### 4.1 Deployment Topology

ONIX must be reachable on the public internet for inbound calls from other Beckn network participants — that is how the protocol works. Its outbound calls to other participants also traverse the public internet. All of these messages are cryptographically signed; the signature is the trust boundary, not network isolation.

The interface between ONIX and your own application, however, should be private. Keeping it internal to your VPC removes an unnecessary exposure.

```
[NFH fabric / other Beckn participants]
              │
        (public internet)
        (signed Beckn messages)
              │
       ┌──────▼──────┐
       │ Load Balancer│  ← public IP
       └──────┬───────┘
              │ (private, within VPC)
       ┌──────▼───────┐
       │     ONIX     │
       └──┬───────┬───┘
          │       │ (private, within VPC)
       ┌──▼──┐  ┌─▼────────┐
       │Redis│  │ Your App │
       └─────┘  └──────────┘
```

ONIX, Redis, and your application all run within the same private network. ONIX's public port is exposed through a load balancer, not directly.

In a production deployment, ONIX and Redis run as containers alongside your application's own containers — either via Docker Compose on a VM, or as workloads in a Kubernetes cluster. The following two sub-sections cover both approaches.

### 4.2 Docker Compose

For VM-based deployments, extend your existing docker-compose file to include ONIX and Redis alongside your application containers. The starter kit's docker-compose is a good starting point — see the [starter kit](https://github.com/beckn/starter-kit) for a complete working example.

A production-oriented compose file adds dedicated Redis instances per node and removes dev-only services:

```yaml
services:
  redis-cnode:
    image: redis:alpine
    container_name: redis-cnode
    restart: unless-stopped
    networks:
      - beckn_network
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5

  redis-pnode:
    image: redis:alpine
    container_name: redis-pnode
    restart: unless-stopped
    networks:
      - beckn_network
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5

  onix-cnode:
    image: fidedocker/onix-adapter
    container_name: onix-cnode
    platform: linux/amd64
    restart: unless-stopped
    ports:
      - "8081:8081"
    environment:
      REDIS_ADDR: redis-cnode:6379
    volumes:
      - ./config:/app/config
    command: ["./server", "--config=/app/config/onix-bap/adapter.yaml"]
    depends_on:
      redis-cnode:
        condition: service_healthy
    networks:
      - beckn_network

  onix-pnode:
    image: fidedocker/onix-adapter
    container_name: onix-pnode
    platform: linux/amd64
    restart: unless-stopped
    ports:
      - "8082:8082"
    environment:
      REDIS_ADDR: redis-pnode:6379
    volumes:
      - ./config:/app/config
    command: ["./server", "--config=/app/config/onix-bpp/adapter.yaml"]
    depends_on:
      redis-pnode:
        condition: service_healthy
    networks:
      - beckn_network

  your-app:
    image: your-org/your-app:latest
    # ... your app's config
    networks:
      - beckn_network

networks:
  beckn_network:
    driver: bridge
```

### 4.3 Kubernetes

For Kubernetes deployments, ONIX and Redis run as workloads in the same cluster and namespace as your application.

**Namespace:**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: beckn-onix
```

**ConfigMap** (mount your adapter config):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: onix-cnode-config
  namespace: beckn-onix
data:
  adapter.yaml: |
    # your adapter config here
```

**Deployment:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: onix-cnode
  namespace: beckn-onix
spec:
  replicas: 2
  selector:
    matchLabels:
      app: onix-cnode
  template:
    metadata:
      labels:
        app: onix-cnode
    spec:
      containers:
      - name: onix-cnode
        image: fidedocker/onix-adapter
        command: ["./server", "--config=/app/config/adapter.yaml"]
        ports:
        - containerPort: 8081
        env:
        - name: REDIS_ADDR
          value: redis-cnode.beckn-onix.svc.cluster.local:6379
        # The following three vars are only needed if using the keymanager plugin (Vault-backed keys).
        # Remove them if using simplekeymanager.
        - name: VAULT_ADDR
          valueFrom:
            secretKeyRef:
              name: beckn-secrets
              key: vault-addr
        - name: VAULT_ROLE_ID
          valueFrom:
            secretKeyRef:
              name: beckn-secrets
              key: vault-role-id
        - name: VAULT_SECRET_ID
          valueFrom:
            secretKeyRef:
              name: beckn-secrets
              key: vault-secret-id
        volumeMounts:
        - name: config
          mountPath: /app/config
        livenessProbe:
          httpGet:
            path: /health
            port: 8081
          initialDelaySeconds: 15
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health
            port: 8081
          initialDelaySeconds: 5
          periodSeconds: 5
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
      volumes:
      - name: config
        configMap:
          name: onix-cnode-config
```

**Service:**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: onix-cnode-service
  namespace: beckn-onix
spec:
  selector:
    app: onix-cnode
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8081
  type: LoadBalancer
```

Repeat the Deployment and Service for `onix-pnode`, pointing at its own config and its own Redis cluster service.

### 4.4 Key Management

ONIX ships two key manager plugins. The choice is a devops decision based on your operational preferences — not driven by how many instances you run.

**`simplekeymanager`** — Ed25519 keys are embedded directly in the adapter config file. No external dependencies. Choose this when:
- You are comfortable managing key files and rotating them via a config update + restart
- You do not operate a Vault cluster and do not want to

**`keymanager` (Vault-backed)** — keys are stored in HashiCorp Vault and fetched at runtime. Choose this when:
- Your organisation already operates Vault
- You require a full audit trail on key access
- Your compliance posture mandates centralized secrets management
- You need to rotate keys without restarting the adapter

**Setting up Vault when chosen:**

```bash
# Enable the KV secrets engine
export VAULT_ADDR='https://vault.example.com:8200'
vault login token=<root-token>
vault secrets enable -path=beckn kv-v2

# Store node signing keys
vault kv put beckn/keys/node \
  private_key="$(cat node_private_key.pem)" \
  public_key="$(cat node_public_key.pem)"

# Enable AppRole auth and create a policy
vault auth enable approle
vault policy write beckn-policy - <<EOF
path "beckn/data/keys/*" {
  capabilities = ["read"]
}
EOF
vault write auth/approle/role/beckn-role \
  token_policies="beckn-policy" \
  token_ttl=1h \
  token_max_ttl=4h

# Retrieve credentials — pass these as VAULT_ROLE_ID and VAULT_SECRET_ID env vars
vault read auth/approle/role/beckn-role/role-id
vault write -f auth/approle/role/beckn-role/secret-id
```

**Key rotation:**
- `simplekeymanager`: update the key values in the config file and restart the adapter.
- `keymanager` (Vault): write a new key version with `vault kv put beckn/keys/node ...`; the adapter picks up the new version on its next key fetch without a restart.

See [CONFIG.md](CONFIG.md) for the full plugin configuration reference for both key managers.

### 4.5 Async Message Publishing with RabbitMQ

By default ONIX processes each request synchronously — it calls the upstream participant, waits for a response, and returns. For high-volume deployments, or when your application consumes Beckn callbacks from a queue rather than via a direct webhook, use the `publisher` plugin to route messages through RabbitMQ instead.

**When to use it:**
- Your application cannot guarantee low-latency webhook handling
- You need to decouple Beckn message receipt from your application's processing
- You want durable delivery and retry semantics for inbound callbacks

**Add RabbitMQ to your compose or cluster:**

```yaml
services:
  rabbitmq:
    image: rabbitmq:3-management
    container_name: rabbitmq
    ports:
      - "5672:5672"
      - "15672:15672"   # management UI at http://localhost:15672
    environment:
      RABBITMQ_DEFAULT_USER: admin
      RABBITMQ_DEFAULT_PASS: admin123
    volumes:
      - rabbitmq-data:/var/lib/rabbitmq
    networks:
      - beckn_network
```

**Create exchange and queue:**

```bash
docker exec rabbitmq rabbitmqadmin declare exchange \
  name=beckn_exchange type=topic durable=true

docker exec rabbitmq rabbitmqadmin declare queue \
  name=beckn_callbacks durable=true

docker exec rabbitmq rabbitmqadmin declare binding \
  source=beckn_exchange destination=beckn_callbacks \
  routing_key="on_*"
```

**Pass credentials to ONIX:**

```yaml
environment:
  RABBITMQ_ADDR: rabbitmq:5672
  RABBITMQ_USER: admin
  RABBITMQ_PASS: admin123
```

Wire the `publisher` plugin in your adapter config and add `publish` to the relevant handler's steps — see [CONFIG.md](CONFIG.md) for full plugin parameters.

### 4.6 Systemd Service

For bare-metal or VM deployments without Docker, run the adapter directly as a systemd service.

Create `/etc/systemd/system/beckn-onix.service`:

```ini
[Unit]
Description=Beckn ONIX Adapter
After=network.target

[Service]
Type=simple
User=beckn
WorkingDirectory=/opt/beckn-onix
EnvironmentFile=/opt/beckn-onix/.env
ExecStart=/opt/beckn-onix/server --config=/opt/beckn-onix/config/adapter.yaml
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable beckn-onix
sudo systemctl start beckn-onix
sudo systemctl status beckn-onix
```

### 4.7 Cloud Provider Integrations

Public cloud providers maintain integration plugins and deployment tooling for ONIX targeting their managed services:

- [Google Cloud Platform](https://github.com/GoogleCloudPlatform/dpi-accelerator-beckn-onix)
- [Amazon Web Services](https://github.com/beckn/onix-aws-cdk)

---

## 5. Observability

Beckn-ONIX emits metrics, distributed traces, and structured audit logs via the `otelsetup` plugin using the OpenTelemetry SDK over OTLP. A reference two-tier collector stack (node-level + network-level) with Grafana and Loki is included in `install/network-observability/`.

Full architecture, signal catalogue, audit log configuration, collector setup, and backend integration guidance:

**[pkg/plugin/implementation/otelsetup/OBSERVABILITY.md](pkg/plugin/implementation/otelsetup/OBSERVABILITY.md)**

---

## 6. Troubleshooting

### Plugin loading failures

**Error:** `failed to load plugin: plugin.Open: plugin.so: cannot open shared object file`

```bash
# Rebuild plugins from the project root
./install/build-plugins.sh

# Verify .so files are present
ls -la plugins/
```

**Error:** `plugin was built with a different version of package internal/godebugs`

The plugin was compiled with a different Go version than the server binary. Rebuild both:

```bash
go version          # confirm the active version
rm -rf plugins/*.so
./install/build-plugins.sh
go build -o server cmd/adapter/main.go
```

### Redis connection refused

**Error:** `dial tcp 127.0.0.1:6379: connect: connection refused`

```bash
# In Docker, verify the container is healthy
docker ps | grep redis
docker exec redis redis-cli ping
```

Check that `REDIS_ADDR` in the container environment matches the Redis service name and port within your docker-compose network.

### Signature validation failures

**Error:** `signature validation failed: invalid signature`

- Confirm the correct key pair is configured — the public key registered with the network registry must match the private key in `simplekeymanager` or Vault
- Check clock synchronization between nodes; the signature includes a `created` timestamp and the validator applies a clock-skew tolerance
- Confirm the `subscriber_id` and `key_id` in the config match what is registered in the network registry

### Vault authentication failures

**Error:** `vault: authentication failed` or `invalid role or secret ID`

Secret IDs from Vault's AppRole have a TTL and expire. Regenerate and update the env var:

```bash
vault write -f auth/approle/role/beckn-role/secret-id

# Verify independently
vault login -method=approle \
  role_id=$VAULT_ROLE_ID \
  secret_id=$VAULT_SECRET_ID
```

### Port already in use

**Error:** `listen tcp :8081: bind: address already in use`

```bash
lsof -i :8081
kill -9 <PID>
```

### Debug logging

```yaml
log:
  level: debug
  destinations:
    - type: stdout
```
