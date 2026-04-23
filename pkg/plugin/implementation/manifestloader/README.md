# Manifest Loader

`manifestloader` fetches a manifest, verifies its detached signature, and caches the verified document for downstream consumers such as `opapolicychecker`.

One primary use case is fetching network manifests published by Network Facilitator Organizations (NFOs). Other plugins can then consume the verified manifest to configure themselves according to the policies and artifact locations defined by the NFO.

## Config

```yaml
manifestLoader:
  id: manifestloader
  config:
    cacheTTL: 24h
    fetchTimeoutSeconds: "30"
    forceRefreshOnStartup: false
    disableCache: false
```

Supported config keys:

- `cacheTTL`: TTL for verified manifest cache entries.
- `fetchTimeoutSeconds`: HTTP timeout for manifest, signature, and key fetches.
- `forceRefreshOnStartup`: optional. Defaults to `false`. Bypasses cache once per manifest key after process start, then resumes normal cache use.
- `disableCache`: optional. Defaults to `false`. Bypasses cache entirely and skips cache writes. Useful for debugging manifest changes.

## Cache behavior

- Manifest cache is independent from OPA policy refresh cadence.
- If Redis or another persistent cache backend is used, restarting ONIX does not clear cached manifests.
- `forceRefreshOnStartup` is the operator-friendly way to refresh stale manifests on restart without manually deleting cache keys.
- `disableCache` is intended for debugging and should generally be left `false` in production.
- The loader now logs whether a manifest came from cache, bypassed cache, or was fetched and re-verified remotely.

## Trust boundary

- The manifest loader verifies the manifest itself, but it does not restrict which domains may appear inside the manifest content.
- Downstream plugins that consume a verified manifest may fetch additional policy or artifact URLs declared by the manifest publisher.
- This is an intentional trust decision: once a manifest is verified, ONIX trusts the NFO-defined artifact locations referenced by that manifest.
