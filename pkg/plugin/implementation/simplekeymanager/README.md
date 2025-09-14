# SimpleKeyManager Plugin

A simple keymanager plugin for beckn-onix that reads Ed25519 and X25519 keys from configuration instead of using external secret management systems like HashiCorp Vault.

## Overview

This plugin provides a lightweight alternative to the vault keymanager by reading cryptographic keys directly from configuration. It's designed for development environments and simpler deployments that don't require the complexity of external secret management.

## Features

- **Ed25519 + X25519 Key Support**: Supports Ed25519 signing keys and X25519 encryption keys
- **Configuration-Based**: Reads keys from YAML configuration instead of environment variables
- **Multiple Formats**: Supports both PEM and Base64 encoded keys
- **Auto-detection**: Automatically detects key format (PEM vs Base64)
- **Zero Dependencies**: No external services required (unlike vault keymanager)
- **Memory Storage**: Stores keysets in memory for fast access

## Configuration

### Basic Configuration

In your beckn-onix configuration file:

```yaml
plugins:
  keymanager:
    id: simplekeymanager
    config:
      networkParticipant: bap-network
      keyId: bap-network-key
      signingPrivateKey: uc5WYG/eke0PVGyQ9JNVLpwQL0K9JIZfHfqUHdLBTaY=
      signingPublicKey: kUSiFNAD3+6oE7KffKucxZ74e6g4i9VM6ypImg4rVCM=
      encrPrivateKey: uc5WYG/eke0PVGyQ9JNVLpwQL0K9JIZfHfqUHdLBTaY=
      encrPublicKey: kUSiFNAD3+6oE7KffKucxZ74e6g4i9VM6ypImg4rVCM=
```

### Configuration Options

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `networkParticipant` | string | Yes | Identifier for the keyset, represents subscriberId or networkParticipant name |
| `keyId` | string | Yes | Unique Key id for the keyset |
| `signingPrivateKey` | string | Yes* | Ed25519 private key for signing (Base64 or PEM) |
| `signingPublicKey` | string | Yes* | Ed25519 public key for signing (Base64 or PEM) |
| `encrPrivateKey` | string | Yes* | X25519 private key for encryption (Base64 or PEM) |
| `encrPublicKey` | string | Yes* | X25519 public key for encryption (Base64 or PEM) |

*Required if any key is provided. If keys are configured, all four keys must be provided.

## Key Generation

### Ed25519 Signing Keys

```bash
# Generate Ed25519 signing key pair
openssl genpkey -algorithm Ed25519 -out signing_private.pem
openssl pkey -in signing_private.pem -pubout -out signing_public.pem

# Convert to base64 (single line)
signing_private_b64=$(openssl pkey -in signing_private.pem -outform DER | base64 -w 0)
signing_public_b64=$(openssl pkey -in signing_public.pem -pubin -outform DER | base64 -w 0)
```

### X25519 Encryption Keys

```bash
# Generate X25519 encryption key pair
openssl genpkey -algorithm X25519 -out encr_private.pem
openssl pkey -in encr_private.pem -pubout -out encr_public.pem

# Convert to base64 (single line)
encr_private_b64=$(openssl pkey -in encr_private.pem -outform DER | base64 -w 0)
encr_public_b64=$(openssl pkey -in encr_public.pem -pubin -outform DER | base64 -w 0)
```

## Usage

The plugin implements the same `KeyManager` interface as the vault keymanager:

- `GenerateKeyset() (*model.Keyset, error)` - Generate new key pair
- `InsertKeyset(ctx, keyID, keyset) error` - Store keyset in memory
- `Keyset(ctx, keyID) (*model.Keyset, error)` - Retrieve keyset from memory
- `DeleteKeyset(ctx, keyID) error` - Delete keyset from memory
- `LookupNPKeys(ctx, subscriberID, uniqueKeyID) (string, string, error)` - Lookup public keys from registry

### Example Usage in Code

```go
// The keyset from config is automatically loaded with the configured keyId
keyset, err := keyManager.Keyset(ctx, "bap-network")
if err != nil {
    log.Fatal(err)
}

// Generate new keys programmatically
newKeyset, err := keyManager.GenerateKeyset()
if err != nil {
    log.Fatal(err)
}

// Store the new keyset
err = keyManager.InsertKeyset(ctx, "new-key-id", newKeyset)
if err != nil {
    log.Fatal(err)
}
```

## Comparison with Vault KeyManager

| Feature | SimpleKeyManager | Vault KeyManager |
|---------|------------------|------------------|
| **Setup Complexity** | Very Low (config only) | High (requires Vault) |
| **Configuration** | YAML configuration | Vault connection + secrets |
| **Dependencies** | None | HashiCorp Vault |
| **Security** | Basic (config-based) | Advanced (centralized secrets) |
| **Key Rotation** | Manual config update | Automated options |
| **Audit Logging** | Application logs only | Full audit trails |
| **Multi-tenancy** | Limited (memory-based) | Full support |
| **Best for** | Development/Testing/Simple deployments | Production/Enterprise |

## Testing

Run tests with:
```bash
cd pkg/plugin/implementation/simplekeymanager
go test -v ./...
```

## Installation

1. The plugin is automatically built with beckn-onix
2. Configure the plugin in your beckn-onix configuration file. Change in configuration requires restart of service.
3. The plugin will be loaded automatically when beckn-onix starts

## Security Considerations

- Configuration files contain sensitive key material
- Use proper file permissions for config files
- Implement regular key rotation

## License

This plugin follows the same license as the main beckn-onix project.
