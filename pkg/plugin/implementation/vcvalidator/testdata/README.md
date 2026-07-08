# vcvalidator test vectors

Mock Verifiable Credentials used by the unit tests. Everything here is offline:
the tests inject an in-memory fetcher that maps the DID-document and status-list
URLs to the files below, so no network access is required.

## `vectors/` — generated mock credentials

Produced by [`gen/main.go`](gen/main.go). Each issuer key is derived from a
fixed seed and every document is emitted with stable field ordering, so the
files are reproducible — regenerate them only when you intend to change a vector:

```bash
# from the plugin directory
go run testdata/gen/main.go
```

| file | DID method | revoked? | notes |
|------|-----------|----------|-------|
| `didkey-unrevoked.json` | `did:key` (Ed25519) | no | `credentialStatus` bit clear |
| `didkey-revoked.json` | `did:key` (Ed25519) | yes | `credentialStatus` bit set |
| `didjwk-unrevoked.json` | `did:jwk` (Ed25519) | no | issuer key embedded in the DID |
| `didjwk-revoked.json` | `did:jwk` (Ed25519) | yes | |
| `didweb-unrevoked.json` | `did:web` | no | issuer is the object form `{id,name}` |
| `didweb-revoked.json` | `did:web` | yes | |
| `didweb-did.json` | — | — | DID document served at `https://issuer.example.org/.well-known/did.json` |
| `statuslist.json` | — | — | StatusList2021 credential; bit `94` set (revoked), bit `17` clear |

The revoked and not-revoked vectors of a method share an issuer key and differ
only in which `statusListIndex` they point at in `statuslist.json`.

All vectors carry a validity window of `2026-01-01 .. 2027-12-31`; the tests pin
the clock to `2026-06-27`, so they never expire under test regardless of the
real date.

## `flockenergy_vc.json` — real-world credential

A genuine, externally-issued `MeterDataRequestCredential`: a `did:key` (P-256)
VC-JWT used by `TestRealDIDKeyVC` to verify the plugin against a credential it
did not generate itself. Its window is `2026-06-04 .. 2026-12-04`.
