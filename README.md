# Beckn-ONIX

<div align="center">

[![Go Version](https://img.shields.io/badge/Go-1.23-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-green.svg)](LICENSE)
[![CI Status](https://img.shields.io/github/actions/workflow/status/beckn/beckn-onix/ci.yml)](https://github.com/beckn/beckn-onix/actions)
[![Coverage](https://img.shields.io/badge/Coverage-90%25-brightgreen.svg)](https://codecov.io/gh/beckn/beckn-onix)

**A production-ready, plugin-based middleware adapter for the Beckn Protocol**

[Overview](#overview) • [Features](#features) • [Architecture](#architecture) • [Quick Start](#quick-start) • [Documentation](#documentation) • [Contributing](#contributing)

</div>

---
## Latest Update
In August 2025, a completely new Beckn-ONIX adapter was made available. This version introduces a Plugin framework at it's core. 
The ONIX Adapter previous to this release is archived to a separate branch, [main-pre-plugins](https://github.com/beckn/beckn-onix/tree/main-pre-plugins) for reference.

## Overview

Beckn-ONIX is an enterprise-grade middleware adapter system designed to facilitate seamless communication in any Beckn-enabled network. It acts as a protocol adapter between Beckn Application Platforms (BAPs - buyer applications) and Beckn Provider Platforms (BPPs - seller platforms), ensuring secure, validated, and compliant message exchange across various commerce networks.

### What is Beckn Protocol?

The **Beckn Protocol** is an open protocol that enables location-aware, local commerce across any platform and any domain. It allows creation of open, decentralized networks where:

- **Platform Independence**: Buyers and sellers can transact regardless of the platforms they use
- **Interoperability**: Seamless communication between different systems using standardized protocols
- **Domain Agnostic**: Works across retail, mobility, healthcare, logistics, and other domains
- **Network Neutral**: Can be deployed in any Beckn-compliant network globally

### Key Concepts

- **BAP (Beckn Application Platform)**: Buyer-side applications that help users search for and purchase products/services (e.g., consumer apps, aggregators)
- **BPP (Beckn Provider Platform)**: Seller-side platforms that provide products/services (e.g., merchant platforms, service providers)
- **Beckn Network**: Any network implementing the Beckn Protocol for enabling open commerce

## Features

### 🔌 **Plugin-Based Architecture**
- **Dynamic Plugin Loading**: Load and configure plugins at runtime without code changes
- **Extensible Design**: Easy to add new functionality through custom plugins
- **Hot-Swappable Components**: Update plugins without application restart (in development)

### 🔐 **Enterprise Security**
- **Ed25519 Digital Signatures**: Cryptographically secure message signing and validation
- **HashiCorp Vault Integration**: Centralized secrets and key management
- **Request Authentication**: Every message is authenticated and validated
- **TLS/SSL Support**: Encrypted communication channels

### ✅ **Protocol Compliance**
- **JSON Schema Validation**: Ensures all messages comply with Beckn protocol specifications
- **Version Management**: Support for multiple protocol versions simultaneously
- **Domain-Specific Schemas**: Tailored validation for different business domains

### 🚀 **High Performance**
- **Redis Caching**: Response caching for improved performance
- **RabbitMQ Integration**: Asynchronous message processing via message queues
- **Connection Pooling**: Efficient resource utilization
- **Configurable Timeouts**: Fine-tuned performance controls

### 📊 **Observability**
- **Structured Logging**: JSON-formatted logs with contextual information
- **Transaction Tracking**: End-to-end request tracing with unique IDs
- **OpenTelemetry Metrics**: Performance and business metrics collection
- **Runtime Instrumentation**: Go runtime + Redis client metrics included
- **Health Checks**: Liveness and readiness probes for Kubernetes

### 🌐 **Multi-Domain Support**
- **Retail & E-commerce**: Product search, order management, fulfillment tracking
- **Mobility Services**: Ride-hailing, public transport, vehicle rentals
- **Logistics**: Shipping, last-mile delivery, returns management
- **Healthcare**: Appointments, telemedicine, pharmacy services
- **Financial Services**: Loans, insurance, payments

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    HTTP Request                          │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│                   Module Handler                         │
│  (bapTxnReceiver/Caller or bppTxnReceiver/Caller)      │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│                 Processing Pipeline                      │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐    │
│  │ Middleware  │→ │   Steps     │→ │   Plugins   │    │
│  │(preprocess) │  │(validate,   │  │(cache,router│    │
│  └─────────────┘  │route, sign) │  │validator...)│    │
│                   └─────────────┘  └─────────────┘    │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│              External Services/Response                  │
└─────────────────────────────────────────────────────────┘
```

### Core Components

#### 1. **Transaction Modules**
- `bapTxnReceiver`: Receives callback responses at BAP
- `bapTxnCaller`: Sends requests from BAP to BPP
- `bppTxnReceiver`: Receives requests at BPP
- `bppTxnCaller`: Sends responses from BPP to BAP

#### 2. **Processing Steps**
- `validateSign`: Validates digital signatures on incoming requests
- `addRoute`: Determines routing based on configuration
- `validateSchema`: Validates against JSON schemas
- `sign`: Signs outgoing requests
- `counterSign`: Generates a counter-signature and embeds it in the ACK response for Beckn v2.0.0 LTS; no-op for earlier versions
- `cache`: Caches requests/responses
- `publish`: Publishes messages to queue

#### 3. **Plugin Types**
- **Cache**: Redis-based response caching 
- **Router**: YAML-based routing rules engine for request forwarding (supports domain-agnostic routing for Beckn v2.x.x)
- **Registry**: Standard Beckn registry or Beckn One DeDi registry lookup for participant information
- **Signer**: Ed25519 digital signature creation for outgoing requests
- **SignValidator**: Ed25519 signature validation for incoming requests
- **SchemaValidator**: JSON schema validation
- **Schemav2Validator**: OpenAPI 3.x schema validation with action-based matching 
- **KeyManager**: HashiCorp Vault integration for production key management
- **SimpleKeyManager**: Embedded key management for local development (no external dependencies)
- **Publisher**: RabbitMQ message publishing for asynchronous processing
- **Encrypter**: AES encryption for sensitive data protection
- **Decrypter**: AES decryption for encrypted data processing
- **ReqPreprocessor**: Request preprocessing (UUID generation, headers)
- **ReqMapper**: Middleware to transform payload either between Beckn versions or against other platforms.
- **OtelSetup**: Observability setup for metrics, traces, and logs (OTLP). Supports optional audit log configuration via `auditFieldsConfig` (YAML mapping actions to fields) . See [CONFIG.md](CONFIG.md) for details.


## Quick Start

### Prerequisites

- Go 1.24 or higher
- Redis (for caching)
- Docker (optional, for containerized deployment)

### Build and Run

1. **Clone the repository**
```bash
git clone https://github.com/beckn/beckn-onix.git
cd beckn-onix
```

2. **Build the application**
```bash
go build -o server cmd/adapter/main.go
```

3. **Build plugins**
```bash
./install/build-plugins.sh
```

4. **Extract schemas**
```bash
unzip schemas.zip
```

5. **Start Redis** (if not running)
```bash
docker run -d -p 6379:6379 redis:alpine
```

6. **Update the config file**

**Note**: You can modify the configuration file to suit your environment before starting the server. ONIX adapter/server must be restarted to reflect any change made to the config file.

The following config change is required to all cache related entries in order to connect to `redis` that was started earlier.
```yaml
        cache:
          id: cache
          config:
            addr: localhost:6379
```

7. **Run the application**

```bash
./server --config=config/local-simple.yaml
```

The server will start on `http://localhost:8081`

### Automated Setup (Recommended)

For local setup, starts only redis and onix adapter:

```bash
# Clone and setup everything automatically
git clone https://github.com/beckn/beckn-onix.git
cd beckn-onix/install
chmod +x setup.sh
./setup.sh
```

This automated script will:
- Start Redis container
- Build all plugins with correct Go version
- Build the adapter server
- Start ONIX adapter in Docker
- Create environment configuration

**Note:**
- **Schema Validation**: Extract schemas before running: `unzip schemas.zip` (required for `schemavalidator` plugin)
- **Alternative**: You can use `schemav2validator` plugin instead, which fetches schemas from a URL and doesn't require local schema extraction. See [CONFIG.md](CONFIG.md) for more configuration details.
- **Optional**: Before running the automated setup, build the adapter image and update `docker-compose-adapter.yaml` to use the correct image

```bash
# from the repository root
docker build -f Dockerfile.adapter-with-plugins -t beckn-onix:latest .
```

**For detailed setup instructions, see [SETUP.md](SETUP.md)**

**Services Started:**
- Redis: localhost:6379
- ONIX Adapter: http://localhost:8081

### Docker Deployment

**Note:** Start redis before before running onix adapter.

```bash
# Build the Docker image
 docker build -t beckn-onix:latest -f Dockerfile.adapter-with-plugins .

# Run the container
docker run -p 8081:8081 \
  -v $(pwd)/config:/app/config \
  -v $(pwd)/schemas:/app/schemas \
  -e CONFIG_FILE="/app/config/local-simple.yaml" \
  beckn-onix:latest
```

### Basic Usage Example

#### 1. Search for Products (BAP → BPP)

```bash
curl -X POST http://localhost:8081/bap/caller/search \
  -H "Content-Type: application/json" \
  -d '{
    "context": {
      "domain": "nic2004:60221",
      "country": "IND",
      "city": "std:080",
      "action": "search",
      "version": "0.9.4", 
      "bap_id": "bap.example.com",
      "bap_uri": "https://bap.example.com/beckn",
      "transaction_id": "550e8400-e29b-41d4-a716-446655440000",
      "message_id": "550e8400-e29b-41d4-a716-446655440001",
      "timestamp": "2023-06-15T09:30:00.000Z",
      "ttl": "PT30S"
    },
    "message": {
      "intent": {
        "fulfillment": {
          "start": {
            "location": {
              "gps": "12.9715987,77.5945627"
            }
          },
          "end": {
            "location": {
              "gps": "12.9715987,77.5945627"
            }
          }
        }
      }
    }
  }'
```

## Configuration

### Configuration Structure

```yaml
appName: "beckn-onix"
log:
  level: debug
  destinations:
    - type: stdout
http:
  port: 8080
  timeout:
    read: 30
    write: 30
    idle: 30
pluginManager:
  root: ./plugins
modules:
  - name: bapTxnReceiver
    path: /bap/receiver/
    handler:
      type: std
      role: bap
      plugins:
        cache:
          id: cache
          config:
            addr: localhost:6379
        router:
          id: router
          config:
            routingConfig: ./config/routing.yaml
        schemaValidator:
          id: schemavalidator  # or schemav2validator 
          config:
            schemaDir: ./schemas  # for schemavalidator
            # type: url           # for schemav2validator
            # location: https://example.com/spec.yaml
      steps:
        - validateSign
        - addRoute
        - validateSchema
```

### Deployment Modes

1. **Combined Mode**: Single instance handling both BAP and BPP (`config/onix/`) - Uses `secretskeymanager` (HashiCorp Vault) for production key management
2. **BAP-Only Mode**: Dedicated buyer-side deployment (`config/onix-bap/`)
3. **BPP-Only Mode**: Dedicated seller-side deployment (`config/onix-bpp/`)
4. **Local Development Combined Mode**: Simplified configuration (`config/local-simple.yaml`) - Uses `simplekeymanager` with embedded Ed25519 keys, no vault setup needed
5. **Local Development Combined Mode (Alternative)**: Development configuration (`config/local-dev.yaml`) - Uses `keymanager`, vault setup needed
6. **Local with Observability (BAP/BPP)**: Configs `config/local-beckn-one-bap.yaml` and `config/local-beckn-one-bpp.yaml` include OtelSetup (metrics, traces, audit logs) for use with an OTLP collector. Audit fields are configured via `config/audit-fields.yaml`. For a full stack (collectors, Grafana, Loki), see `install/network-observability/`

## API Endpoints

### BAP Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/bap/caller/search` | Search for products/services |
| POST | `/bap/caller/select` | Select specific items |
| POST | `/bap/caller/init` | Initialize order |
| POST | `/bap/caller/confirm` | Confirm order |
| POST | `/bap/caller/status` | Check order status |
| POST | `/bap/caller/track` | Track order/shipment |
| POST | `/bap/caller/cancel` | Cancel order |
| POST | `/bap/caller/update` | Update order |
| POST | `/bap/caller/rating` | Submit rating |
| POST | `/bap/caller/support` | Get support |

### BPP Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/bpp/receiver/*` | Receives all BAP requests |
| POST | `/bpp/caller/on_*` | Sends responses back to BAP |


## Documentation

- **[Setup Guide](SETUP.md)**: Complete installation, configuration, and deployment instructions
- **[Configuration Guide](CONFIG.md)**: Description of Configuration concepts and all config parameters 
- **[Contributing](CONTRIBUTING.md)**: Guidelines for contributors
- **[Governance](GOVERNANCE.md)**: Project governance model
- **[License](LICENSE)**: Apache 2.0 license details

## GUI Component

The project includes a Next.js-based GUI component located in `onix-gui/` that provides:
- Visual configuration management
- Request/response monitoring
- Plugin status dashboard
- Routing rules editor

## Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run specific test
go test ./pkg/plugin/implementation/cache -v

# Run tests with race detection
go test -race ./...
```

## Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details on:
- Code of Conduct
- Development process
- Submitting pull requests
- Reporting issues

## Support

- **Issues**: [GitHub Issues](https://github.com/beckn/beckn-onix/issues)
- **Discussions**: [GitHub Discussions](https://github.com/beckn/beckn-onix/discussions)
- **Documentation**: [Wiki](https://github.com/beckn/beckn-onix/wiki)

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- [Beckn Foundation](https://beckn.org) for the protocol specifications

| Contributor | Organization | Github ID |
|--------|----------|-------------|
| Ashish Guliya | Google Cloud | ashishkgGoogle |
| Pooja Joshi | Google Cloud | poojajoshi2 |
| Deepa Mulchandani | Google Cloud | Deepa-Mulchandani |
| Ankit | Beckn Labs | ankitShogun |
| Abhishek | Beckn Labs | em-abee |
| Viraj Kulkarni | Beckn Labs | viraj89 |
| Amay Pandey | Google Cloud |  |
| Dipika Prasad | Google Cloud | DipikaPrasad |
| Tanya Madaan | ONDC | tanyamadaan |
| Binu | ONDC |  |
| Faiz M | Beckn Labs | faizmagic |
| Ravi Prakash | Beckn Labs | ravi-prakash-v |
| Siddharth Prakash | Google Cloud |  |
| Namya Patiyal | Google Cloud |  |
| Saksham Nagpal | Google Cloud | sakshamGoogle |
| Arpit Bharadwaj | Google Cloud |  |
| Pranoy | Google Cloud |  |
| Mayuresh Nirhali | Beckn Labs | nirmay |
| Madhuvandhini B | Google Cloud | madhuvandhini5856 |
| Siddhartha Banerjee | Google Cloud | sidb85 |
| Manendra Pal Singh | NPCI BHIM | manendrapalsingh |

---

<div align="center">
Built with ❤️ for the open Value Network ecosystem
</div>
