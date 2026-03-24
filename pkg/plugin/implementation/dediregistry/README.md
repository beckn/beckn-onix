# DeDi Registry Plugin

A **registry type plugin** for Beckn-ONIX that integrates with DeDi (Decentralized Digital Infrastructure) registry services via the new DeDi Wrapper API.

## Overview

The DeDi Registry plugin implements the `RegistryLookup` interface to retrieve public keys and participant information from DeDi registry services. It's used by the KeyManager for **signature validation of incoming requests** from other network participants.





## Configuration

```yaml
registry:
  id: dediregistry
  config:
    url: "https://dedi-wrapper.example.com/dedi"
    registryName: "subscribers.beckn.one"
    allowedNetworkIDs: "commerce-network/subscriber-references,local-commerce/subscriber-references"
    timeout: 30
    retry_max: 3
    retry_wait_min: 1s
    retry_wait_max: 5s
```

### Configuration Parameters

| Parameter | Required | Description | Default |
|-----------|----------|-------------|---------|
| `url` | Yes | DeDi wrapper API base URL (include /dedi path) | - |
| `registryName` | Yes | Registry name for lookup path | - |
| `allowedNetworkIDs` | No | Allowlist of network membership IDs from `data.network_memberships` for signature validation | - |
| `timeout` | No | Request timeout in seconds | Client default |
| `retry_max` | No | Maximum number of retry attempts | 4 (library default) |
| `retry_wait_min` | No | Minimum wait time between retries (e.g., "1s", "500ms") | 1s (library default) |
| `retry_wait_max` | No | Maximum wait time between retries (e.g., "5s") | 30s (library default) |

## API Integration

### Beckn Registry API Format
```
GET {url}/lookup/{subscriber_id}/{registryName}/{key_id}
```

**Example**: `https://api.bekcn.io/registry/dedi/lookup/bpp.example.com/subscribers.beckn.one/76EU7K8oC9EQbXPMRL5uw3KbmTxbg3YDXHvm9nVQpK2eGghASnwHzm`

### Authentication
**No authentication required** - Beckn Registry API is public.

### Expected Response Format

```json
{
  "message": "Record retrieved from registry cache",
  "data": {
    "record_id": "76EU8vY9TkuJ9T62Sc3FyQLf5Kt9YAVgbZhryX6mFi56ipefkP9d9a",
    "details": {
      "url": "http://dev.np2.com/beckn/bap",
      "type": "BAP",
      "domain": "energy",
      "subscriber_id": "dev.np2.com",
      "signing_public_key": "384qqkIIpxo71WaJPsWqQNWUDGAFnfnJPxuDmtuBiLo=",
      "encr_public_key": "test-encr-key"
    },
    "network_memberships": ["commerce-network/subscriber-references", "local-commerce/subscriber-references"],
    "created_at": "2025-10-27T11:45:27.963Z",
    "updated_at": "2025-10-27T11:46:23.563Z"
  }
}
```

## Usage Context

### Signature Validation Flow
```
1. External ONIX → Request with Authorization header
2. ONIX Receiver → parseHeader() extracts subscriberID/keyID  
3. validateSign step → KeyManager.LookupNPKeys()
4. KeyManager → DeDiRegistry.Lookup() with extracted values
5. DeDi Registry → GET {url}/lookup/{subscriberID}/{registryName}/{keyID}
6. DeDi Wrapper → Returns participant public keys
7. SignValidator → Validates signature using retrieved public key
```

### Module Configuration Example

```yaml
modules:
  - name: bppTxnReceiver
    handler:
      plugins:
        registry:
          id: dediregistry
          config:
            url: "https://api.bekcn.io/registry/dedi"
            registryName: "subscribers.beckn.one"
            allowedNetworkIDs: "commerce-network/subscriber-references,local-commerce/subscriber-references"
            timeout: 30
            retry_max: 3
            retry_wait_min: 1s
            retry_wait_max: 5s
      steps:
        - validateSign  # Required for registry lookup
        - addRoute
```

## Field Mapping

| DeDi Wrapper Field | Beckn Field | Description |
|-------------------|-------------|-------------|
| `data.details.subscriber_id` | `subscriber_id` | Participant identifier |
| `{key_id from URL}` | `key_id` | Unique key identifier |
| `data.details.signing_public_key` | `signing_public_key` | Public key for signature verification |
| `data.details.encr_public_key` | `encr_public_key` | Public key for encryption |
| `data.is_revoked` | `status` | Not mapped (Status field will be empty) |
| `data.created_at` | `created` | Creation timestamp |
| `data.updated_at` | `updated` | Last update timestamp |

## Features

- **No Authentication Required**: DeDi wrapper API doesn't require API keys
- **GET Request Format**: Simple URL-based parameter passing
- **Comprehensive Error Handling**: Validates required fields and HTTP responses
- **Simplified Response**: Focuses on public key retrieval for signature validation
- **Retry Support**: Built-in retry mechanism for network resilience

## Testing

Run the test suite:

```bash
go test ./pkg/plugin/implementation/dediregistry -v
```

The tests cover:
- URL construction validation
- Response parsing for new API format
- Error handling scenarios
- Configuration validation
- Plugin provider functionality

## Migration Notes

This plugin replaces direct DeDi API integration with the new DeDi Wrapper API format:

- **Removed**: API key authentication, namespaceID parameters
- **Added**: Configurable registryName parameter
- **Changed**: POST requests → GET requests
- **Updated**: Response structure parsing (`data.details` object)
- **Updated**: Optional allowlist validation now checks `data.network_memberships`
- **Added**: New URL path parameter format

## Dependencies

- `github.com/hashicorp/go-retryablehttp`: HTTP client with retry logic
- Standard Go libraries for HTTP and JSON handling

## Error Handling

- **Configuration Errors**: Missing url or registryName
- **Network Errors**: Connection failures, timeouts
- **HTTP Errors**: Non-200 status codes from DeDi wrapper
- **Data Errors**: Missing required fields in response
- **Validation Errors**: Empty subscriber ID or key ID in request
