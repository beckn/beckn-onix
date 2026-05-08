# agentenginetransportwrapper

A `TransportWrapper` plugin that bridges a beckn-onix adapter directly to a
[Vertex AI Agent Engine](https://cloud.google.com/vertex-ai/generative-ai/docs/agent-engine/overview)
deployed reasoning engine, replacing the previous Pub/Sub +
JavaScript-UDF + push-subscription bridge with a single in-process step.

On every outbound HTTP request through the wrapped transport this plugin:

1. Reads the outbound JSON body (assumed to be a Beckn message envelope).
2. Extracts `context.action`.
3. Rejects the request with HTTP 502 if the action does not start with `on_`
   (the plugin only handles Beckn callbacks; see "Why on_*-only?" below).
4. Wraps the original body byte-for-byte into the Agent Engine `:query`
   envelope:
   ```json
   {
     "class_method": "<action>",
     "input": { "request": <original Beckn body> }
   }
   ```
5. Mints a Google OAuth2 access token (cloud-platform scope) using the
   token source built once at `New()` (see Authentication below).
6. Sets `Authorization: Bearer <access-token>` and
   `Content-Type: application/json`.
7. Forwards the request to the underlying transport once (single-shot).

The plugin is symmetrical to the JS UDF transformation and the
`oidc_token{}` block of the previous Pub/Sub-based bridge — swapping a
route's `targetType: publisher` for `targetType: url` plus this
TransportWrapper produces an end-to-end equivalent flow with one fewer
hop and no Pub/Sub topic to operate.

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

## Authentication

Tokens minted are **OAuth2 access tokens** with the `cloud-platform`
scope, not OIDC ID tokens — Vertex AI's `aiplatform.googleapis.com`
endpoint validates OAuth2 scopes, not audience claims.

The token source is built **eagerly in `New()`**, so misconfiguration
(no ADC, bogus impersonation target, missing
`iam.serviceAccountTokenCreator` grant, etc.) surfaces at adapter
startup rather than at the first inbound callback.

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
rule must use a URL target instead of a publisher target:

```yaml
# old: hop through Pub/Sub
- target:
    targetType: publisher
    publisherId: "<adapter-topic-name>"

# new: forward straight to Agent Engine; this plugin handles the rest
- target:
    targetType: url
    url: "https://<region>-aiplatform.googleapis.com/v1/projects/<project>/locations/<region>/reasoningEngines/<engine-id>:query"
```

## Why on_*-only?

The `:query` envelope wrap is the whole purpose of this plugin and
applies only to inbound Beckn callbacks (`on_search`, `on_discover`,
`on_select`, `on_init`, `on_confirm`, `on_status`, `on_cancel`,
`on_update`). Outbound forward-direction Beckn calls (`search`,
`select`, `init`, `confirm`, `status`, `cancel`, `update`) target the
network's BPP, not Vertex AI, and must NOT be wrapped or have an
OAuth2 token attached — they have their own Beckn `Authorization`
header set by the `signer` step.

The plugin therefore rejects non-`on_*` actions with HTTP 502 as a
safety check: such a request reaching this transport indicates a
misconfiguration (e.g., the wrapper attached to the wrong module or
to a generic outbound BAP/BPP path). The supported deployment is to
attach this wrapper only to the module that handles the inbound
callbacks (typically `bapTxnReceiver` for a BAP); the
`config/local-dev.yaml` example shows the correct placement.

A future revision could expose `allowedActionPrefixes` (defaulting to
`["on_"]`) so the same plugin could pass-through-mint a token without
envelope rewriting for non-callback actions; the present version keeps
the surface minimal.

## Failure modes

| Condition | Caller sees |
|---|---|
| Body is not valid JSON | 502 from the adapter |
| `context` block missing or non-object | 502 |
| `context.action` missing, empty, or non-string | 502 |
| Action does not start with `on_` | 502 |
| Token mint failure (e.g. metadata server unreachable) | 502 |
| Per-request context deadline exceeded during token mint | error propagated to the caller |
| Agent Engine 5xx | passthrough of the response status (typically 5xx) |
| Network / connection error to Agent Engine | error propagated (typically 502 from `httputil.ReverseProxy`) |

The plugin is **single-shot, no retries, no DLQ** — matching the
convention of every other TransportWrapper and the rest of the
sync-proxy path in beckn-onix. Failed deliveries are surfaced
synchronously to the upstream Beckn caller.
