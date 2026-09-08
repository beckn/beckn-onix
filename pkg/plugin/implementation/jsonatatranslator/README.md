# JSONata Translator

`jsonatatranslator` implements the `Translator` plugin interface (`pkg/plugin/definition/translator.go`) using the JSONata expression language. `Translate(ctx, artifact, payload)` treats `artifact` as JSONata expression text, compiles it (cached by expression text, up to 500 entries), and evaluates it against `payload`.

It's the default `translator` for `reqmapper`'s `payloadTransformer` step and `schemaversionmediator` — see [CONFIG.md](../../../../CONFIG.md#12-reqmapper-plugin-step). Both share one instance (and its expression cache) when configured on the same handler.

## Config

```yaml
translator:
  id: jsonatatranslator
```

No config keys are read today; `config` is accepted only for interface consistency with other `TranslatorProvider` plugins.

## Notes

- Compile and evaluate errors are both wrapped with `%w`, so callers can still `errors.As` into an underlying `*v206.JSONataError`.
- There's no compile-only validation entry point — a bad expression only surfaces on first `Translate` call, not at load time. `reqmapper` used to compile mappings eagerly at startup before this migration; that fail-fast behavior is gone now that execution lives behind the engine-agnostic `Translator` interface.
- `ctx` isn't forwarded to the evaluator — `jsonata-go` doesn't support cancellation.
