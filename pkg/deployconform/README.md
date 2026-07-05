# Deployment Conformance

`pkg/deployconform` and the `deployconform` CLI let a Network Facilitator
Organization (NFO) publish the *ideal deployed configuration* of a devkit —
and let every network participant verify their running deployment against it.

Network policies (see the [OPA Policy Checker](../plugin/implementation/opapolicychecker/README.md))
govern the **messages** participants exchange. Deployment conformance governs
the **configuration** those participants run: the docker-compose file, the
adapter configs it launches, the routing/policy/schema files those configs
reference. A participant that quietly removes the `checkPolicy` step, swaps
an image tag, or points at a different Rego file deviates from the network's
operating agreement even though every message it sends still validates.

Conformance is **warn-and-alert, never block**: deviations are logged as
warnings and (optionally) reported to the network's observability collector.
Nothing stops a deviating stack from running.

## How it works

```
docker-compose.yml ──┐
adapter configs ─────┤   discover      canonicalize        redact              hash
routing / policies ──┼──────────────▶ (sorted-key JSON) ─▶ (variance rules) ─▶ (sha256 per artifact,
referenced files ────┘                                                          merkle-style root)
                                                                                      │
                             ┌────────────────────────────────────────────────────────┤
                             ▼                                                        ▼
                   baseline generation (NFO)                              verification (participant)
                   publish + sign baseline.yaml                           compare root & artifact hashes,
                   reference it from the network manifest                 diff deviating paths, evaluate
                                                                          deployment policy, emit telemetry
```

