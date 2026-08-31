# JSON Mapper Plugin

A **mapper plugin** for Beckn-ONIX that transforms one JSON document into another
using a JSONata mapping fetched at runtime.

## Overview

Implements `definition.Mapper`. Given a mapping reference and an input, it
fetches, compiles, caches and runs whatever is there.

It is domain-free by design: it knows nothing about who is calling, nothing about
what a mapping says, and nothing about the payloads passing through. Anything
specific to a network or a provider belongs in the caller, which is what lets one
mapper serve all of them.

Its first caller is the OAN provider flow, where it translates between Beckn
payloads and each provider's own request and response shapes -- so adding a
provider is two mapping files and a registry row rather than another
transformation routine.

It is **not** a pipeline step. A provider plugin holds it and calls it twice --
once to build the upstream request, once to turn the answer back into Beckn.
That is what lets one plugin own the whole exchange while the translation stays
generic.

## Mapping files

A mapping file carries every action one capability serves, keyed by action name:

```yaml
actions:
  select: |
    {
      "lat": _local.lat,
      "lon": _local.lon
    }
  confirm: |
    {
      "booking_id": beckn.message.contract.commitments[0].id
    }
```

**Request files are keyed by the action they translate** (`select`); **response
files by the action they produce** (`on_select`). Each file therefore names the
Beckn actions it actually deals in, and the filename carries no meaning — naming
a file after one action while it serves several would be worse than not naming
it.

One file per direction rather than per action means a transaction walking
`select` then `confirm` pays one fetch, not one per step. An action the file does
not declare is refused, and the error names the ones it does serve.

A mapping that fails to compile takes down only its own action: a typo in
`confirm` is no reason for `select` to stop being served.

References come from the registry (`requestMapping` / `responseMapping` on a
capability binding) and are fully-qualified `http`/`https` URLs. Anything else --
a bare path, a `file://`, a URL with no host -- is refused: references are
external input, and an unchecked one would let a registry record name a local
file and have the adapter read it.

## What a mapping can read

| key | request leg | response leg |
|---|---|---|
| `beckn` | the inbound Beckn payload | the inbound Beckn payload |
| `_local` | values the provider plugin resolved | the same values |
| `response` | — | the provider's raw answer |

`_local` stays in scope on the response leg on purpose. A provider's answer
rarely repeats what it was asked, so values resolved before the call are often
the only source for them in the output — the coordinates of a forecast, say.

## Why the action is a key, not a convention

Nothing else in the pipeline knows the action. A binding key is
`participantId|capabilityCode` and carries none, so without this a capability
publishing one mapping would run it for every action that reached it — a
`confirm` served by a `select` mapping, succeeding quietly and producing
nonsense.

Making the action a key in the file rather than a part of its name means the file
states which actions it serves, instead of a convention someone has to remember.

## Configuration

```yaml
mapper:
  id: jsonmapper
  config:
    fetchTimeout: 5s
    cacheTTL: 1h
    negativeTTL: 1m
    maxMappingBytes: "262144"
    maxCacheEntries: "200"
```

Every setting is optional; the defaults above are what the plugin applies.

`negativeTTL` is how long a failed fetch is remembered. Without it a broken
reference turns every inbound request into an outbound one.

`maxMappingBytes` caps what is read from a mapping host. References come from the
registry, so an unbounded read is an unbounded allocation driven by whoever can
write a registry record.

## Caching, and why evaluation takes a lock

A compiled expression is code, not data, so it cannot live in the shared Redis
cache. It is held in memory, keyed by reference, bounded by `maxCacheEntries`.

`jsonata.Expression.Evaluate` **mutates the expression it is called on** — it
binds into the expression's own frame — so one compiled mapping cannot serve two
requests at once. Confirmed with the race detector, not assumed.

Evaluation therefore takes a per-mapping lock. That is the cheaper trade by a
wide margin: evaluation is ~22µs against ~184µs to compile, and both are dwarfed
by the upstream call the mapped request goes on to make. Different mappings still
run in parallel. A pool of compiled expressions would remove even that, and is
the upgrade if one mapping ever becomes hot enough to matter.

## Failure

A mapping that cannot be fetched, parsed or compiled is an operator or registry
fault and surfaces as a plain error. A mapping that ran but could not be applied
is the payload's shape being wrong, and surfaces as a `SCH_SCHEMA_ADAPTATION_FAILED`
bad request.
