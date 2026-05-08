# agentenginetransportwrapper

A `TransportWrapper` plugin that bridges a beckn-onix adapter directly to a
[Vertex AI Agent Engine](https://cloud.google.com/vertex-ai/generative-ai/docs/agent-engine/overview)
deployed reasoning engine, replacing the previous Pub/Sub +
JavaScript-UDF + push-subscription bridge with a single in-process step.

On every outbound HTTP request through the wrapped transport this plugin:

1. Reads the outbound JSON body (assumed to be a Beckn message envelope).
2. Extracts `context.action`.
3. Decides whether to **wrap** or **pass through** based on the configured
   `allowedActionPrefixes` and `passthroughOther` (see "Action handling"
   below).
4. For wrapped actions: rewrites the body byte-for-byte into the Agent
   Engine `:query` envelope:
   ```json
   {
     "class_method": "<action>",
     "input": { "request": <original Beckn body> }
   }
   ```
5. For wrapped actions: mints a Google OAuth2 access token (cloud-platform
   scope) using the token source built once at `New()` (see Authentication
   below).
6. For wrapped actions: sets `Authorization: Bearer <access-token>` and
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
            # Optional. Default ["on_"]. Action prefixes that should be wrapped.
            allowedActionPrefixes: ["on_"]
            # Optional. Default false. If true, requests with a non-matching
            # action are forwarded UNMODIFIED (no envelope, no token).
            passthroughOther: false
```

| Key | Type | Default | Description |
|---|---|---|---|
| `serviceAccount` | string | _empty_ | If set, the plugin impersonates this service account when minting access tokens. The caller identity (typically the adapter Pod's KSA) must hold `roles/iam.serviceAccountTokenCreator` on this SA. When empty, Application Default Credentials are used directly. |
| `allowedActionPrefixes` | []string | `["on_"]` | Action prefixes (matched via `strings.HasPrefix`) that the wrapper transforms into the Agent Engine `:query` envelope. Must contain at least one prefix; an empty list is rejected at startup. |
| `passthroughOther` | bool | `false` | What to do with requests whose `context.action` does not match any allowed prefix. `false` (the default) returns HTTP 502 to the upstream — fail-loud behaviour suitable for a wrapper attached to a callback-only module. `true` forwards the request **unmodified** (no envelope, no token attached, no `Content-Type` override) — useful when the wrapper is attached to a module that handles a mix of callback and non-callback actions. |

## Action handling

The plugin's behaviour for each inbound request follows this matrix:

| Action matches `allowedActionPrefixes`? | `passthroughOther` | Behaviour |
|---|---|---|
| Yes | _any_ | Wrap into `:query` envelope, mint OAuth2 access token, attach `Authorization: Bearer …`, forward. |
| No | `false` (default) | Reject with HTTP 502: `action %q does not match any allowed prefix …`. Fail-loud — surfaces mis-attachment of the wrapper. |
| No | `true` | Forward the request **unmodified** (original body, original headers, no envelope, no token). |

Examples:

- **BAP receiver only handles callbacks** (typical):
  ```yaml
  config:
    serviceAccount: agent-invoker-sa@PROJECT.iam.gserviceaccount.com
    # allowedActionPrefixes defaults to ["on_"]; passthroughOther defaults
    # to false. Any non-on_* request is rejected as a config error.
  ```

- **BAP receiver mixes callbacks and a custom action wrapped through Vertex AI**:
  ```yaml
  config:
    serviceAccount: agent-invoker-sa@PROJECT.iam.gserviceaccount.com
    allowedActionPrefixes: ["on_", "search"]
    # passthroughOther stays false: anything that's not on_* or search is
    # rejected. search calls are wrapped and forwarded to Agent Engine.
  ```

- **Single module handles callbacks AND forward-direction Beckn calls** (advanced):
  ```yaml
  config:
    serviceAccount: agent-invoker-sa@PROJECT.iam.gserviceaccount.com
    allowedActionPrefixes: ["on_"]
    passthroughOther: true
    # on_* callbacks are wrapped + signed for Vertex AI.
    # search/init/confirm/etc. are forwarded untouched (Beckn signer
    # earlier in the pipeline already set their Authorization header).
  ```

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

In `passthroughOther: true` mode, the token source is still built at
startup (because it may still be needed by wrapped actions), but it is
never invoked for non-matching actions.

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

## Failure modes

| Condition | Caller sees |
|---|---|
| Body is not valid JSON | 502 from the adapter |
| `context` block missing or non-object | 502 |
| `context.action` missing, empty, or non-string | 502 |
| Action does not match any `allowedActionPrefixes` AND `passthroughOther` is `false` | 502 |
| Token mint failure (e.g. metadata server unreachable) | 502 |
| Per-request context deadline exceeded during token mint | error propagated to the caller |
| Agent Engine 5xx (or upstream 5xx in passthrough mode) | passthrough of the response status (typically 5xx) |
| Network / connection error | error propagated (typically 502 from `httputil.ReverseProxy`) |

The plugin is **single-shot, no retries, no DLQ** — matching the
convention of every other TransportWrapper and the rest of the
sync-proxy path in beckn-onix. Failed deliveries are surfaced
synchronously to the upstream Beckn caller.