1. **Discover** — walk the deployment graph from the compose file: each
   service subtree becomes an artifact (`compose:<service>`), plus every
   local file reachable from it — the config named by `CONFIG_FILE` or a
   `--config=<path>` / `--config <path>` argument (translated through the
   service's bind mounts), bind-mounted files, and files referenced by
   `./...` strings ending in `.yaml`/`.yml`/`.json`/`.rego` inside configs
   (routing configs, policy configs, Rego files). All paths are confined to the
   devkit root.
2. **Canonicalize** — parse YAML/JSON artifacts and re-serialize as compact
   JSON with sorted keys (`canonical-json/1`), so hashes track content, not
   formatting or comments. Other files are hashed as LF-normalized bytes.
3. **Redact** — apply the NFO's *variance rules*: paths that are legitimately
   participant-specific (signing keys, participant IDs, ports, registry
   details) are replaced with a placeholder before hashing. Everything not
   declared variable is network-fixed by default.
4. **Hash & compare** — after redaction, every compliant participant produces
   identical artifact hashes and one identical role root hash. On mismatch,
   the verifier diffs the local canonical form against the baseline's and
   names the exact deviating paths.
5. **Policy** — an optional Rego policy (same `{valid, violations}` decision
   contract as network message policies) constrains the values the hash layer
   cannot pin: subset rules on `allowedNetworkIDs`, "checkPolicy must appear
   in every module's steps", and so on. It is evaluated locally over the full
   unredacted configuration tree.
6. **Telemetry** — deviations are optionally POSTed to the manifest's
   `observability.collector.url` as `deployment.deviation` events carrying
   artifact IDs, deviation kinds, and paths — never configuration values.

Because the variance rules live in the NFO-signed baseline, a participant
cannot self-exempt a field: exceptions are network-governed.

## The `deployment` section of the network manifest

The baseline is distributed exactly like network policies: referenced from
the signed network manifest, fetched over HTTPS, and verified against a
detached signature and a published public key.

```yaml
deployment:
  devkitId: "p2p-trading-devkit"
  baseline:
    id: "p2p-trading-baseline-v1"
    url: "https://nfo.example.org/deploy/baseline.yaml"
    signed: true
    signatureUrl: "https://nfo.example.org/deploy/baseline.yaml.sig"
    signingPublicKeyLookupUrl: "https://api.dedi.global/dedi/lookup/nfo.example.org/key-reg/policy-key"
  policy:                      # optional; same schema as the policies section
    type: "rego"
    source: "file"
    file:
      id: "deployment-policy-v1"
      url: "https://nfo.example.org/deploy/deployment.rego"
      policyQueryPath: "data.deployment.policy.result"
      signed: true
      signatureUrl: "https://nfo.example.org/deploy/deployment.rego.sig"
      signingPublicKeyLookupUrl: "https://api.dedi.global/dedi/lookup/nfo.example.org/key-reg/policy-key"
```

| Field | Required | Description |
| ----- | -------- | ----------- |
| `deployment.devkitId` | Yes | Devkit release this baseline describes |
| `deployment.baseline` | Yes | The baseline document artifact (URL + signature metadata) |
| `deployment.policy` | No | Deployment policy, `source: file` only for now |

This PR also adds the `observability` section (`enabled`, `config`,
`collector.url`) to the typed manifest schema, matching the published
network-manifest documentation; the collector URL is where deviation events
are sent.

## The baseline document

```yaml
baselineVersion: "1.0"
baselineType: "deployment-baseline"
networkId: "example.org/production"
devkitId: "p2p-trading-devkit"
releaseId: "2026.07"
hashAlgorithm: "sha256"
canonicalization: "canonical-json/1"
placeholder: "__PARTICIPANT_SPECIFIC__"
composePath: "install/docker-compose.yml"
variance:
  - artifacts: ["config/adapter-*.yaml"]
    paths:
      - "http.port"
      - "modules.handler.plugins.keyManager.config"
      - "modules.handler.plugins.registry.config"
  - artifacts: ["config/routing-*.yaml"]      # no paths = whole artifact is
                                              # participant-owned
  - artifacts: ["compose:*"]
    paths: ["ports", "container_name", "environment"]
roles:
  bap:
    services: ["onix-bap"]
    artifacts:                                # computed by `deployconform baseline`
      - id: "compose:onix-bap"
        sha256: "…"
        canonical: '{"image":"…",…}'
      - id: "config/adapter-bap.yaml"
        sha256: "…"
        canonical: '{…}'
    rootSha256: "…"
```

Notes on the format:

- **Roles** slice the devkit by participant: a production BAP verifies only
  the `bap` role even though the devkit compose file also defines the BPP.
- **Scope is the ONIX adapters and their configuration.** Only services
  listed under a role are verified; devkit sandbox/mock services (and any
  other container the participant replaces with a real system in
  production) are left out of every role and therefore never checked.
- **Variance paths** are dot notation into the parsed artifact. Lists are
  traversed transparently (`modules.handler.plugins…` applies to every
  element of `modules`); `*` matches any key. A rule with no `paths` marks
  the whole artifact participant-owned — only its presence is checked.
- **`canonical`** embeds the redacted canonical form of each artifact. It is
  what enables path-level deviation reports; it never contains participant
  values because it is produced *after* redaction from the NFO's reference
  checkout.
- **`include`** (optional) adds files discovery would not reach, e.g.
  `include: ["schemas/**"]`.

## Generating and publishing a baseline (NFO)

1. Author a spec — the baseline document above without `artifacts`/
   `rootSha256` — and check out the exact devkit release it describes.
2. Generate:

   ```bash
   go run ./cmd/deployconform baseline \
     --root ./devkits/p2p-trading-devkit \
     --spec baseline-spec.yaml \
     --out baseline.yaml
   ```

3. Sign `baseline.yaml` with a detached signature (same flow as the network
   manifest, e.g. `openssl dgst -sha256 -sign private.pem -out
   baseline.yaml.sig baseline.yaml`) and publish both at stable URLs.
4. Add the `deployment` section to the network manifest, re-sign and
   re-publish the manifest, and bump its `releaseId`.

## Verifying a deployment (participant)

Resolve everything from the network ID (registry metadata → signed manifest
→ signed baseline → signed policy):

```bash
go run ./cmd/deployconform verify \
  --root . \
  --network-id nfo.example.org/production
```

Other resolution modes:

```bash
# explicit manifest URLs (signature still verified)
deployconform verify --root . \
  --manifest-url https://nfo.example.org/manifest.yaml \
  --manifest-signature-url https://nfo.example.org/manifest.yaml.sig \
  --manifest-key-url https://api.dedi.global/dedi/lookup/…/policy-key

# local files, development only (no verification)
deployconform verify --root . --baseline-file baseline.yaml \
  --policy-file deployment.rego --policy-query data.deployment.policy.result
```

Useful flags: `--role <name>` (default: every role with services in the
local compose file), `--json`, `--strict` (exit 2 on deviations, for CI),
`--watch 15m` (keep verifying on an interval; intervals below one minute
are clamped, since every tick re-fetches the published artifacts),
`--telemetry=false`.

Example output:

```
OK   role "bap" conforms to the network baseline (root 3f2a1b9c04de…)
WARN role "bpp" deviates from the network baseline (expected root 9c04…, computed 71ee…)
  [modified] config/adapter-bpp.yaml
    - modules[0].handler.steps: list has 3 entries, baseline expects 4
  [policy] deployment policy violations:
    - config/adapter-bpp.yaml: module txnReceiver must include the checkPolicy step
```

### Running as a sidecar

Add the verifier to the devkit stack so drift is caught continuously:

```yaml
  conformance:
    image: <adapter image with deployconform>   # or build FROM golang
    command: ["deployconform", "verify", "--root", "/devkit",
              "--network-id", "nfo.example.org/production", "--watch", "15m"]
    volumes:
      - ..:/devkit:ro
```

The sidecar needs only a read-only mount of the devkit and outbound HTTPS;
it never modifies anything.

## Writing deployment policies

The policy input for each role is:

```json
{
  "networkId": "example.org/production",
  "devkitId": "p2p-trading-devkit",
  "role": "bap",
  "compose": { "services": { … } },
  "artifacts": {
    "config/adapter-bap.yaml": { …parsed tree… },
    "policies/network.rego": "…file text…"
  }
}
```

Policies follow the standard decision contract — a rule returning
`{"valid": bool, "violations": [string]}`:

```rego
package deployment.policy

import rego.v1

violations contains msg if {
	some name, cfg in input.artifacts
	startswith(name, "config/adapter-")
	some module in cfg.modules
	not "checkPolicy" in module.handler.steps
	msg := sprintf("%s: module %s must include the checkPolicy step", [name, module.name])
}

result := {"valid": count(violations) == 0, "violations": violations}
```

Test it exactly like a network policy: `opa eval -d deployment.rego -i
input.json --format=raw data.deployment.policy.result`.

## Deviation telemetry

Non-compliant roles produce one `deployment.deviation` event each, POSTed as
JSON to the manifest's `observability.collector.url` (or `--collector-url`):

```json
{
  "eventType": "deployment.deviation",
  "networkId": "example.org/production",
  "devkitId": "p2p-trading-devkit",
  "role": "bpp",
  "expectedRoot": "9c04…", "computedRoot": "71ee…",
  "baselineDigest": "ab31…",
  "findings": [
    {"artifactId": "config/adapter-bpp.yaml", "kind": "modified",
     "details": [{"path": "modules[0].handler.steps"}]}
  ],
  "generatedAt": "2026-07-05T10:00:00Z"
}
```

Finding details are structured `{path, message}` pairs; for `modified`
findings the message (which renders local configuration values, possibly
including misplaced secrets) is dropped before emission, so local values
never leave the host. Telemetry failures are warnings, never
errors.

## Security considerations

- The manifest, baseline, and deployment policy are all fetched with
  detached-signature verification (`pkg/security/artifactverifier`, the same
  path the manifest loader and OPA policy checker use). Verification can only
  be disabled with an explicit `--skip-signature-verification` flag that
  prints a prominent warning.
- Discovery is confined to the devkit root: references that resolve outside
  it (including through symlinks) are ignored, so a hostile config cannot
  make the verifier read or hash arbitrary host files.
- All reads and fetches are size-bounded; discovery is depth- and
  count-bounded.
- Participant-owned values are redacted *before* hashing, so no secret
  material appears in baselines, reports embed values only in local output,
  and telemetry events carry paths only.
- The tool needs no credentials and never writes to the devkit.

## Known limitations

- `deployment.policy.source: bundle` is not implemented yet; use a single
  signed Rego file.
- Directory bind mounts are not walked implicitly (a mounted `../schemas`
  directory contributes only the files configs actually reference); use
  `include` globs to pin whole directories.
- Only `./`-prefixed references with config-bearing extensions
  (`.yaml`/`.yml`/`.json`/`.rego`) are followed from inside config files;
  anything else must be pinned with `include` globs.
- URLs in configuration are treated as opaque strings — the URL is part of
  the hashed content, the resource behind it is not fetched. Content pinning
  for remote artifacts belongs to their own signature/checksum mechanisms.
- Environment variables interpolated by compose (`${VAR}`) are hashed as
  written, not expanded.
- Conformance is advisory: it detects and reports drift, it does not prevent
  a deviating stack from starting.

## Dependencies

- `github.com/open-policy-agent/opa/v1` — deployment policy evaluation
- `gopkg.in/yaml.v3` — configuration parsing
- `pkg/model`, `pkg/security/artifactverifier`, and the `manifestloader` /
  `dediregistry` plugins — manifest resolution and signature verification

No new module dependencies are introduced.
