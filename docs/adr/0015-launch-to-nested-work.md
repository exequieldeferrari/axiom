# 15. Relating a launch to the nested work that reported its identity

- Status: accepted
- Date: 2026-08-12

## Context

ADR 0014 gave a turn two subagent counts and refused to connect them: how many
calls handed work to a nested agent, and how many calls a nested agent made.
Nothing in the event model related one to the other, so the report said so in
as many words.

It also recorded why: `tool_response.agentId` would join the two exactly, and
Axiom did not persist it, because the adapter read `tool_input` and never
`tool_response`. That was deferred to its own change.

This is that change, and the question it answers is one sentence:

> Which recorded tool calls reported the agent identity that a recorded launch
> returned?

It is not an execution graph, it is not subagent cost attribution, and it is
not a claim about what a nested agent did. It is an identity match on a handle
the agent itself put on both records, and every decision below exists to keep
it that small.

## Evidence

From a controlled capture in an isolated Claude Code project, replaying twelve
launches across seven runs: synchronous, parallel, background, failed, one that
recorded no nested work, one against a custom agent type, and one interrupted
mid-flight. Raw hook payloads were preserved beside the events Axiom derived
from them, and the two were cross-referenced record by record.

**`tool_response.agentId` and the nested calls' `agent_id` match exactly.** Every
nested call recorded during a launched agent's lifetime carried, in the hook
field that becomes `Event.SubagentID`, the identity that launch's response had
returned. The match was exact in both directions, with no near-misses and no
prefixes. This confirms ADR 0014's reading on a much larger capture.

**The identity is unique within a session and is not known to be beyond one.**
No identity was reused within a session across any run. Nothing in the payloads
establishes global uniqueness, and Axiom's existing rule is that an agent's
identifiers mean nothing outside the session that issued them.

**Turn identifiers do not bound a nested agent.** A background agent still
running when the next prompt began recorded work under a turn identifier other
than the one its launch was recorded under. A relation keyed by turn would have
lost that work, or worse, kept it under the wrong turn.

**Two response shapes carry the identity, and only two fields are common.** A
synchronous launch returns `status: "completed"` with `content`, `usage`,
`totalTokens`, `toolStats` and `resolvedModel`. An asynchronous one returns
`status: "async_launched"` with `description`, `outputFile` and no result at
all. `agentId` appears in both. `agentType` appears in only one.

**Asynchronous launches happen without being asked for.** Claude Code launched
agents asynchronously in runs where `run_in_background` was absent from
`tool_input` entirely. Axiom cannot predict from the input which mode a launch
will take, so nothing may depend on knowing.

**Order establishes nothing.** A synchronous launch's nested calls are recorded
*before* the launch, because a hook sees a call only once it has returned. An
asynchronous launch's arrive after. Two parallel agents interleaved their work
between the two launches that started them, and completed in the opposite order
from the one they started in.

**A failed launch returns no response.** A launch against an agent type that did
not exist produced `PostToolUseFailure` with no `tool_response` at all, so there
is nothing to record and no identity to match on.

**A launch can record an identity and no nested work.** An interrupted agent,
and an agent asked for something that needed no tool, both produced a launch
with an identity that no recorded call ever reported.

**Nested work exists with no launch to relate it to.** Any log that begins after
a launch holds it, and so does every log written before this field existed.

**The usage stream carries no agent identifier.** It carries the session, the
turn, the invocation and the measurements, and nothing that names an agent.
There is no join from a launch to a model request, in either direction.

## Decisions

### The identity is persisted on the result side, and is not `Event.SubagentID`

`ToolCall.Result.Subagent.AgentID` is added, optional throughout and
`omitempty` at every level.

It is deliberately not `Event.SubagentID`. The two describe different agents on
the same record: `Event.SubagentID` names the agent that **made** a call, and
`AgentID` names the agent a call **created**. A nested agent launching another
carries both at once, with different values, and the capture produced exactly
that record. Reusing one field for both would relate an agent's work to itself.

It is also not `ToolMetadata`. Metadata is derived from what a tool was given;
this is derived from what a tool returned. Keeping the provenance legible from
the field's position is what lets a future strict mode drop one without
dropping the other, and what stops "the type the agent declared" and "the
identity the agent returned" from being read as one kind of fact.

Absence means the identity was not recorded. It never means there was no agent.

### The adapter reads one field of one tool

`extractResult` reads `tool_response.agentId`, for `Agent` and `Task` only, and
only as a string. Anything else — a missing response, a response that is not an
object, an `agentId` that is a number, an object, `null` or empty — leaves the
identity absent.

The tool names come from the same constants the metadata allowlist uses, so a
third name cannot be learned by one and not the other.

Nothing else in the response is read, and the response never reaches an event,
even as raw bytes. `prompt`, `content`, `description`, `outputFile`,
`agentType`, `resolvedModel`, `usage`, `totalTokens` and `toolStats` are all
present in captured responses and all left where they are.

Presence of an identity is not read as success. A response is read whatever the
record says became of the call, because an identity that was returned was
returned; the call's outcome is recorded separately and neither is derived from
the other.

### The key is (session, agent identity), and never the turn

`internal/delegation` relates a launch `L` that returned identity `A` in session
`S` to exactly the recorded tool calls in `S` whose `Event.SubagentID` is `A`.

The session is part of the key because an identity is the agent's own and is not
known to mean anything outside the session that issued it, which is the rule the
rest of Axiom already follows for turns.

