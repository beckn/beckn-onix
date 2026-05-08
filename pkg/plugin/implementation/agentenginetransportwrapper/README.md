# agentenginetransportwrapper

A `TransportWrapper` plugin that bridges a beckn-onix adapter directly to a
Vertex AI Agent Engine deployed reasoning engine.

On every outbound HTTP request through the wrapped transport this plugin:

1. Reads the outbound JSON body (assumed to be a Beckn message envelope).
2. Extracts `context.action`.
3. **If the action starts with `on_` (Beckn callback):**
   1. Wraps the body byte-for-byte into the Agent Engine `:query`
      envelope:
      ```json
      {
        "class_method": "<action>",
        "input": { "request": <original Beckn body> }
      }
      ```
   2. Mints a Google OAuth2 access token (cloud-platform scope) using
      the token source built once at `New()` (see Authentication below).
   3. Sets `Authorization: Bearer <access-token>` and
      `Content-Type: application/json`.
4. **If the action does not start with `on_`:** forwards the request
   **unmodified** (original body, original headers, no envelope, no
   token).
5. Forwards the request to the underlying transport once (single-shot).

## Configuration

The plugin is loaded as a `transportWrapper` block on a per-module basis
inside `adapter.yaml`:

```yaml
modules:
  - name: bapTxnReceiver
    handler:
      ...
      plugins:
        transportWrapper:
          id: agentenginetransportwrapper
          config:
            serviceAccount: "agent-invoker-sa-XYZ@PROJECT.iam.gserviceaccount.com"
```

| Key | Type | Default | Description |
|---|---|---|---|
| `serviceAccount` | string | _empty_ | If set, the plugin impersonates this service account when minting access tokens. The caller identity (typically the adapter Pod's KSA) must hold `roles/iam.serviceAccountTokenCreator` on this SA. When empty, Application Default Credentials are used directly. |

## Action handling

The plugin's behaviour for each inbound request:

| `context.action` prefix | Behaviour |
|---|---|
| `on_` (Beckn callback) | Wrap into `:query` envelope, mint OAuth2 access token, attach `Authorization: Bearer …`, forward. |
| anything else | Forward the request **unmodified** — original body, original headers, no envelope, no token. |

This makes the wrapper safe to attach to a module that handles a mix of
inbound callbacks and forward-direction Beckn calls: `on_*` requests are
routed to the Agent Engine, while `search`, `select`, `init`, `confirm`,
`status`, `cancel`, `update` etc. are forwarded untouched (their Beckn
`Authorization` header is already set by the `signer` step earlier in
the pipeline).

## Authentication

Tokens minted are **OAuth2 access tokens** with the `cloud-platform`
scope, not OIDC ID tokens — Vertex AI's `aiplatform.googleapis.com`
endpoint validates OAuth2 scopes, not audience claims.

The token source is built **eagerly in `New()`**, so misconfiguration
(no ADC, bogus impersonation target, missing
`iam.serviceAccountTokenCreator` grant, etc.) surfaces at adapter
startup rather than at the first inbound callback. The token source is
not invoked for non-callback (pass-through) actions.

Per-request token mint calls run on a dedicated goroutine and respect
the request's `context.Context`, so a hung metadata-server / IAM call
will not outlive the caller's HTTP deadline.

## IAM prerequisites

When the plugin is configured to impersonate a target service account
(`serviceAccount` set), the following bindings must exist:

| Resource | Role | Member | Purpose |
|---|---|---|---|
| The configured `serviceAccount` | `roles/iam.serviceAccountTokenCreator` | The adapter Pod's KSA (via Workload Identity) | Allows the Pod to mint access tokens **as** the target SA. |
| The Vertex AI project | `roles/aiplatform.user` | The configured `serviceAccount` | Allows the minted token to invoke the reasoning engine. |

When the plugin is in ADC mode (`serviceAccount` empty), only the
second binding is required, applied directly to the Pod's KSA.

## Routing wiring

For the plugin to receive any traffic, the relevant module's routing
rule must use a URL target pointing at the reasoning engine's `:query`
endpoint:

```yaml
- target:
    targetType: url
    url: "https://<region>-aiplatform.googleapis.com/v1/projects/<project>/locations/<region>/reasoningEngines/<engine-id>:query"
```

## Failure modes

| Condition | Caller sees |
|---|---|
| Body is not valid JSON | 502 from the adapter |
| `context` block missing or non-object | 502 |
| `context.action` missing, empty, or non-string | 502 |
| Token mint failure (e.g. metadata server unreachable) — callbacks only | 502 |
| Per-request context deadline exceeded during token mint — callbacks only | error propagated to the caller |
| Agent Engine 5xx (or upstream 5xx in pass-through mode) | passthrough of the response status (typically 5xx) |
| Network / connection error | error propagated (typically 502 from `httputil.ReverseProxy`) |
