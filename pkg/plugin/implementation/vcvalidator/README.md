# vcvalidator — Verifiable Credential validator middleware

A beckn-onix `Middleware` plugin that verifies the [W3C Verifiable
Credentials](https://www.w3.org/TR/vc-data-model-2.0/) embedded in a request
body. When enabled it gates the configured beckn action(s) and rejects the
request with a **NACK** if any embedded credential fails verification — the
request is never forwarded to the next handler.

It implements the
[`definition.MiddlewareProvider`](../../definition/middleware.go) contract and
is loaded by the plugin manager as `vcvalidator.so`, like any other middleware
(e.g. `reqpreprocessor`).

A credential is any JSON object in the body carrying both a `proof` and a
`credentialSubject`. This is the combination beckn uses for an embedded VC (for
example a credential nested under
`message.contract.participants[].participantAttributes`), so the plugin needs no
knowledge of the surrounding message shape.

## What it checks

For every embedded credential:

1. **Proof signature** — verifies a VC-JWT (`proof.jwt`) signature against the
   issuer's public key, resolved from the issuer DID. Supported DID methods:
   - `did:key` — `Ed25519` (`z6Mk…`), `P-256` (`zDn…`), `secp256k1` (`zQ3…`)
   - `did:jwk` — embedded JWK
   - `did:web` — fetches `https://<host>/[path/]did.json` and reads the
     verification method's `publicKeyJwk` / `publicKeyMultibase` /
     `publicKeyBase58`
2. **Issuer binding** — the JWT signer (the `kid` controller DID) must equal the
   credential's declared `issuer`. A credential signed by anyone other than its
   issuer is rejected (`ISSUER_MISMATCH`). The signing algorithm in the JWT
   header must also match the resolved key's algorithm (alg-confusion
   protection).
3. **Validity window** — `validFrom` / `validUntil` and the JWT `nbf` / `exp`.
4. **Revocation** — when `credentialStatus` is present:
   StatusList2021 / BitstringStatusList bitstring lookup, a DEDI registry
   lookup, or a generic revoked indicator.

### A note on JSON-LD Data Integrity proofs

Proofs of type `Ed25519Signature2020` / `DataIntegrityProof` (carrying a
`proofValue` rather than a `jwt`) require RDF canonicalisation (URDNA2015),
which this plugin does **not** implement. With `requireProof: true` (default)
such credentials are rejected; with `requireProof: false` the signature step is
skipped and only the validity window, revocation, and verification-method
resolvability are checked.

## NACK error codes

| code | meaning | HTTP |
|------|---------|------|
| `INVALID_CREDENTIAL` | malformed credential / missing issuer | 400 |
| `INVALID_PROOF` | signature invalid, missing, or alg mismatch | 401 |
| `ISSUER_MISMATCH` | proof signer ≠ declared issuer | 401 |
| `CREDENTIAL_EXPIRED` | outside validity window | 401 |
| `DID_RESOLUTION_FAILED` | could not resolve issuer / verification-method DID | 401 |
| `CREDENTIAL_REVOKED` | revoked per `credentialStatus` | 403 |

The NACK body matches beckn-onix's v2 shape:

```json
{"message":{"status":"NACK","messageId":"…","error":{"code":"…","message":"…"}}}
```

## Configuration

Wired as a `middleware` entry on a module handler (alongside `reqpreprocessor`):

```yaml
modules:
  - name: bppTxnReceiver
    path: /bpp/receiver/
    handler:
      type: std
      role: bpp
      plugins:
        middleware:
          - id: vcvalidator
            config:
              enabled: "true"           # master switch
              actions: "confirm"        # REQUIRED — comma list of gated beckn actions
              allowedDidMethods: "key,jwk,web"
              checkExpiry: "true"
              checkRevocation: "true"
              requireProof: "true"      # reject proofs this plugin cannot verify
              failOpen: "false"         # on did:web/revocation network errors: false = reject
              httpTimeout: "10"         # seconds
              debugLogging: "false"
```

| key | required | default | meaning |
|-----|----------|---------|---------|
| `enabled` | no | `true` | when `false`, every request passes through untouched |
| `actions` | **yes** (when enabled) | — | comma list of gated beckn actions, e.g. `confirm,init` |
| `allowedDidMethods` | no | `key,jwk,web` | permitted issuer / verification-method DID methods |
| `checkExpiry` | no | `true` | enforce `validFrom`/`validUntil` and `nbf`/`exp` |
| `checkRevocation` | no | `true` | check `credentialStatus` |
| `requireProof` | no | `true` | reject credentials whose proof this plugin cannot verify |
| `failOpen` | no | `false` | on transient network errors, `true` allows / `false` rejects |
| `httpTimeout` | no | `10` | seconds; bounds did:web and revocation-list fetches |
| `debugLogging` | no | `false` | verbose per-credential logging |

`actions` has no hidden code default — it must be declared in the YAML so the
gated messages are always visible from the config alone.

## Testing

```bash
# from the repo root
go test ./pkg/plugin/implementation/vcvalidator/...
```

The suite runs fully offline. It includes:

- `TestVectors` — the committed mock credentials under
  [`testdata/vectors/`](testdata/vectors/) covering `did:key`, `did:jwk` and
  `did:web` in both a not-revoked and a revoked state; the referenced DID
  document and StatusList2021 credential are served from an in-memory fetcher.
- `TestRealDIDKeyVC` — a real, externally-issued `did:key` (P-256) VC-JWT
  ([`testdata/flockenergy_vc.json`](testdata/flockenergy_vc.json)).
- Negative tests for tampered signatures, expired / not-yet-valid windows,
  issuer mismatch, did:web unreachable (fail-closed and fail-open), DEDI
  revocation, and Data Integrity proof rejection.

See [`testdata/README.md`](testdata/README.md) for the fixtures and how to
regenerate them.

## Building

`vcvalidator` is listed in [`install/build-plugins.sh`](../../../../install/build-plugins.sh)
and is built like any other plugin:

```bash
go build -buildmode=plugin -o plugins/vcvalidator.so \
    ./pkg/plugin/implementation/vcvalidator/cmd/plugin.go
```
