# Beckn-ONIX Setup Guide

This comprehensive guide walks you through setting up Beckn-ONIX from development to production deployment.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Development Setup](#development-setup)
3. [Production Setup](#production-setup)
4. [Configuration Guide](#configuration-guide)
5. [External Services Setup](#external-services-setup)
6. [GUI Component Setup](#gui-component-setup)
7. [Docker Deployment](#docker-deployment)
8. [Kubernetes Deployment](#kubernetes-deployment)
9. [Service Observability](#service-ob)
10. [Testing Your Setup](#testing-your-setup)
11. [Troubleshooting](#troubleshooting)
12. [Sample Payloads](#sample-payloads)

---

## Prerequisites

### System Requirements

- **Operating System**: Linux, macOS, or Windows (with WSL2)
- **CPU**: 2+ cores recommended
- **RAM**: 4GB minimum, 8GB recommended
- **Disk Space**: 2GB free space

### Software Requirements

#### Required
- **Go 1.23+**: [Download Go](https://golang.org/dl/)
- **Git**: [Download Git](https://git-scm.com/downloads)
- **Redis 6.0+**: For caching functionality

#### Optional (for production)
- **Docker 20.10+**: For containerized deployment
- **HashiCorp Vault 1.12+**: For secrets management
- **RabbitMQ 3.10+**: For async messaging
- **Kubernetes 1.25+**: For orchestrated deployment

### Verify Installation

```bash
# Check Go version
go version
# Expected: go version go1.23.x

# Check Git
git --version

# Check Docker (optional)
docker --version

# Check Redis
redis-cli --version
```

---

## Quick Start (Recommended)

### Option 1: ONIX Adapter Only (Fastest)

For just the ONIX adapter with Redis (no Vault required):

```bash
# Clone the repository
git clone https://github.com/beckn/beckn-onix.git
cd beckn-onix/install

# Run the automated setup
chmod +x setup.sh
./setup.sh
```

This will automatically:
- Start Redis container
- Build all plugins
- Build adapter server
- Start ONIX adapter in Docker using `local-simple.yaml` (with `simplekeymanager`)
- Create .env file

**Services Started:**
- Redis: localhost:6379
- ONIX Adapter: http://localhost:8081

**Key Management:** Uses `simplekeymanager` with embedded keys - no Vault setup required!

**Note:** 
- **Schema Validation**: Extract schemas before running: `unzip schemas.zip` (required for `schemavalidator` plugin)
- **Alternative**: You can use `schemav2validator` plugin instead, which fetches schemas from a URL and doesn't require local schema extraction. See [CONFIG.md](CONFIG.md) for more configuration details.
- **Optional**: Before running the automated setup, build the adapter image and update `docker-compose-adapter.yaml` to use the correct image

```bash
# from the repository root
docker build -f Dockerfile.adapter-with-plugins -t beckn-onix:latest .
```

### Option 2: Beckn One Network Setup

For a local setup using Beckn One with DeDI-Registry and Catalog Discovery:

```bash
cd beckn-onix/install
chmod +x beckn-onix.sh
./beckn-onix.sh

# Choose option 3: "Set up a network on local machine with Beckn One (DeDI-Registry, Catalog Discovery)"
```

This will automatically:
- Install required packages (Docker, docker-compose, jq)
- Build ONIX adapter plugins
- Start Redis
- Start ONIX adapters for BAP and BPP with Beckn One configuration
- Start Sandbox containers for BAP and BPP

**Services Started:**
- Redis: localhost:6379
- ONIX Adapter (BAP): http://localhost:8081
- ONIX Adapter (BPP): http://localhost:8082
- Sandbox BAP: http://localhost:3001
- Sandbox BPP: http://localhost:3002

**Key Features:**
- Uses Beckn One (DeDI-Registry) for registry services
- Pre-configured keys in YAML configuration files
- No local registry or gateway deployment required

**Note:** 
- **Schema Validation**: Extract schemas before running: `unzip schemas.zip` (required for `schemavalidator` plugin)
- **Alternative**: You can use `schemav2validator` plugin instead, which fetches schemas from a URL and doesn't require local schema extraction. See [CONFIG.md](CONFIG.md) for more configuration details.

### Option 3: Complete Beckn Network


For a full local Beckn network with all components:

```bash
cd beckn-onix/install
chmod +x beckn-onix.sh
./beckn-onix.sh

# Choose option 4: "Set up a network on local machine with local registry and gateway (without Beckn One)"

```

This will automatically:
- Install and configure Registry service
- Install and configure Gateway service
- Create BAP Protocol Server registry entries
- Create BPP Protocol Server registry entries
- Build ONIX adapter plugins
- **Detect config file** from `docker-compose-adapter.yml`
- **Extract keys** from protocol server configs (`bap-client.yml`, `bpp-client.yml`)
- **Auto-update simplekeymanager** in the detected config file with extracted keys
- Start ONIX Adapter with Redis

**Services Started:**
- Registry: http://localhost:3030
- Gateway: http://localhost:4030  
- Sandbox API: http://localhost:3000 
- ONIX Adapter: http://localhost:8081
- Redis: localhost:6379

**Note:** 
- **Schema Validation**: Extract schemas before running: `unzip schemas.zip` (required for `schemavalidator` plugin)
- **Alternative**: You can use `schemav2validator` plugin instead, which fetches schemas from a URL and doesn't require local schema extraction. See [CONFIG.md](CONFIG.md) for more configuration details.
- **Optional**: Before running the automated full-network setup, build the adapter image and update `docker-compose-adapter.yaml` to use the correct image

```bash
# from the repository root
docker build -f Dockerfile.adapter-with-plugins -t beckn-onix:latest .
```

**Intelligent Key Management:**
The script reads `docker-compose-adapter.yml` to detect which config file is being used (default: `local-simple.yaml`), extracts keys from protocol server configs, and automatically updates the `simplekeymanager` section in that config file - no manual configuration needed!

**Note:** Update `docker-compose-adapter.yml` to use the correct config file and correct image (optional):
- For combined setup (simplekeymanager): `CONFIG_FILE: "/app/config/local-simple.yaml"`
- For combined setup (keymanager with Vault): `CONFIG_FILE: "/app/config/local-dev.yaml"`
- For combined setup (production): `CONFIG_FILE: "/app/config/onix/adapter.yaml"`
- For BAP only: `CONFIG_FILE: "/app/config/onix-bap/adapter.yaml"`
- For BPP only: `CONFIG_FILE: "/app/config/onix-bpp/adapter.yaml"`

The script will automatically detect and configure whichever file you specify.

---

## Development Setup (Manual)

### Step 1: Clone the Repository

```bash
git clone https://github.com/beckn/beckn-onix.git
cd beckn-onix
```

### Step 2: Install Dependencies

```bash
# Download Go dependencies
go mod download

# Verify dependencies
go mod verify
```

### Step 3: Build the Application

```bash
# Build the main server binary
go build -o server cmd/adapter/main.go

# Make it executable
chmod +x server
```

### Step 4: Build Plugins

The application uses a plugin architecture. Build all plugins:

```bash
# Make the build script executable
chmod +x install/build-plugins.sh

# Build all plugins (run from project root)
./install/build-plugins.sh
```

This creates `.so` files in the `plugins/` directory:
- `cache.so` - Redis caching
- `router.so` - Request routing
- `signer.so` - Message signing
- `signvalidator.so` - Signature validation
- `schemavalidator.so` - JSON schema validation
- `schemav2validator.so` - OpenAPI 3.x schema validation
- `keymanager.so` - Vault integration
- `simplekeymanager.so` - Simple key management (no Vault)
- `publisher.so` - RabbitMQ publishing
- `registry.so` - Registry lookup
- `dediregistry.so` - registry type plugin for public key lookup
- `encrypter.so` / `decrypter.so` - Encryption/decryption
- `reqpreprocessor.so` - Request preprocessing

### Step 5: Setup Redis (Local)

#### Option A: Using Docker
```bash
docker run -d \
  --name redis-onix \
  -p 6379:6379 \
  redis:alpine
```

#### Option B: Native Installation

**macOS:**
```bash
brew install redis
brew services start redis
```

**Ubuntu/Debian:**
```bash
sudo apt update
sudo apt install redis-server
sudo systemctl start redis-server
sudo systemctl enable redis-server
```

**Verify Redis:**
```bash
redis-cli ping
# Expected: PONG
```

### Step 6: Setup Schemas

```bash
# Create schemas directory
mkdir -p schemas

# Extract schemas from the provided schemas.zip
unzip schemas.zip

# Create logs directory (optional)
mkdir -p logs
```

**Note:** The `schemas.zip` file is included in the repository. You must extract it before running the adapter for schema validation to work.

### Step 7: Configure for Local Development

For local development, use the existing `config/local-simple.yaml` which includes:
- All 4 modules (BAP + BPP)
- Simple key management (no Vault required)
- Redis caching
- Basic routing configuration

The file is pre-configured with embedded sample keys and ready to use for standalone testing.

**Note:** When using `beckn-onix.sh` (Option 2 in Quick Start), the script automatically extracts keys from protocol server configs and updates the `simplekeymanager` section in this file - no manual configuration needed!

### Step 8: Configuration Files Overview

The `config/` directory contains deployment configurations:

```
config/
├── local-dev.yaml                      # Local development with Vault
├── local-simple.yaml                   # Local development (no Vault)
├── local-routing.yaml                  # Local routing rules
├── local-simple-routing.yaml           # Simple routing rules
├── local-simple-routing-BAPCaller.yaml # BAP caller routing
├── local-simple-routing-BPPReceiver.yaml # BPP receiver routing
├── onix/                               # Combined BAP+BPP deployment
│   ├── adapter.yaml
│   ├── plugin.yaml
│   ├── bapTxnCaller-routing.yaml
│   ├── bapTxnReciever-routing.yaml
│   ├── bppTxnCaller-routing.yaml
│   └── bppTxnReciever-routing.yaml
├── onix-bap/                           # BAP-only deployment
│   ├── adapter.yaml
│   ├── plugin.yaml
│   ├── bapTxnCaller-routing.yaml
│   └── bapTxnReciever-routing.yaml
└── onix-bpp/                           # BPP-only deployment
    ├── adapter.yaml
    ├── plugin.yaml
    ├── bppTxnCaller-routing.yaml
    └── bppTxnReciever-routing.yaml
```

Routing configuration files for local-simple are already provided and referenced in `local-simple.yaml`.


### Step 9: Run the Application

**Choose your key management approach:**

#### Option A: Simple Key Manager (Recommended for Local Development)

**No Vault setup required!** Use this for quick local testing.

```bash
# Run with local-simple.yaml (uses simplekeymanager plugin)
./server --config=config/local-simple.yaml

# Or run with go
go run cmd/adapter/main.go --config=config/local-simple.yaml
```

The `local-simple.yaml` config uses `simplekeymanager` plugin with embedded keys - no external dependencies needed.

**With Docker:**
```bash
cd install
docker compose -f docker-compose-adapter.yml up -d onix-adapter
```

The server will start on `http://localhost:8081`

---

#### Option B: HashiCorp Vault (Production-like Setup)

**Only use this if your config has `keymanager` plugin (not `simplekeymanager`).**

Configs that require Vault:
- `config/local-dev.yaml` - uses `keymanager` plugin
- `config/onix/adapter.yaml` - uses `secretskeymanager` plugin

#### Quick Setup (Recommended)

**Note:** Make sure Redis is already running from Step 5.

```bash
# Make the script executable
chmod +x start-vault.sh

# Run the automated setup script
./start-vault.sh

# This creates a .env.vault file with your credentials
# Source it and run the server
source .env.vault && ./server --config=config/local-dev.yaml
```

That's it! The script handles everything automatically.

#### Manual Setup (Advanced)

If you prefer to set up Vault manually or need custom configuration:

```bash
# 1. Start Vault container
docker run -d \
  --name vault-dev \
  --cap-add=IPC_LOCK \
  -p 8200:8200 \
  -e 'VAULT_DEV_ROOT_TOKEN_ID=root' \
  hashicorp/vault:latest

# 2. Configure Vault (run the setup script)
chmod +x config/setup-vault.sh
./config/setup-vault.sh

# 3. Export the displayed credentials
export VAULT_ROLE_ID=<displayed-role-id>
export VAULT_SECRET_ID=<displayed-secret-id>

# 4. Run the server
./server --config=config/local-dev.yaml
```

#### What the Setup Does

- Starts Vault in development mode on port 8200
- Enables AppRole authentication
- Creates necessary policies and roles  
- Sets up the KV secrets engine at path `beckn`
- Stores sample keys for both BAP and BPP
- Generates and saves credentials to `.env.vault`

#### Accessing Vault UI

- **URL:** http://localhost:8200
- **Token:** root

#### Troubleshooting

If you get "invalid role or secret ID" error, the SECRET_ID has expired. Simply run:
```bash
./start-vault.sh
source .env.vault
```

**Alternative: Simple Docker Run Command**

```bash
# Start Vault in dev mode with initial setup
docker run -d \
  --name vault-dev \
  --cap-add=IPC_LOCK \
  -p 8200:8200 \
  -e 'VAULT_DEV_ROOT_TOKEN_ID=root' \
  -e 'VAULT_DEV_LISTEN_ADDRESS=0.0.0.0:8200' \
  hashicorp/vault:latest

# Wait for Vault to be ready
sleep 3

# Setup Vault using a single command
docker exec vault-dev sh -c "
  export VAULT_ADDR='http://127.0.0.1:8200' &&
  export VAULT_TOKEN='root' &&
  vault secrets enable -path=beckn kv-v2 &&
  vault kv put beckn/keys/bap private_key='sample_bap_private_key' public_key='sample_bap_public_key' &&
  vault kv put beckn/keys/bpp private_key='sample_bpp_private_key' public_key='sample_bpp_public_key'
"
```

**Step 9b: Set Environment Variables and Run**

```bash
# Get the AppRole credentials from Vault container logs
docker logs vault-dev | grep "VAULT_ROLE_ID\|VAULT_SECRET_ID"

# Copy the displayed credentials and export them
# They will look something like this:
export VAULT_ROLE_ID='<role-id-from-logs>'
export VAULT_SECRET_ID='<secret-id-from-logs>'

# Run the server
./server --config=config/local-dev.yaml

# Or using go run
go run cmd/adapter/main.go --config=config/local-dev.yaml
```

**Note:** The Vault address is already configured in `config/local-dev.yaml` as `http://localhost:8200`. The docker-compose automatically sets up AppRole authentication and displays the credentials in the logs.

**Alternative: Create a startup script**

Create `run-with-vault.sh`:

```bash
#!/bin/bash
# Set Vault environment variables
export VAULT_ADDR=${VAULT_ADDR:-"http://localhost:8200"}
export VAULT_TOKEN=${VAULT_TOKEN:-"root"}  # For dev mode

# Or use AppRole auth for production-like setup
# export VAULT_ROLE_ID=${VAULT_ROLE_ID:-"beckn-role-id"}
# export VAULT_SECRET_ID=${VAULT_SECRET_ID:-"beckn-secret-id"}

echo "Starting Beckn-ONIX with Vault key management..."
echo "Vault Address: $VAULT_ADDR"

# Check if Vault is accessible
if ! curl -s "$VAULT_ADDR/v1/sys/health" > /dev/null 2>&1; then
    echo "Error: Cannot reach Vault at $VAULT_ADDR"
    echo "Please start Vault first with: vault server -dev -dev-root-token-id='root'"
    exit 1
fi

# Run the server
./server --config=config/local-dev.yaml
```

Make it executable and run:
```bash
chmod +x run-with-vault.sh
./run-with-vault.sh
```

The server will start on `http://localhost:8081`

### Step 10: Verify Setup

```bash
# Check health endpoint
curl http://localhost:8081/health

# Check if modules are loaded
curl http://localhost:8081/bap/receiver/
# Expected: 404 with proper error (means module is loaded)
```

---


## Production Setup

### Integrating with Public cloud providers

Public cloud providers have built integration plugins and other tools to deploy Beckn ONIX easily. They provide specialized plugins that integrate with managed services on those platforms. Network participants can choose to consume them.

1. [Google Cloud Platform](https://github.com/GoogleCloudPlatform/dpi-accelerator-beckn-onix)
2. [Amazon Web Services](https://github.com/beckn/onix-aws-cdk)

The rest of the document focuses on how to deploy ONIX in Production with cloud agnostic plugins and tools.

### Additional Requirements for Production

1. **HashiCorp Vault** for key management
2. **RabbitMQ** for message queuing (needed for async flows)
3. **TLS certificates** for secure communication
4. **Load balancer** for high availability

### Step 1: Setup HashiCorp Vault

#### Install Vault
```bash
# Download and install
wget https://releases.hashicorp.com/vault/1.15.0/vault_1.15.0_linux_amd64.zip
unzip vault_1.15.0_linux_amd64.zip
sudo mv vault /usr/local/bin/
```

#### Configure Vault
```bash
# Start Vault in dev mode (for testing only)
vault server -dev -dev-root-token-id="root"

# In production, use proper configuration
cat > vault-config.hcl <<EOF
storage "file" {
  path = "/opt/vault/data"
}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = 0
  tls_cert_file = "/opt/vault/tls/cert.pem"
  tls_key_file  = "/opt/vault/tls/key.pem"
}

api_addr = "https://vault.example.com:8200"
cluster_addr = "https://vault.example.com:8201"
ui = true
EOF

vault server -config=vault-config.hcl
```

#### Setup Keys in Vault
```bash
# Login to Vault
export VAULT_ADDR='http://localhost:8200'
vault login token=root

# Enable KV secrets engine
vault secrets enable -path=beckn kv-v2

# Store signing keys
vault kv put beckn/keys/bap \
  private_key="$(cat bap_private_key.pem)" \
  public_key="$(cat bap_public_key.pem)"

vault kv put beckn/keys/bpp \
  private_key="$(cat bpp_private_key.pem)" \
  public_key="$(cat bpp_public_key.pem)"
```

### Step 2: Setup RabbitMQ

#### Using Docker
```bash
docker run -d \
  --name rabbitmq-onix \
  -p 5672:5672 \
  -p 15672:15672 \
  -e RABBITMQ_DEFAULT_USER=admin \
  -e RABBITMQ_DEFAULT_PASS=admin123 \
  rabbitmq:3-management
```

#### Configure Exchange and Queues
```bash
# Access RabbitMQ management UI
# http://localhost:15672 (admin/admin123)

# Or use CLI
docker exec rabbitmq-onix rabbitmqctl add_exchange beckn_exchange topic
docker exec rabbitmq-onix rabbitmqctl add_queue search_queue
docker exec rabbitmq-onix rabbitmqctl bind_queue search_queue beckn_exchange "*.search"
```

### Step 3: Production Configuration

Create `config/production.yaml`:

```yaml
appName: "beckn-onix-prod"
log:
  level: info
  destinations:
    - type: file
      config:
        filename: /var/log/beckn-onix/app.log
        maxSize: 100  # MB
        maxBackups: 10
        maxAge: 30    # days
    - type: stdout
  contextKeys:
    - transaction_id
    - message_id
    - subscriber_id
    - module_id
    - domain
    - action
http:
  port: 8080
  timeout:
    read: 60
    write: 60
    idle: 120
  tls:
    enabled: true
    certFile: /etc/ssl/certs/server.crt
    keyFile: /etc/ssl/private/server.key
pluginManager:
  root: ./plugins
modules:
  - name: bapTxnReceiver
    path: /bap/receiver/
    handler:
      type: std
      role: bap
      registryUrl: https://registry.ondc.org/lookup
      plugins:
        keyManager:
          id: keymanager
          config:
            projectID: ${PROJECT_ID}
            vaultAddr: ${VAULT_ADDR}
            kvVersion: v2
        cache:
          id: cache
          config:
            addr: ${REDIS_ADDR}
            password: ${REDIS_PASSWORD}
            db: 0
            poolSize: 100
        schemaValidator:
          id: schemavalidator
          config:
            schemaDir: ./schemas
            strictMode: true
        signValidator:
          id: signvalidator
          config:
            publicKeyPath: beckn/keys
        router:
          id: router
          config:
            routingConfig: ./config/routing-prod.yaml
        publisher:
          id: publisher
          config:
            addr: ${RABBITMQ_ADDR}
            username: ${RABBITMQ_USER}
            password: ${RABBITMQ_PASS}
            exchange: beckn_exchange
            durable: true
            useTLS: true
        middleware:
          - id: reqpreprocessor
            config:
              uuidKeys: transaction_id,message_id
              role: bap
      steps:
        - validateSign
        - addRoute
        - validateSchema
        - cache
        - publish
  
  # Add similar configuration for other modules...
```

### Step 4: Environment Variables

Create `.env.production`:

```bash
# Vault Configuration
VAULT_ADDR=https://vault.example.com:8200
VAULT_ROLE_ID=your-role-id
VAULT_SECRET_ID=your-secret-id
PROJECT_ID=beckn-onix-prod

# Redis Configuration
REDIS_ADDR=redis.example.com:6379
REDIS_PASSWORD=strong-redis-password

# RabbitMQ Configuration
RABBITMQ_ADDR=rabbitmq.example.com:5671
RABBITMQ_USER=beckn_user
RABBITMQ_PASS=strong-rabbitmq-password

# Application Configuration
LOG_LEVEL=info
CONFIG_FILE=/app/config/production.yaml
```

### Step 5: Systemd Service (Linux)

Create `/etc/systemd/system/beckn-onix.service`:

```ini
[Unit]
Description=Beckn ONIX Adapter
After=network.target redis.service

[Service]
Type=simple
User=beckn
Group=beckn
WorkingDirectory=/opt/beckn-onix
EnvironmentFile=/opt/beckn-onix/.env.production
ExecStart=/opt/beckn-onix/server --config=${CONFIG_FILE}
Restart=always
RestartSec=10
StandardOutput=append:/var/log/beckn-onix/stdout.log
StandardError=append:/var/log/beckn-onix/stderr.log

[Install]
WantedBy=multi-user.target
```

Enable and start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable beckn-onix
sudo systemctl start beckn-onix
sudo systemctl status beckn-onix
```

---

## Configuration Guide

### Configuration Hierarchy

```
config/
├── local-dev.yaml                      # Local development with Vault
├── local-simple.yaml                   # Local development (no Vault)
├── local-routing.yaml                  # Local routing rules
├── local-simple-routing.yaml           # Simple routing rules
├── local-simple-routing-BAPCaller.yaml # BAP caller routing
├── local-simple-routing-BPPReceiver.yaml # BPP receiver routing
├── onix/                               # Combined BAP+BPP deployment
│   ├── adapter.yaml
│   ├── plugin.yaml
│   ├── bapTxnCaller-routing.yaml
│   ├── bapTxnReciever-routing.yaml
│   ├── bppTxnCaller-routing.yaml
│   └── bppTxnReciever-routing.yaml
├── onix-bap/                           # BAP-only deployment
│   ├── adapter.yaml
│   ├── plugin.yaml
│   ├── bapTxnCaller-routing.yaml
│   └── bapTxnReciever-routing.yaml
└── onix-bpp/                           # BPP-only deployment
    ├── adapter.yaml
    ├── plugin.yaml
    ├── bppTxnCaller-routing.yaml
    └── bppTxnReciever-routing.yaml
```

### Module Configuration

Each module needs:
- **name**: Unique identifier
- **path**: HTTP endpoint path
- **handler**: Processing configuration
  - **type**: Handler type (usually "std")
  - **role**: "bap" or "bpp"
  - **plugins**: Plugin configurations
  - **steps**: Processing pipeline steps

### Plugin Configuration

#### Cache Plugin (Redis)
```yaml
cache:
  id: cache
  config:
    addr: localhost:6379
    password: ""  # Optional
    db: 0
    poolSize: 50
    minIdleConns: 10
    maxRetries: 3
```

#### KeyManager Plugin (Vault)
```yaml
keyManager:
  id: keymanager
  config:
    projectID: beckn-project
    vaultAddr: http://localhost:8200
    kvVersion: v2
    mountPath: beckn
    namespace: ""  # Optional for Vault Enterprise
```

#### Publisher Plugin (RabbitMQ)
```yaml
publisher:
  id: publisher
  config:
    addr: localhost:5672
    username: guest
    password: guest
    exchange: beckn_exchange
    exchangeType: topic
    durable: true
    autoDelete: false
    useTLS: false
    tlsConfig:
      certFile: ""
      keyFile: ""
      caFile: ""
```

#### SchemaValidator Plugin
```yaml
schemaValidator:
  id: schemavalidator
  config:
    schemaDir: ./schemas
    strictMode: false
    downloadSchemas: true
    schemaURL: https://schemas.beckn.org
```

#### Schema2Validator Plugin

**Remote URL Configuration:**
```yaml
schemaValidator:
  id: schemav2validator
  config:
    type: url
    location: https://raw.githubusercontent.com/beckn/protocol-specifications-v2/refs/tags/core-v2.0.0-lts/api/v2.0.0/beckn.yaml
    cacheTTL: "3600"
```

**Local File Configuration:**
```yaml
schemaValidator:
  id: schemav2validator
  config:
    type: file
    location: ./schemas/beckn-protocol-api.yaml
    cacheTTL: "3600"
```

#### Router Plugin
```yaml
router:
  id: router
  config:
    routingConfig: ./config/routing.yaml
    defaultTimeout: 30
    retryCount: 3
    retryDelay: 1000  # milliseconds
```

### Routing Rules Configuration

**For complete routing configuration documentation including v2 domain-agnostic routing, see [CONFIG.md - Routing Configuration](CONFIG.md#routing-configuration).**

Basic example:

```yaml
routingRules:
  # v1.x.x - domain is required
  - domain: "ONDC:RET10"
    version: "1.0.0"
    targetType: "url"  # or "publisher"
    target:
      url: "https://seller.example.com/beckn"
    endpoints:
      - search
      - select
      - init
      - confirm

  # v2.x.x - domain is optional (domain-agnostic routing)
  - version: "2.0.0"
    targetType: "url"
    target:
      url: "https://seller.example.com/v2/beckn"
    endpoints:
      - search
      - select
```

**Key Points**:
- **v1.x.x**: Domain field is required and used for routing
- **v2.x.x**: Domain field is optional and ignored (domain-agnostic)
- See CONFIG.md for target types: `url`, `bpp`, `bap`, `publisher`

### Processing Steps

Available steps for configuration:
- **validateSign**: Validate incoming signatures
- **addRoute**: Determine routing based on rules
- **validateSchema**: Validate against JSON schemas
- **sign**: Sign outgoing requests
- **cache**: Cache requests/responses
- **publish**: Publish to message queue
- **encrypt**: Encrypt sensitive data
- **decrypt**: Decrypt encrypted data

---

## External Services Setup

### Redis Cluster (Production)

```yaml
# Redis cluster configuration
cache:
  id: cache
  config:
    clusterAddrs:
      - redis-node1:6379
      - redis-node2:6379
      - redis-node3:6379
    password: ${REDIS_PASSWORD}
    poolSize: 100
    readTimeout: 3s
    writeTimeout: 3s
```

### Vault High Availability

```yaml
# Vault HA configuration
keyManager:
  id: keymanager
  config:
    vaultAddrs:
      - https://vault1.example.com:8200
      - https://vault2.example.com:8200
      - https://vault3.example.com:8200
    roleID: ${VAULT_ROLE_ID}
    secretID: ${VAULT_SECRET_ID}
    renewToken: true
    tokenTTL: 3600
```

### RabbitMQ Cluster

```yaml
# RabbitMQ cluster configuration
publisher:
  id: publisher
  config:
    clusterAddrs:
      - amqp://user:pass@rabbitmq1:5672
      - amqp://user:pass@rabbitmq2:5672
      - amqp://user:pass@rabbitmq3:5672
    haPolicy: all  # all, exactly, nodes
    connectionTimeout: 10s
    channelMax: 2047
```

---

## GUI Component Setup

The GUI component provides a web interface for monitoring and management.

### Prerequisites
- Node.js 18+ and npm

### Installation

```bash
cd onix-gui/GUI

# Install dependencies
npm install

# Development mode
npm run dev

# Production build
npm run build
npm start
```

### Features
- **Dashboard**: Real-time metrics and status
- **Configuration Editor**: Visual YAML editor
- **Request Monitor**: Live request/response tracking
- **Plugin Manager**: Enable/disable plugins
- **Routing Rules**: Visual routing configuration
- **Logs Viewer**: Centralized log viewing

### Configuration

Create `onix-gui/GUI/.env.local`:

```bash
NEXT_PUBLIC_API_URL=http://localhost:8081
NEXT_PUBLIC_REFRESH_INTERVAL=5000
```

### Access
Open `http://localhost:3000` in your browser.

---

## Docker Deployment

### Build Docker Image

```bash
# Build the image
docker build -f Dockerfile.adapter -t beckn-onix:latest .

# Tag for registry (optional)
docker tag beckn-onix:latest registry.example.com/beckn-onix:v1.0.0
```

### Docker Compose Setup

**Important: Create docker.yaml config and plugins bundle first**
```bash
# Create symlink to existing config
cd config
ln -s onix/adapter.yaml docker.yaml
cd ..

# Build plugins first
./install/build-plugins.sh

# Create plugins bundle for Docker
cd plugins
zip -r plugins_bundle.zip *.so
cd ..
```

Create `docker-compose.yml`:

```yaml
version: '3.8'

services:
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    volumes:
      - redis-data:/data
    command: redis-server --appendonly yes

  vault:
    image: hashicorp/vault:latest
    ports:
      - "8200:8200"
    environment:
      VAULT_DEV_ROOT_TOKEN_ID: root
      VAULT_DEV_LISTEN_ADDRESS: 0.0.0.0:8200
    cap_add:
      - IPC_LOCK
    volumes:
      - vault-data:/vault/file

  rabbitmq:
    image: rabbitmq:3-management
    ports:
      - "5672:5672"
      - "15672:15672"
    environment:
      RABBITMQ_DEFAULT_USER: admin
      RABBITMQ_DEFAULT_PASS: admin123
    volumes:
      - rabbitmq-data:/var/lib/rabbitmq

  beckn-onix:
    image: beckn-onix:latest
    ports:
      - "8081:8081"
    depends_on:
      - redis
      - vault
      - rabbitmq
    environment:
      VAULT_ADDR: http://vault:8200
      VAULT_TOKEN: root
      REDIS_ADDR: redis:6379
      RABBITMQ_ADDR: rabbitmq:5672
      RABBITMQ_USER: admin
      RABBITMQ_PASS: admin123
    volumes:
      - ./config:/app/config
      - ./schemas:/app/schemas
      - ./plugins:/mnt/gcs/plugins
    command: ["./server", "--config=/app/config/docker.yaml"]

volumes:
  redis-data:
  vault-data:
  rabbitmq-data:
```

**Important: Build the image first**
```bash
# Build the beckn-onix image before running docker-compose
docker build -f Dockerfile.adapter -t beckn-onix:latest .

# Then run docker-compose
docker-compose up -d
```

---

## Kubernetes Deployment

### Kubernetes Manifests

Create namespace:
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: beckn-onix
```

ConfigMap for configuration:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: beckn-onix-config
  namespace: beckn-onix
data:
  adapter.yaml: |
    # Your configuration here
```

Deployment:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: beckn-onix
  namespace: beckn-onix
spec:
  replicas: 3
  selector:
    matchLabels:
      app: beckn-onix
  template:
    metadata:
      labels:
        app: beckn-onix
    spec:
      containers:
      - name: beckn-onix
        image: registry.example.com/beckn-onix:v1.0.0
        ports:
        - containerPort: 8080
        env:
        - name: VAULT_ADDR
          valueFrom:
            secretKeyRef:
              name: beckn-secrets
              key: vault-addr
        - name: REDIS_ADDR
          value: redis-service:6379
        volumeMounts:
        - name: config
          mountPath: /app/config
        - name: plugins
          mountPath: /app/plugins
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
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
          name: beckn-onix-config
      - name: plugins
        persistentVolumeClaim:
          claimName: plugins-pvc
```

Service:
```yaml
apiVersion: v1
kind: Service
metadata:
  name: beckn-onix-service
  namespace: beckn-onix
spec:
  selector:
    app: beckn-onix
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080
  type: LoadBalancer
```

### Helm Chart

For easier deployment, use the Helm chart:

```bash
# Add repo
helm repo add beckn https://charts.beckn.org

# Install
helm install beckn-onix beckn/onix \
  --namespace beckn-onix \
  --create-namespace \
  --values values.yaml
```

Example `values.yaml`:
```yaml
replicaCount: 3

image:
  repository: registry.example.com/beckn-onix
  tag: v1.0.0
  pullPolicy: IfNotPresent

service:
  type: LoadBalancer
  port: 80

ingress:
  enabled: true
  hostname: beckn-onix.example.com
  tls: true

redis:
  enabled: true
  auth:
    enabled: true
    password: secretpassword

vault:
  enabled: true
  server:
    dev:
      enabled: false
    ha:
      enabled: true
      replicas: 3

rabbitmq:
  enabled: true
  auth:
    username: beckn
    password: secretpassword

resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 250m
    memory: 256Mi

autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
```

---

## Service Observability
  - Pull-based metrics exposed via `/metrics`
  - RED metrics for every module and action (rate, errors, duration)
  - Per-step histograms with error attribution
  - Cache, routing, plugin, and business KPIs (signature/schema validations, Beckn messages)
  - Native Prometheus exporter with Grafana dashboards & alert rules (`monitoring/`)
  - Opt-in: add a `plugins.otelsetup` block in your config to wire the `otelsetup` plugin; omit it to run without metrics. Example:

    ```yaml
    plugins:
      otelsetup:
        id: otelsetup
        config:
          serviceName: "beckn-onix"
          serviceVersion: "1.0.0"
          enableMetrics: "true"
          environment: "development"
    ```
### Modular Metrics Architecture**: 
Metrics are organized by module for better maintainability:
    - OTel SDK wiring via `otelsetup` plugin
    - Step execution metrics in `telemetry` package
    - Handler metrics (signature, schema, routing) in `handler` module
    - Cache metrics in `cache` plugin
    
#### Monitoring Quick Start
```bash
./install/build-plugins.sh
go build -o beckn-adapter ./cmd/adapter
./beckn-adapter --config=config/local-simple.yaml
cd monitoring && docker-compose -f docker-compose-monitoring.yml up -d
open http://localhost:3000 # Grafana (admin/admin)
```
Resources:
- `monitoring/prometheus.yml` – scrape config
- `monitoring/prometheus-alerts.yml` – alert rules (RED, cache, step, plugin)
- `monitoring/grafana/dashboards/beckn-onix-overview.json` – curated dashboard
- `docs/METRICS_RUNBOOK.md` – runbook with PromQL recipes & troubleshooting

---

## Testing Your Setup

### Health Check

```bash
# Basic health check
curl http://localhost:8081/health

# Expected response
{
  "status": "healthy",
  "timestamp": "2024-01-15T10:30:00Z",
  "version": "1.0.0"
}
```

### Test Search Request

```bash
# Create a test search request (no Authorization header needed - ONIX auto-generates it)
curl -X POST http://localhost:8081/bap/caller/search \
  -H "Content-Type: application/json" \
  -d '{
    "context": {
      "domain": "nic2004:52110",
      "country": "IND",
      "city": "std:080",
      "action": "search",
      "version": "1.0.0",
      "bap_id": "test.bap.com",
      "bap_uri": "https://test.bap.com/beckn",
      "transaction_id": "'$(uuidgen)'",
      "message_id": "'$(uuidgen)'",
      "timestamp": "'$(date -u +"%Y-%m-%dT%H:%M:%S.000Z")'",
      "ttl": "PT30S"
    },
    "message": {
      "intent": {
        "item": {
          "descriptor": {
            "name": "coffee"
          }
        },
        "fulfillment": {
          "end": {
            "location": {
              "gps": "12.9715987,77.5945627",
              "area_code": "560001"
            }
          }
        },
        "payment": {
          "buyer_app_finder_fee_type": "percent",
          "buyer_app_finder_fee_amount": "3"
        }
      }
    }
  }'
```

### Load Testing

Use Apache Bench or similar tools:

```bash
# Install ab
sudo apt-get install apache2-utils

# Simple load test (no Authorization header needed - ONIX auto-generates it)
ab -n 1000 -c 10 -p search.json -T application/json \
  http://localhost:8081/bap/caller/search
```

### Integration Testing

Create test script `test_integration.sh`:

```bash
#!/bin/bash

# Test BAP endpoints
endpoints=(
  "search"
  "select"
  "init"
  "confirm"
  "status"
  "track"
  "cancel"
  "update"
  "rating"
  "support"
)

for endpoint in "${endpoints[@]}"; do
  echo "Testing /bap/caller/$endpoint..."
  response=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "http://localhost:8081/bap/caller/$endpoint" \
    -H "Content-Type: application/json" \
    -d @"test_payloads/${endpoint}.json")
  
  if [ "$response" -eq 200 ] || [ "$response" -eq 202 ]; then
    echo "✅ $endpoint: SUCCESS"
  else
    echo "❌ $endpoint: FAILED (HTTP $response)"
  fi
done
```

---

## Troubleshooting

### Common Issues and Solutions

#### 1. Plugin Loading Failures

**Error**: `failed to load plugin: plugin.Open: plugin.so: cannot open shared object file`

**Solution**:
```bash
# Rebuild plugins from project root
./install/build-plugins.sh

# Check plugin files exist
ls -la plugins/

# Verify plugin compatibility
go version
file plugins/cache.so
```

**Error**: `plugin was built with a different version of package internal/godebugs`

**Solution**: Plugin version mismatch - rebuild with same Go version:
```bash
# Clean and rebuild plugins
rm -rf plugins/*.so
./install/build-plugins.sh

# Ensure same Go version for server and plugins
go version
```

#### 2. Redis Connection Issues

**Error**: `dial tcp 127.0.0.1:6379: connect: connection refused`

**Solution**:
```bash
# Check Redis is running
redis-cli ping

# Start Redis if not running
sudo systemctl start redis

# Check Redis configuration
redis-cli CONFIG GET bind
redis-cli CONFIG GET protected-mode
```

#### 3. Signature Validation Failures

**Error**: `signature validation failed: invalid signature`

**Solution**:
- Verify correct key pair is being used
- Check timestamp synchronization between systems
- Ensure signature includes all required headers
- Validate Base64 encoding of signature

```bash
# Generate test keys
openssl genpkey -algorithm ed25519 -out private_key.pem
openssl pkey -in private_key.pem -pubout -out public_key.pem
```

#### 4. Schema Validation Errors

**Error**: `schema validation failed: required property 'context' not found`

**Solution**:
- Verify schema files are in correct directory
- Check schema version matches protocol version
- Validate JSON structure

```bash
# Download latest schemas
wget https://schemas.beckn.org/core/v1.0.0.zip
unzip v1.0.0.zip -d schemas/
```

#### 5. Port Already in Use

**Error**: `listen tcp :8081: bind: address already in use`

**Solution**:
```bash
# Find process using port
lsof -i :8081

# Kill process
kill -9 <PID>

# Or use different port in config
```

#### 6. Vault Authentication Issues

**Error**: `vault: authentication failed`

**Solution**:
```bash
# Check Vault status
vault status

# Verify authentication
vault login -method=approle \
  role_id=${VAULT_ROLE_ID} \
  secret_id=${VAULT_SECRET_ID}

# Check policy permissions
vault policy read beckn-policy
```

### Debug Mode

Enable debug logging:

```yaml
log:
  level: debug
  destinations:
    - type: stdout
      config:
        pretty: true
        includeCalller: true
```

Or via environment variable:
```bash
LOG_LEVEL=debug ./server --config=config/adapter.yaml
```

### Performance Tuning

#### System Limits
```bash
# Increase file descriptors
ulimit -n 65536

# Add to /etc/security/limits.conf
beckn soft nofile 65536
beckn hard nofile 65536
```

#### Go Runtime
```bash
# Set GOMAXPROCS
export GOMAXPROCS=8

# Enable profiling
./server --config=config/adapter.yaml --profile
```

#### Redis Optimization
```bash
# Redis configuration
redis-cli CONFIG SET maxclients 10000
redis-cli CONFIG SET tcp-keepalive 60
redis-cli CONFIG SET timeout 300
```

---

## Sample Payloads

### Search Request (Retail)

```json
{
  "context": {
    "domain": "nic2004:52110",
    "country": "IND",
    "city": "std:080",
    "action": "search",
    "version": "1.0.0",
    "bap_id": "buyerapp.com",
    "bap_uri": "https://buyerapp.com/beckn",
    "transaction_id": "6d5f4c3b-2a1e-4b8c-9f7d-3e2a1b5c8d9f",
    "message_id": "a9f8e7d6-c5b4-3a2e-1f0d-9e8c7b6a5d4f",
    "timestamp": "2024-01-15T10:30:00.000Z",
    "ttl": "PT30S"
  },
  "message": {
    "intent": {
      "item": {
        "descriptor": {
          "name": "Laptop"
        }
      },
      "fulfillment": {
        "type": "Delivery",
        "end": {
          "location": {
            "gps": "12.9715987,77.5945627",
            "area_code": "560001"
          }
        }
      },
      "payment": {
        "buyer_app_finder_fee_type": "percent",
        "buyer_app_finder_fee_amount": "3"
      }
    }
  }
}
```

### Select Request

```json
{
  "context": {
    "domain": "nic2004:52110",
    "country": "IND",
    "city": "std:080",
    "action": "select",
    "version": "1.0.0",
    "bap_id": "buyerapp.com",
    "bap_uri": "https://buyerapp.com/beckn",
    "bpp_id": "sellerapp.com",
    "bpp_uri": "https://sellerapp.com/beckn",
    "transaction_id": "6d5f4c3b-2a1e-4b8c-9f7d-3e2a1b5c8d9f",
    "message_id": "b8e7f6d5-c4a3-2b1e-0f9d-8e7c6b5a4d3f",
    "timestamp": "2024-01-15T10:31:00.000Z",
    "ttl": "PT30S"
  },
  "message": {
    "order": {
      "provider": {
        "id": "P1",
        "locations": [
          {
            "id": "L1"
          }
        ]
      },
      "items": [
        {
          "id": "I1",
          "quantity": {
            "count": 2
          }
        }
      ],
      "fulfillment": {
        "end": {
          "location": {
            "gps": "12.9715987,77.5945627",
            "address": {
              "door": "21A",
              "name": "ABC Apartments",
              "building": "Tower 1",
              "street": "100 Feet Road",
              "locality": "Indiranagar",
              "city": "Bengaluru",
              "state": "Karnataka",
              "country": "India",
              "area_code": "560001"
            }
          },
          "contact": {
            "phone": "9876543210",
            "email": "customer@example.com"
          }
        }
      }
    }
  }
}
```

### Init Request

```json
{
  "context": {
    "domain": "nic2004:52110",
    "country": "IND",
    "city": "std:080",
    "action": "init",
    "version": "1.0.0",
    "bap_id": "buyerapp.com",
    "bap_uri": "https://buyerapp.com/beckn",
    "bpp_id": "sellerapp.com",
    "bpp_uri": "https://sellerapp.com/beckn",
    "transaction_id": "6d5f4c3b-2a1e-4b8c-9f7d-3e2a1b5c8d9f",
    "message_id": "c7f6e5d4-b3a2-1e0f-9d8e-7c6b5a4d3e2f",
    "timestamp": "2024-01-15T10:32:00.000Z",
    "ttl": "PT30S"
  },
  "message": {
    "order": {
      "provider": {
        "id": "P1",
        "locations": [
          {
            "id": "L1"
          }
        ]
      },
      "items": [
        {
          "id": "I1",
          "quantity": {
            "count": 2
          },
          "fulfillment_id": "F1"
        }
      ],
      "billing": {
        "name": "John Doe",
        "address": {
          "door": "21A",
          "name": "ABC Apartments",
          "building": "Tower 1",
          "street": "100 Feet Road",
          "locality": "Indiranagar",
          "city": "Bengaluru",
          "state": "Karnataka",
          "country": "India",
          "area_code": "560001"
        },
        "email": "john.doe@example.com",
        "phone": "9876543210",
        "created_at": "2024-01-15T10:32:00.000Z",
        "updated_at": "2024-01-15T10:32:00.000Z"
      },
      "fulfillment": {
        "id": "F1",
        "type": "Delivery",
        "tracking": false,
        "end": {
          "location": {
            "gps": "12.9715987,77.5945627",
            "address": {
              "door": "21A",
              "name": "ABC Apartments",
              "building": "Tower 1",
              "street": "100 Feet Road",
              "locality": "Indiranagar",
              "city": "Bengaluru",
              "state": "Karnataka",
              "country": "India",
              "area_code": "560001"
            }
          },
          "contact": {
            "phone": "9876543210",
            "email": "customer@example.com"
          }
        }
      },
      "payment": {
        "type": "ON-ORDER",
        "collected_by": "BAP",
        "buyer_app_finder_fee_type": "percent",
        "buyer_app_finder_fee_amount": "3",
        "settlement_details": [
          {
            "settlement_counterparty": "seller-app",
            "settlement_phase": "sale-amount",
            "settlement_type": "neft",
            "settlement_bank_account_no": "1234567890",
            "settlement_ifsc_code": "SBIN0001234",
            "beneficiary_name": "Seller Name",
            "bank_name": "State Bank of India",
            "branch_name": "Koramangala"
          }
        ]
      }
    }
  }
}
```

### Confirm Request

```json
{
  "context": {
    "domain": "nic2004:52110",
    "country": "IND",
    "city": "std:080",
    "action": "confirm",
    "version": "1.0.0",
    "bap_id": "buyerapp.com",
    "bap_uri": "https://buyerapp.com/beckn",
    "bpp_id": "sellerapp.com",
    "bpp_uri": "https://sellerapp.com/beckn",
    "transaction_id": "6d5f4c3b-2a1e-4b8c-9f7d-3e2a1b5c8d9f",
    "message_id": "d8f7e6d5-c4b3-2a1e-0f9d-8e7c6b5a4d3f",
    "timestamp": "2024-01-15T10:33:00.000Z",
    "ttl": "PT30S"
  },
  "message": {
    "order": {
      "id": "ORDER123",
      "state": "Created",
      "provider": {
        "id": "P1",
        "locations": [
          {
            "id": "L1"
          }
        ]
      },
      "items": [
        {
          "id": "I1",
          "fulfillment_id": "F1",
          "quantity": {
            "count": 2
          }
        }
      ],
      "quote": {
        "price": {
          "currency": "INR",
          "value": "4000"
        },
        "breakup": [
          {
            "item_id": "I1",
            "item_quantity": {
              "count": 2
            },
            "title_type": "item",
            "title": "Laptop",
            "price": {
              "currency": "INR",
              "value": "3800"
            }
          },
          {
            "item_id": "F1",
            "title_type": "delivery",
            "title": "Delivery charges",
            "price": {
              "currency": "INR",
              "value": "100"
            }
          },
          {
            "item_id": "F1",
            "title_type": "packing",
            "title": "Packing charges",
            "price": {
              "currency": "INR",
              "value": "50"
            }
          },
          {
            "item_id": "I1",
            "title_type": "tax",
            "title": "Tax",
            "price": {
              "currency": "INR",
              "value": "50"
            }
          }
        ],
        "ttl": "P1D"
      },
      "payment": {
        "uri": "https://ondc.transaction.com/payment",
        "tl_method": "http/get",
        "params": {
          "transaction_id": "TXN123456",
          "amount": "4000",
          "currency": "INR"
        },
        "type": "ON-ORDER",
        "status": "PAID",
        "collected_by": "BAP",
        "buyer_app_finder_fee_type": "percent",
        "buyer_app_finder_fee_amount": "3",
        "settlement_details": [
          {
            "settlement_counterparty": "seller-app",
            "settlement_phase": "sale-amount",
            "settlement_type": "neft",
            "settlement_bank_account_no": "1234567890",
            "settlement_ifsc_code": "SBIN0001234",
            "beneficiary_name": "Seller Name",
            "bank_name": "State Bank of India",
            "branch_name": "Koramangala"
          }
        ]
      },
      "fulfillment": {
        "id": "F1",
        "type": "Delivery",
        "tracking": true,
        "start": {
          "location": {
            "id": "L1",
            "descriptor": {
              "name": "Seller Store"
            },
            "gps": "12.9715987,77.5945627",
            "address": {
              "locality": "Koramangala",
              "city": "Bengaluru",
              "area_code": "560034",
              "state": "Karnataka"
            }
          },
          "time": {
            "range": {
              "start": "2024-01-15T11:00:00.000Z",
              "end": "2024-01-15T12:00:00.000Z"
            }
          },
          "contact": {
            "phone": "9988776655",
            "email": "seller@example.com"
          }
        },
        "end": {
          "location": {
            "gps": "12.9715987,77.5945627",
            "address": {
              "door": "21A",
              "name": "ABC Apartments",
              "building": "Tower 1",
              "street": "100 Feet Road",
              "locality": "Indiranagar",
              "city": "Bengaluru",
              "state": "Karnataka",
              "country": "India",
              "area_code": "560001"
            }
          },
          "time": {
            "range": {
              "start": "2024-01-15T14:00:00.000Z",
              "end": "2024-01-15T18:00:00.000Z"
            }
          },
          "person": {
            "name": "John Doe"
          },
          "contact": {
            "phone": "9876543210",
            "email": "customer@example.com"
          }
        }
      },
      "created_at": "2024-01-15T10:33:00.000Z",
      "updated_at": "2024-01-15T10:33:00.000Z"
    }
  }
}
```

### Authorization Header Structure

**Note:** ONIX adapter automatically generates and adds the Authorization signature header to outgoing requests. You don't need to manually create it when calling ONIX endpoints.

For reference, the Authorization header format is:

```
Authorization: Signature keyId="{subscriber_id}|{key_id}|{algorithm}",algorithm="{algorithm}",created="{created}",expires="{expires}",headers="(created) (expires) digest",signature="{base64_signature}"
```

Example of how ONIX generates it internally:
```bash
#!/bin/bash

# Variables
SUBSCRIBER_ID="buyerapp.com"
KEY_ID="key1"
ALGORITHM="ed25519"
CREATED=$(date +%s)
EXPIRES=$((CREATED + 300))
PRIVATE_KEY="path/to/private_key.pem"

# Create string to sign
STRING_TO_SIGN="(created): ${CREATED}
(expires): ${EXPIRES}
digest: SHA-256=${DIGEST}"

# Sign with Ed25519
SIGNATURE=$(echo -n "$STRING_TO_SIGN" | \
  openssl pkeyutl -sign -inkey $PRIVATE_KEY -rawin | \
  base64 -w 0)

# Create header
AUTH_HEADER="Signature keyId=\"${SUBSCRIBER_ID}|${KEY_ID}|${ALGORITHM}\",algorithm=\"${ALGORITHM}\",created=\"${CREATED}\",expires=\"${EXPIRES}\",headers=\"(created) (expires) digest\",signature=\"${SIGNATURE}\""

echo $AUTH_HEADER
```

---

## Conclusion

This setup guide covers all aspects of deploying Beckn-ONIX from local development to production. Key points:

1. **Start Simple**: Begin with local development setup
2. **Test Thoroughly**: Use provided test scripts and payloads
3. **Scale Gradually**: Move from single instance to clustered deployment
4. **Monitor Continuously**: Use logs and metrics for observability
5. **Secure Always**: Implement proper authentication and encryption

For additional help:
- Check [GitHub Issues](https://github.com/beckn/beckn-onix/issues)
- Join [Community Discussions](https://github.com/beckn/beckn-onix/discussions)
- Review [API Documentation](https://docs.beckn.org)

Happy deploying! 🚀
