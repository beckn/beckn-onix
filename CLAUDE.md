# beckn-onix — Claude Project Context

## What This Repo Is

Beckn-ONIX is a production-ready, plugin-based middleware adapter for the Beckn Protocol. It acts as a protocol bridge between BAPs (buyer applications) and BPPs (seller platforms) in any Beckn-enabled commerce network. The adapter handles message signing/validation, schema compliance, routing, caching, and observability.

Go module: `github.com/beckn-one/beckn-onix`  
Go version: 1.24.6

---

## Repository Layout

```
beckn-onix/
├── cmd/adapter/          # Main entrypoint (main.go)
├── core/                 # Core adapter engine
│   └── module/           # Module lifecycle, HTTP handler, client
├── pkg/
│   ├── plugin/
│   │   ├── definition/   # Plugin interfaces (contracts)
│   │   ├── implementation/  # All plugin implementations (one dir each)
│   │   └── manager.go    # Dynamic plugin loader
│   ├── model/            # Shared domain models
│   ├── log/              # Logging utilities
│   ├── response/         # Response helpers
│   └── telemetry/        # OpenTelemetry setup
├── config/               # YAML adapter configs per role/env
├── plugins/              # Compiled .so plugin binaries
├── schemas/              # Beckn JSON schemas for validation
├── install/              # Setup, build, and teardown scripts
├── Deployment/           # Kubernetes manifests
└── benchmarks/           # Performance benchmarks
```

---

## Plugin Architecture

Plugins are Go shared libraries (`.so`) loaded at runtime via `pkg/plugin/manager.go`. Each plugin lives in `pkg/plugin/implementation/<name>/` with its own `cmd/plugin.go` entrypoint.

**Available plugins:**

| Plugin | Purpose |
|---|---|
| `signer` / `signvalidator` | Ed25519 message signing and verification |
| `keymanager` / `simplekeymanager` | Cryptographic key management (Vault-backed and simple) |
| `registry` / `dediregistry` | Beckn network registry lookup (standard + DeDi) |
| `schemavalidator` / `schemav2validator` | JSON Schema validation for Beckn messages |
| `opapolicychecker` | OPA-based policy enforcement with network-aware config |
| `reqmapper` | JSONata-based request transformation |
| `reqpreprocessor` | Request preprocessing pipeline |
| `router` | Request routing to BAP/BPP endpoints |
| `cache` | Redis-backed response caching |
| `publisher` | RabbitMQ async message publishing |
| `encrypter` / `decrypter` | Message encryption/decryption |
| `otelsetup` | OpenTelemetry initialization |

**To add a new plugin:** implement the interface in `pkg/plugin/definition/`, create `pkg/plugin/implementation/<name>/`, and register it in `install/build-plugins.sh`.

---

## Build & Run

```bash
# Build all plugins
./install/build-plugins.sh

# Run the adapter (pick a config)
go run ./cmd/adapter/main.go -config config/local-dev.yaml

# Run tests
go test ./...

# Run benchmarks
./benchmarks/run_benchmarks.sh
```

**Config files** in `config/` follow a naming convention:
- `generic-bap.yaml` / `generic-bpp.yaml` — generic role configs
- `local-*.yaml` — local dev configs
- `local-beckn-one-*.yaml` — beckn.one network configs
- `local-retail-*.yaml` — retail domain configs

---

## Current Iteration: 1.6.0 — April 2026

Milestone due: **April 30, 2026** | 62% complete

**Open issues (12):**

### OPA / Policy cluster
- [#619](https://github.com/beckn/beckn-onix/issues/619) — Policy declaration in beckn
- [#642](https://github.com/beckn/beckn-onix/issues/642) — OPA: signed policy artifact verification ← PR #661 pending merge
- [#643](https://github.com/beckn/beckn-onix/issues/643) — OPA: network-specific policy config ← PR #661 pending merge
- [#647](https://github.com/beckn/beckn-onix/issues/647) — Extend `dediregistry` for registry metadata lookup
- [#648](https://github.com/beckn/beckn-onix/issues/648) — Add a generic manifest loader plugin
- [#649](https://github.com/beckn/beckn-onix/issues/649) — Add manifest-backed policy support to `opapolicychecker`

### Observability
- [#652](https://github.com/beckn/beckn-onix/issues/652) — Include all loaded plugins in Network Observability metrics
- [#657](https://github.com/beckn/beckn-onix/issues/657) — fix: parentSpanId/parent_id naming inconsistency + missing message_id→span_id OTTL mapping
- [#658](https://github.com/beckn/beckn-onix/issues/658) — fix: action metric label uses URL path instead of Beckn context.action

### Protocol
- [#651](https://github.com/beckn/beckn-onix/issues/651) — [Proto-v2.0] Add CounterSignature to Ack response

### Infrastructure
- [#592](https://github.com/beckn/beckn-onix/issues/592) — Beckn Infra services config to be separated into a signed file
- [#610](https://github.com/beckn/beckn-onix/issues/610) — Bug: Registry on_subscribe fails

---

## Key Conventions

- **Language:** Go only. No generated code checked in except `.so` plugin binaries.
- **Error handling:** return errors up the call stack; use `pkg/log` (zerolog) for structured logging.
- **Config format:** YAML. All plugin configs live under the `plugins:` key in adapter YAML files.
- **Testing:** unit tests alongside source (`_test.go`). Integration tests use `httptest.NewServer`. Target ≥ 90% coverage.
- **Commits:** conventional commits (`feat:`, `fix:`, `chore:`, `docs:`). Reference issue numbers.
- **Breaking changes:** call them out explicitly in PR descriptions as "migration required".

---

## Team

- **nirmay** (you) — lead reviewer, observability and protocol work
- **nirmalnr** — OPA/policy and DeDi registry work
- **711ayush711** — contributor, secondary reviewer

---

## Useful Links

- [GitHub repo](https://github.com/beckn/beckn-onix)
- [Open issues](https://github.com/beckn/beckn-onix/issues)
- [Milestone 1.6.0](https://github.com/beckn/beckn-onix/milestone/10)
- [Open PRs](https://github.com/beckn/beckn-onix/pulls)
- [Beckn Protocol docs](https://beckn.org)
