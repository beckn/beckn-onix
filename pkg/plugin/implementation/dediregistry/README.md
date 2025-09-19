# DeDi Registry Plugin

A Beckn-ONIX registry type plugin for integrating with DeDi registry services. Implements the `RegistryLookup` interface to provide participant information and public keys.

## Overview

The DeDi Registry plugin enables Beckn-ONIX to lookup DeDi registries for participant records, converting DeDi API responses to standard Beckn Subscription format for seamless integration with existing registry infrastructure.

## Features

- **RegistryLookup Interface**: Implements standard Beckn registry interface
- **DeDi API Integration**: GET requests to DeDi registry endpoints with Bearer authentication
- **Data Conversion**: Converts DeDi responses to Beckn Subscription format
- **HTTP Retry Logic**: Built-in retry mechanism using retryablehttp client
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

### In Code

```go
// Load DeDi registry plugin
dediRegistry, err := manager.Registry(ctx, &plugin.Config{
    ID: "dediregistry",
    Config: map[string]string{
        "baseURL": "https://dedi-registry.example.com",
        "apiKey": "your-api-key",
        "namespaceID": "beckn-network",
        "registryName": "participants",
        "recordName": "participant-id",
    },
})

// Or use specific method
dediRegistry, err := manager.DeDiRegistry(ctx, config)

// Lookup participant (returns Beckn Subscription format)
subscription := &model.Subscription{}
results, err := dediRegistry.Lookup(ctx, subscription)
if err != nil {
    return err
}

// Extract public key from first result
if len(results) > 0 {
    publicKey := results[0].SigningPublicKey
    subscriberID := results[0].SubscriberID
}
```

## API Response Structure

The plugin expects DeDi registry responses in this format:

```json
{
  "message": "success",
  "data": {
    "namespace": "beckn",
    "schema": {
      "entity_name": "participant.example.com",
      "entity_url": "https://participant.example.com",
      "publicKey": "base64-encoded-public-key",
      "keyType": "ed25519",
      "keyFormat": "base64"
    },
    "state": "active",
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

### Converted to Beckn Format

The plugin converts this to standard Beckn Subscription format:

```json
{
  "subscriber_id": "participant.example.com",
  "url": "https://participant.example.com",
  "signing_public_key": "base64-encoded-public-key",
  "status": "active",
  "created": "2023-01-01T00:00:00Z",
  "updated": "2023-01-01T00:00:00Z"
}
```

## Testing

Run plugin tests:

```bash
go test ./pkg/plugin/implementation/dediregistry -v
```

## Dependencies

- `github.com/hashicorp/go-retryablehttp`: HTTP client with retry logic
- Standard Go libraries for HTTP and JSON handling

## Integration Notes

- **Registry Type Plugin**: Implements `RegistryLookup` interface, not a separate plugin category
- **Interchangeable**: Can be used alongside or instead of standard registry plugin
- **Manager Integration**: Available via `manager.Registry()` or `manager.DeDiRegistry()` methods
- **Data Conversion**: Automatically converts DeDi format to Beckn Subscription format
- **Interface Compliance**: Implements `RegistryLookup` interface with `Lookup()` method only
- **Build Integration**: Included in `build-plugins.sh` script, compiles to `dediregistry.so`