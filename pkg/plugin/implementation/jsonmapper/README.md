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
provider is one mapping file and a registry row rather than another
transformation routine.

It is **not** a pipeline step. A provider plugin holds it and calls it twice --
once to build the upstream request, once to turn the answer back into Beckn.
That is what lets one plugin own the whole exchange while the translation stays
generic.

## Mapping files

One file per binding-action, carrying **both directions** and what the capability
requires of a payload:

```yaml
# mappings/mausamgram/weather-observation.select.yaml
required:
  - check: |
      ( $ra := beckn.message.contract.commitments[0].resources[0].resourceAttributes;
        $ra.location.type = "Point" )
    message: "this capability needs a Point location"

request: |
  {
    "lat": beckn.message.contract.commitments[0].resources[0].resourceAttributes.location.coordinates[1]
  }

response: |
  {
    "rainfall": response.fcstday1.rain,
    "at": response.location
  }
```

The three keys are checked and run in the order they appear.

**One file rather than two because both legs of one upstream call are one unit of
configuration.** They are published, reviewed and retired together, and a
reference to one is a reference to the other. It also means the response leg is
already fetched and compiled by the time it is needed — one round trip, one fetch.

**A half that is absent or empty produces nothing, with no error.** What nothing
means belongs to the caller: on the request leg it means there is no document to
send. A half that will not compile is a different thing and reported as an error —
the two must not collapse, or an unmapped upstream answer would go out as a Beckn
response.

A broken half takes down only itself: a typo in the response mapping is no reason
to stop making the call, and finding out on the way back beats finding out before
the call was made.

### `required`

A list of preconditions, each a JSONata predicate and the message to refuse with.
Named `check`/`message` so each field says what it is: `check` is what has to
hold, `message` is what the caller is told when it does not. A neutral name like
`test` says nothing about which way the predicate must answer, and `otherwise`
reads like an alternative value rather than an error.

**It exists because a mapping otherwise cannot refuse.** Without it, every
judgement about whether a payload can be served at all lives in Go — so a
provider with its own rule needs its own build, and the rule ends up in a
different place from the extraction it guards.

`Verify` evaluates them in order and **the first failure is the one reported**.
Reporting the last, or all of them, buries the thing to fix.

Four ways this refuses rather than passing quietly:

- a predicate that is false → the caller gets its `message`, as a `400`
- a predicate answering anything but `true`/`false` → a mapping fault, **not**
  permission. A typo yielding nothing would otherwise wave through every request
  the check existed to stop.
- a predicate with no `message` → a mapping fault. Refusing without saying why
  is the failure this key exists to avoid.
- a predicate that will not compile → its own error, and it does not take the
  halves down with it

Each predicate is a separate expression, so each binds its own variables — there
is no shared scope with the halves.

**A mapping declaring no `required` imposes nothing.** That is deliberate: it lets
one provider adopt preconditions while others have not, so a second provider
arriving with its own rules does not force every existing mapping to be rewritten.

The consequence is worth stating plainly: **nothing in the adapter enforces a
payload rule any more.** Whatever a mapping does not require, it accepts. That is
the point — the rule is configuration — and it is why the responsibility sits in
the published file.

Which action a file serves is settled by the registry entry pointing at it, so
nothing inside names it and the filename carries no meaning to this plugin. (The
registry contract does require the filename's action segment to match the action
it sits under — that is checked where the records are written.)

### References

The registry carries the full URL of one published file, and this plugin fetches
it verbatim. Anything that is not a fetchable `http`/`https` URL — a bare path, a
`file://`, a URL with no host — is refused: a reference is external input, and an
unchecked one would let a registry record name a local file and have the adapter
read it.

**What that check cannot do is constrain which host.** A registry record chooses
that, and this plugin fetches, *compiles* and runs what comes back. So who may
write a registry record is part of this plugin's threat model. (A reference
carried as a path under an operator-configured root would close that off; the
network has not settled on a fixed location for published mappings, so the URL
stays in the record for now.)

## What a mapping can read

| key | request leg | response leg |
|---|---|---|
| `beckn` | the inbound Beckn payload | the inbound Beckn payload |
| `response` | — | the provider's raw answer |

**What a party sent, and nothing else.** Values a provider plugin resolved before
the call are deliberately not passed in: the plugin holds them and used them to
make the call, so a mapping reading them back would be a detour and a second name
for the same data. Where the answer needs such a value, it takes it from what the
provider echoed.

## Why the direction is a parameter, not a convention

The caller makes one round trip and needs both halves of it, and nothing in the
file distinguishes them by position. Passing the direction explicitly is what
keeps a response mapping from ever being applied to an outbound request —
which would succeed quietly and produce nonsense.

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