The turn is deliberately excluded. The capture holds a nested agent whose work
named a different turn from its launch, so a turn-keyed relation would be wrong
on evidence that already exists. The turn a launch was recorded in is kept as
provenance, because it is where the launch is displayed, and it takes no part in
matching.

No timestamp, append position, tool name, subagent type, duration or proximity
is consulted. There is no heuristic to get wrong.

### The relation is order-independent by construction

Records are accumulated as they are read and the relation is resolved when the
report is taken. Launches and nested work are collected independently, so
neither waits on the other, and the answer is identical whether the launch was
recorded first, last, or between the calls it names.

This is not defensive: all three orders are in the capture, and two of them are
the ordinary behaviour of synchronous and asynchronous launches.

### Three states, held apart

A launch is reported in one of three states, and no two may be read as each
other:

1. **No returned identity recorded.** Every launch written before this field
   existed, and every launch that reported failing. Axiom has nothing to match
   on. It is rendered as `Work == nil`, not as an empty measurement.
2. **Identity recorded, nothing reported it.** A launch whose identity no
   recorded call carried. It is a statement about the log: a call rejected
   before it ran is never recorded, and a log can end before the work reaches
   it. It is never rendered as an agent that did no work.
3. **Identity recorded, work reported it.** The relation, with the attributable
   calls composed by shape.

Nested work matching no recorded launch is counted at the end of the section,
with the number of identities behind it, and is never given to a launch nearby.
Two parallel agents interleaving their work in one turn is in the capture;
proximity would have attributed half of it to the wrong agent.

### Nested launches need no special case

A launch made by a nested agent is both a call that agent made and a launch that
created another. It is counted as the outer launch's attributable work and
related to its own nested work, with no recursion and no hierarchy invented
beyond what the two identity matches establish. The report renders no tree.

### A launch is recognized from metadata, exactly as a turn recognizes one

A record carrying a returned identity but no `metadata.subagent` is not promoted
into a launch. The evidence that a call was a launch is what ADR 0014 said it
was, and recognizing one here on different grounds would let this section hold a
launch the turn above it does not.

### The classifier is extracted rather than duplicated a third time

ADR 0014 kept `profiler.shapeOf` and `turns.shapeOf` duplicated and held them to
a table, and said explicitly that a fourth consumer would be the time to
reconsider. Composing delegated work is the third, and a third copy of a rule is
where the rule stops being one.

`internal/work` now holds the shape classification, the outcome counting and the
composition. It depends on the event model and nothing else, so `internal/turns`
and `internal/delegation` share it without either taking on the analysis that
judges the work. The profiler keeps its own, because its categories are a
different set and it is the judging analysis; the drift table in
`internal/profiler` now pins the profiler against the shared classifier, which
is the same seam it was pinning before.

### No schema change, and no backfill

`event.SchemaVersion` stays 1. The field is optional and additive, historical
records decode unchanged, and re-encoding one does not add it.

Nothing is reconstructed for a record that never held an identity. A historical
launch stays a launch, in state 1 above, forever.

### Consumption attribution stays out

Nothing joins a model request to an agent. A synchronous response does report
`totalTokens` and a `usage` block, and a background one reports neither; a
per-launch total taken from the first would be a measurement that exists for
half the launches and double-counts what a turn's observed consumption already
holds. ADR 0013's and ADR 0014's positions are unchanged.

### No generic graph

There is no node type, no edge type, no traversal and no `internal/graph`. The
package exposes one relation because one relation is what the evidence
establishes. A second relation, if one is ever established, can be built beside
it rather than fitted into a framework built before it existed.

### What is deliberately not here

No agent identity in the output, no per-launch consumption or cost, no duration
attribution, no nesting depth, no tree rendering, no ordering claims, no
completeness claims, no quality or efficiency judgement, no separate report
section, no schema version bump, and no change to `internal/profiler`,
`internal/activity`, `internal/reacquire`, `internal/correlate` or
`internal/timeline`.

## Consequences

A turn that delegated now names each launch that returned an identity and says
what the calls reporting it were, in the same operation vocabulary the turn
above uses. The turn's own counts are unchanged.

The claim on the page is exact and narrow, and the section states it: these
calls reported the same identity the agent returned for this launch. Nothing
about completeness, causation, cost or usefulness follows from it, and the
wording is tested against each of those readings.

The per-launch counts do not add up to *Calls by a nested agent*. That count
includes nested work no recorded launch accounts for, which is reported
separately. A reader who adds the launch lines together and compares gets a
smaller number, and the section says why.

Axiom now reads a tool's response, which it never did before. The boundary is
one field of one tool, extracted into one opaque string, and a regression test
asserts that a response carrying secrets in `prompt`, `content`, `description`
and `outputFile` persists none of them.

Only records written from now on can carry an identity. Every existing log keeps
exactly the report it had, plus one line per turn saying that its launches
carried no identity to match on.

A launch's ordinal within a turn may skip a number, where a launch between two
described ones returned no identity. The count of those launches is on the line
below, which is what the gap refers to.

A launch recorded without a turn identifier is related normally and displayed
nowhere, because the section displays turns and there is no turn to display it
under. It is already counted in the section's existing line for recorded calls
that named no turn. Giving it a home of its own would mean inventing a turn for
it, which is exactly what ADR 0013 refused to do.
