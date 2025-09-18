# DeDi Registry Plugin

A Beckn-ONIX plugin for integrating with DeDi (Decentralized Digital Identity) registry services to lookup participant information and public keys.

## Overview

The DeDi Registry plugin enables Beckn-ONIX to query DeDi registries for participant records, supporting secure communication and identity verification in Beckn networks.

## Features

- **Registry Lookup**: Query DeDi registries for participant records
- **Public Key Retrieval**: Extract public keys for signature validation
- **Configurable Endpoints**: Support for different DeDi registry implementations
- **HTTP Retry Logic**: Built-in retry mechanism for reliable API calls
- **Timeout Control**: Configurable request timeouts

## Configuration

```yaml
plugins:
  dediRegistry:
    id: dediregistry
    config:
      baseURL: "https://dedi-registry.example.com"
      apiKey: "your-api-key"
      namespaceID: "beckn-network"
      registryName: "participants"
      recordName: "participant-id"
      timeout: "30"  # seconds
```

### Configuration Parameters

| Parameter | Required | Description | Default |
|-----------|----------|-------------|---------|
| `baseURL` | Yes | DeDi registry API base URL | - |
| `apiKey` | Yes | API key for authentication | - |
| `namespaceID` | Yes | DeDi namespace identifier | - |
| `registryName` | Yes | Registry name to query | - |
| `recordName` | Yes | Record name/identifier | - |
| `timeout` | No | Request timeout in seconds | 30 |

## Usage

### In Module Configuration

```yaml
modules:
  - name: bapTxnReceiver
    handler:
      plugins:
        dediRegistry:
          id: dediregistry
          config:
            baseURL: "https://dedi-registry.example.com"
            apiKey: "your-api-key"
            namespaceID: "beckn-network"
            registryName: "participants"
            recordName: "participant-id"
```

### In Processing Steps

Access the plugin in custom processing steps:

```go
// Get DeDi registry from handler
dediRegistry := handler.GetDeDiRegistry()

// Lookup participant record
record, err := dediRegistry.LookupRecord(ctx, "participant.example.com")
if err != nil {
    return err
}

// Extract public key
publicKey := record.Schema.PublicKey
```

### With KeyManager Integration

```go
// Use with KeyManager for public key retrieval
keyManager := handler.GetKeyManager()
dediRegistry := handler.GetDeDiRegistry()

// Lookup and store public key
record, err := dediRegistry.LookupRecord(ctx, participantID)
if err == nil && record.Schema.PublicKey != "" {
    keyManager.StorePublicKey(participantID, record.Schema.PublicKey)
}
```

## API Response Structure

The plugin expects DeDi registry responses in this format:

```json
{
  "schema": {
    "participant_id": "participant.example.com",
    "public_key": "base64-encoded-public-key",
    "status": "active",
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

## Error Handling

The plugin handles various error scenarios:

- **Network Errors**: Automatic retry with exponential backoff
- **Authentication Errors**: Clear error messages for invalid API keys
- **Not Found**: Returns appropriate errors when records don't exist
- **Timeout Errors**: Configurable timeout handling

## Testing

Run plugin tests:

```bash
go test ./pkg/plugin/implementation/dediregistry -v
```

## Dependencies

- `github.com/hashicorp/go-retryablehttp`: HTTP client with retry logic
- Standard Go libraries for HTTP and JSON handling

## Integration Notes

- Plugin follows Beckn-ONIX plugin architecture patterns
- Compatible with existing KeyManager and other plugins
- Can be used in any module (BAP/BPP receiver/caller)
- Supports hot-reloading through configuration changes