# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Beckn-ONIX is a middleware adapter system for the Beckn protocol, designed for any Beckn-enabled network. It acts as a bridge between BAP (Beckn Application Platform) and BPP (Beckn Provider Platform) systems, providing a plugin-based architecture for handling HTTP requests, routing, validation, signing, and integration with external services.

## Technology Stack

- **Language**: Go 1.23 (with Go 1.23.4 toolchain)
- **Architecture**: Plugin-based middleware system
- **Key Dependencies**: 
  - Redis (caching)
  - RabbitMQ (messaging via amqp091-go)
  - HashiCorp Vault (secrets management)
  - JSON Schema validation (jsonschema/v6)
- **Containerization**: Docker with Dockerfile.adapter
- **Frontend**: Node.js-based GUI component (onix-gui/)

## Build and Development Commands

### Build locally
```bash
go build -o server cmd/adapter/main.go
```

### Run with configuration
```bash
./server --config=config/onix/adapter.yaml
```

### Docker build
```bash
docker build -f Dockerfile.adapter -t beckn-onix-adapter .
```

### Run tests
```bash
go test ./...
```

### Run specific test
```bash
go test ./pkg/plugin/implementation/cache -v
```

### Run tests with coverage
```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out  # View coverage in browser
go tool cover -func=coverage.out  # View coverage summary
```

### Run tests with race detection
```bash
go test -race ./...
```

### Format and lint code
```bash
go fmt ./...
go vet ./...
golangci-lint run  # Requires golangci-lint installation
```

### Clean build artifacts
```bash
rm -f server
rm -rf plugins/*.so
```

### Module maintenance
```bash
go mod tidy       # Clean up dependencies
go mod download   # Download dependencies
go mod verify     # Verify dependencies
```

### Local Development Setup

For local development without Docker:

1. **Build plugins** (required for the plugin-based architecture):
```bash
./build-plugins.sh
```

2. **Create required directories**:
```bash
mkdir -p schemas
```

3. **Run with local config**:
```bash
go run cmd/adapter/main.go --config=config/local-dev.yaml
```

Note: The application requires:
- Redis running on localhost:6379 for caching
- Plugins built as .so files in the ./plugins directory
- Schema files in ./schemas for validation (optional)
- HashiCorp Vault for key management (optional, can be disabled in config)

## Architecture Overview

### Core Components

1. **cmd/adapter/main.go**: Main application entry point that:
   - Loads YAML configuration
   - Initializes plugin manager
   - Sets up HTTP server with configurable timeouts
   - Registers modules and their handlers

2. **core/module/**: Core business logic and module system
   - `module.go`: Module registration and management
   - `handler/`: HTTP request handlers and processing steps
   - `client/`: Client registry for external service connections

3. **pkg/plugin/**: Extensible plugin system with:
   - `definition/`: Plugin interfaces (cache, router, signer, validator, etc.)
   - `implementation/`: Concrete implementations of each plugin type
   - `manager.go`: Plugin lifecycle management

### Plugin Types

The system supports these plugin types:
- **cache**: Redis-based caching
- **router**: Request routing based on YAML configuration  
- **signer/signvalidator**: Request signing and validation
- **schemavalidator**: JSON schema validation
- **keymanager**: HashiCorp Vault integration for secrets
- **publisher**: RabbitMQ message publishing
- **encrypter/decrypter**: AES encryption/decryption
- **reqpreprocessor**: Request preprocessing middleware (UUID generation, etc.)

### Configuration Structure

The system uses YAML configuration files in `config/` directory:
- `config/local-dev.yaml`: Simplified configuration for local development
- `config/local-routing.yaml`: Routing rules for local development
- `config/onix/`: Combined BAP+BPP configuration for production
- `config/onix-bap/`: BAP-only deployment configuration
- `config/onix-bpp/`: BPP-only deployment configuration

Each configuration defines:
- HTTP server settings (port, timeouts)
- Plugin manager settings
- Modules with their handlers, plugins, and processing steps

### Request Flow

1. HTTP request received by server
2. Routed to appropriate module (bapTxnReceiver, bapTxnCaller, bppTxnReceiver, bppTxnCaller)
3. Processed through configured steps (validateSign, addRoute, validateSchema, sign)
4. Each step uses configured plugins to perform its function
5. Response returned or forwarded based on routing configuration

## Module Types and Responsibilities

- **bapTxnReceiver**: Receives incoming requests at BAP (buyer platform)
- **bapTxnCaller**: Makes outgoing calls from BAP to BPP
- **bppTxnReceiver**: Receives incoming requests at BPP (provider platform)
- **bppTxnCaller**: Makes outgoing calls from BPP to BAP

## Module Configuration Patterns

Modules follow this structure:
- **name**: Module identifier
- **path**: HTTP endpoint path
- **handler**: Processing configuration including:
  - **role**: "bap" or "bpp"
  - **plugins**: Plugin instances with their configurations
  - **steps**: Ordered list of processing steps

## Processing Steps

Available processing steps that can be configured:
- **validateSign**: Validates digital signatures on incoming requests
- **addRoute**: Determines routing based on configuration
- **validateSchema**: Validates against JSON schemas
- **sign**: Signs outgoing requests
- **cache**: Caches requests/responses
- **publish**: Publishes messages to queue

## Environment Variables

The configuration supports environment variable substitution using `${variable}` syntax, commonly used for:
- `${projectID}`: GCP project ID for Vault and Pub/Sub
- Connection strings and service endpoints

## Testing

Tests are colocated with source files using `_test.go` suffix. Each plugin implementation has comprehensive test coverage including mock data in `testdata/` directories.

## CI/CD Pipeline

The project uses GitHub Actions for CI with the following checks:
- Unit tests with minimum 90% coverage requirement
- golangci-lint for code quality
- Coverage reports uploaded to Codecov