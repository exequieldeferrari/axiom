# 4. Measured tool impact

- Status: accepted
- Date: 2026-08-11

## Context

Axiom reports repeated work from the behavior stream, and records what an agent
measured in a separate usage stream. Until now nothing joined them, so a finding
could say a file was read three times but not what those repeats returned.

The join is worth doing because the size of a repeated tool result is a fact the
agent itself reported. It is not an estimate, and it does not need to be
converted into tokens or dollars to be useful. It is also the point at which
Axiom could most easily start inventing numbers, so the rules below are about
what the join refuses to do.

Paired captures from real Claude Code sessions motivated the design. They also
showed that the output of a repeated call is not predictable from the output of
the first: the same file read again returned a fraction of the original bytes in
one session and the whole file in another. That difference is not a documented
guarantee and Axiom does not depend on it, but it is the clearest argument
against estimating a repeat's cost from anything other than its own measurement.

## Decisions

### Identity is a session, a turn, and an invocation

A measurement is attached to an occurrence only when
`session_id + turn_id + invocation_id` match. Both streams carry all three from
the same agent-reported fields.

The composite is deliberate. An invocation identifier is only known to be unique
within the turn that produced it; nothing in the recorded evidence establishes
that it is unique across sessions. Joining on it alone would be relying on a
property Axiom has not verified, and the composite costs nothing.

Timestamps are never part of the identity. The two logs are written by different
processes with independent lifetimes, and captured pairs show their records
interleaving: a tool result can be written after the session that produced it has
already ended. Any proximity rule would be inventing a relationship the
identifiers already state exactly.

### Tool names are not part of the join

Agents redact tool names in telemetry — an MCP call can be reported under a
generic name while the hook payload carries the real one. The hook stream is
authoritative for what a tool was and what it did. Telemetry contributes a
measurement and nothing else, so a disagreement about names cannot affect
whether a measurement matches.

### The profiler records occurrence identity as it observes

A finding carries `Calls`, one entry per occurrence in the order observed.
`Calls[0]` did the work first and repeated nothing; `Calls[1:]` are the redundant
occurrences, and only those contribute to a measurement. Reporting the total for
all three reads of a file would attribute work that had to happen to a finding
about work that did not.

Identity is recorded while the run is being observed because nothing later can
recover it. Reconstructing which occurrence was which from timestamps is the one
approach that must not be taken, and the interleaving described above is why.

Findings remain complete evidence without identifiers. An agent that reports none
still produces findings; only the join is lost.

### Attribution is complete or absent

A measurement is reported only when every redundant occurrence in a finding was
matched exactly once and carried a size. If any of them is missing, ambiguous, or
sizeless, the finding reports no measurement at all.

A partial sum is worse than no sum. It looks exactly like a complete one and
understates the total without saying so. The same reasoning already governs
`ObservedTotal`, which is nil when any occurrence was recorded without a
duration.

Duplicate records make an invocation ambiguous rather than resolved. Two records
for one invocation may describe the same call twice or two different calls, and
choosing either would present a guess as a measurement.

### Model requests are not charged to tool calls

Token counts and cost are reported per model request, which covers a whole turn.
Nothing in the recording says how much of a turn's tokens a particular tool call
caused. Usage records for model requests carry no invocation identifier at all,
so the correlation index ignores them by construction rather than by discipline.

Turn-level analysis is a separate problem and is not attempted here.

### Telemetry stays optional

`axiom profile` behaves exactly as it did when no usage log exists, which is the
ordinary case: telemetry is recorded only while a receiver is running. A missing,
unreadable, or partially readable usage log leaves findings unmeasured instead of
failing the analysis.

The measured line is omitted rather than rendered as unknown. A finding that
cannot be measured says nothing about bytes, because a report full of "not
measured" rows would train a reader to ignore the column that matters. Records
the usage log could not parse are still reported as a warning, since those are a
lost measurement rather than an absent one.

## Consequences

- The number Axiom reports is the size of results the agent said it returned. It
  is bytes, not tokens and not cost, and the report says so where it appears.
- Most findings will carry no measurement, because most users will not have been
  running a receiver. This is expected and must never read as zero.
- Correlation is a separate package that depends on the profiler, not the other
  way round. The profiler gains no knowledge that telemetry exists, and no OTLP
  type reaches either package.
- A usage log that cannot be read in full is discarded rather than used in part:
  the record that would have made a measurement ambiguous may be exactly the one
  that was lost.
- Turn-level token and cost evidence remains unused. It is recorded and waiting.
