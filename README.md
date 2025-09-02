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
- **Metrics Support**: Performance and business metrics collection
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
- `cache`: Caches requests/responses
- `publish`: Publishes messages to queue

#### 3. **Plugin Types**
- **Cache**: Redis-based response caching
- **Router**: YAML-based routing rules engine
- **Signer/SignValidator**: Ed25519 signature handling
- **SchemaValidator**: JSON schema validation
- **KeyManager**: HashiCorp Vault integration
- **Publisher**: RabbitMQ message publishing
- **Encrypter/Decrypter**: AES encryption/decryption
- **ReqPreprocessor**: Request preprocessing (UUID generation, headers)

## Quick Start

### Prerequisites

- Go 1.23 or higher
- Redis (for caching)
- Docker (optional, for containerized deployment)
- HashiCorp Vault (optional, for production key management)
- RabbitMQ (optional, for async messaging)

### Installation

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
./build-plugins.sh
```

4. **Start Redis** (if not running)
```bash
docker run -d -p 6379:6379 redis:alpine
```

5. **Run the application**
```bash
./server --config=config/local-dev.yaml
```

The server will start on `http://localhost:8081`

### Docker Deployment

```bash
# Build the Docker image
docker build -f Dockerfile.adapter -t beckn-onix:latest .

# Run the container
docker run -p 8080:8080 \
  -v $(pwd)/config:/app/config \
  -v $(pwd)/schemas:/app/schemas \
  beckn-onix:latest
```

### Basic Usage Example

#### 1. Search for Products (BAP → BPP)

```bash
curl -X POST http://localhost:8081/bap/caller/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Signature keyId=\"bap.example.com|key1|ed25519\",algorithm=\"ed25519\",created=\"1234567890\",expires=\"1234567990\",headers=\"(created) (expires) digest\",signature=\"base64signature\"" \
  -d '{
    "context": {
      "domain": "nic2004:60221",
      "country": "IND",
      "city": "std:080",
      "action": "search",
      "core_version": "0.9.4",
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
          id: schemavalidator
          config:
            schemaDir: ./schemas
      steps:
        - validateSign
        - addRoute
        - validateSchema
```

### Deployment Modes

1. **Combined Mode**: Single instance handling both BAP and BPP (`config/onix/`)
2. **BAP-Only Mode**: Dedicated buyer-side deployment (`config/onix-bap/`)
3. **BPP-Only Mode**: Dedicated seller-side deployment (`config/onix-bpp/`)
4. **Local Development**: Simplified configuration (`config/local-dev.yaml`)

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
| Ankit | FIDE | ankitShogun |
| Abhishek | FIDE | em-abee |
| Viraj Kulkarni | FIDE | viraj89 |
| Amay Pandey | Google Cloud |  |
| Dipika Prasad | Google Cloud | DipikaPrasad |
| Tanya Madaan | ONDC | tanyamadaan |
| Binu | ONDC |  |
| Faiz M | FIDE | faizmagic |
| Ravi Prakash | FIDE | ravi-prakash-v |
| Siddharth Prakash | Google Cloud |  |
| Namya Patiyal | Google Cloud |  |
| Saksham Nagpal | Google Cloud | sakshamGoogle |
| Arpit Bharadwaj | Google Cloud |  |
| Pranoy | Google Cloud |  |
| Mayuresh Nirhali | FIDE Volunteer | nirmay |
| Madhuvandhini B | Google Cloud | madhuvandhini5856 |
| Siddhartha Banerjee | Google Cloud | sidb85 |

---

<div align="center">
Built with ❤️ for the open Value Network ecosystem
</div>
