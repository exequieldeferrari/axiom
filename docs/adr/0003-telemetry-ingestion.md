# 3. Telemetry ingestion

- Status: accepted
- Date: 2026-08-11

## Context

Axiom records what an agent *did*. It cannot see what that behavior *cost*:
hooks carry no tokens, no cost, and no measure of how much a tool result added
to the conversation. The long-term goal is to correlate the two, so that a
repeated read can be reported with the resource consumption it actually caused
instead of an estimate.

Claude Code can export that measurement over OpenTelemetry. This milestone is
about receiving it and nothing else. Correlating it with behavior is a separate
problem, and doing it badly would produce exactly the confident, unfounded
numbers Axiom exists to avoid.

The decisions below were made against a real export captured from Claude Code
2.1.226, not against the specification.

## Decisions

### Logs only, over HTTP/JSON

Claude Code exports three signals. Metrics are pre-aggregated counters, so they
cannot be attributed to an individual tool call or turn. Traces are documented
as beta and carry no measurement the log records do not already have. The log
records carry per-event measurements with the identifiers Axiom needs, and they
are stable. So Axiom receives logs and refuses everything else at the door.

The transport is OTLP/JSON over HTTP, decoded with the standard library. The
alternative was the official OpenTelemetry Go packages, which would have brought
a protobuf runtime, a metrics SDK, and their transitive dependencies to parse
a payload Axiom itself configures the exporter to send. The decoder is under two
hundred lines because it only decodes what it is willing to store.

This means Axiom is not a general OTLP collector, and should not become one. It
receives one signal, in one encoding, from a producer it configured.

### The wire format is not what the specification suggests

The OTLP JSON mapping specifies 64-bit integers as quoted strings. The captured
export does not do that consistently. In a single batch, `duration_ms` arrives
as a bare JSON number on `api_request` and as a quoted string on `tool_result`,
and `tool_result_size_bytes` is quoted while `cache_creation_tokens` is not.

A decoder that trusted either form alone would silently lose half the
measurements while still returning valid-looking records. Attributes are
therefore normalized to strings on decode, and converted by the consumer that
knows what a field means. That is also why a missing attribute is reported as
absent rather than zero: `Attrs.Int` returns a second result, and every optional
measurement in a usage record is a pointer.

### Two streams, deliberately independent

Usage records go to `usage.jsonl`, next to but separate from `events.jsonl`.

The two have different writers and different lifetimes. Events are written by
short-lived hook processes on every tool call; usage records are written by a
receiver that only exists while someone is running it. Mixing them would mean a
profiler could no longer tell "this session consumed nothing" from "nobody was
listening", and a fault in one writer could corrupt the other's history.

Both files are read and written by the same generic store, so the append and
scan guarantees — the size limit, the single-write append, the counting of
malformed, truncated, and unknown-schema records — apply identically to both.

`axiom observe` never opens the event log. Its output describes each record from
its own fields only. Telemetry does not name the tool on every event, and
looking the name up in the behavior stream would create precisely the coupling
this milestone is trying to keep out until correlation is designed properly.

### The privacy boundary is an allowlist of thirteen attributes

Every record Claude Code sends carries the developer's email address, their user
and account identifiers, their organization identifier, and terminal details.
The `user_prompt` event carries a `prompt` attribute, redacted by default and
populated in full the moment anyone sets `OTEL_LOG_USER_PROMPTS`.

Axiom reads thirteen named attributes and never iterates the rest, so a content
attribute that appears in a future version, or one a user enables deliberately,
cannot reach the store. Resource-level attributes are dropped entirely rather
than merged into records, because that is where machine and service identity
live.

Axiom never writes `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_ASSISTANT_RESPONSES`,
`OTEL_LOG_TOOL_DETAILS`, `OTEL_LOG_TOOL_CONTENT`, or `OTEL_LOG_RAW_API_BODIES`.
A profiler that needs prompt text to work is a profiler with the wrong design.

### Configuration is explicit, per-signal, and refuses conflicts

`axiom init` does not touch telemetry. `axiom init --telemetry` does, and only
then.

Hooks are passive: installing them changes what Axiom sees. Telemetry
configuration changes where the user's data goes, which is not something to do
as a side effect of an unrelated command.

Only the per-signal variables are written. The generic `OTEL_EXPORTER_OTLP_*`
variables apply to every signal, so setting them would redirect metrics and
traces that Axiom neither receives nor understands. If any of them is already
present, or if a logs variable is set to something else, the install refuses and
writes nothing at all — including the hooks. Someone with a corporate collector
already configured has made a decision, and quietly layering on top of it is
worse than asking them to resolve it.

### A foreground process, and dropped telemetry when it is absent

`axiom observe` runs in the foreground until interrupted. No daemon, no launchd,
no service installation. The export path is fail-open by construction: Claude
Code's exporter drops what it cannot deliver, so when Axiom is not listening the
telemetry is simply lost, and nothing about the session changes.

That makes the usage stream inherently partial, which is the honest trade for
not installing a background service on someone's machine. It is also why a
missing usage record can never be read as a measurement of zero.

## Consequences

- Usage records exist only for the time a receiver was running. Any future
  analysis has to treat their absence as unknown, and Axiom's schema is built
  so that absence is representable.
- The two streams cannot be joined yet. `tool_use_id` and `prompt.id` are
  preserved on every usage record specifically so that a later milestone can do
  it, but nothing reads them today.
- Axiom accepts telemetry from anything that can POST OTLP/JSON to the port,
  including a producer that is not Claude Code. Records are mapped by Claude
  Code's own event names, so a foreign payload produces nothing. A second agent
  will need its own mapping, which is where agent neutrality belongs.
- Claude Code's own numbers are taken as reported. The cost figure is the
  agent's estimate, not a billing figure, and the field name says so.
- Nothing is aggregated, attributed, or turned into a saving. `axiom observe`
  prints what it recorded and stops there.
