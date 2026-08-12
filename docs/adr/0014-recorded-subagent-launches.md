# 14. Recorded subagent launches

- Status: accepted
- Date: 2026-08-12

## Context

ADR 0013 gave Axiom the recorded turn, and left one thing in it unexplained: the
calls that hand work to a nested agent were counted as work this version cannot
describe. It attributed that to the Claude adapter and deferred the fix to a
change that would gather the evidence properly.

This is that change. The evidence says the adapter was never the problem, and
that the report has been contradicting itself.

The question here is narrow and is not subagent cost attribution:

> What recorded work did a turn hand to a nested agent, and what work did a
> nested agent do in it?

## What was observed

A controlled capture, Claude Code 2.1.228 on macOS, in an isolated data
directory and a scratch project, with Axiom installed through the real
`axiom init --telemetry` flow and an OTLP receiver running. A second hook
handler wrote every payload verbatim before Axiom saw it, covering `PreToolUse`,
`SubagentStop` and `Stop` as well, which Axiom does not install. Five
non-interactive runs produced 8 `Agent` calls: two launched in parallel in one
turn, three sequential, one launched in the background, one type defined for the
run, one deliberately invalid type, and two single launches.

**`subagent_type` is supplied, on every launch.** `tool_input` carries
`description`, `prompt`, `run_in_background` and `subagent_type`. All 8 launches
carried it, including the one that failed. ADR 0013's claim that the field is
absent is false.

**The adapter records it correctly.** All 8 reached `events.jsonl` carrying
`metadata.subagent`, with the type verbatim.

**The report contradicted itself about the same 8 calls.** *Observed operations*
counted them as `Subagent 8`. *Recorded turns* counted them as
`Uninterpreted 8`, in the same run, over the same log. `internal/turns` had no
case for `metadata.subagent`.

**A failed launch launches nothing and still declares a type.** An invalid type
produced `PostToolUseFailure`, no `tool_response`, and
`subagent_type: "nonexistent-agent-xyz"` in the input. The declaration is not
the event.

**Type is an open string.** A type defined for the run appeared verbatim in both
`tool_input.subagent_type` and the nested calls' `agent_type`. There is no
enumeration to report.

**`tool_response.agentId` matches the nested calls' `agent_id` exactly.** With
two parallel agents in one turn the pairing was unambiguous in both directions,
and it held again in the sequential and background runs. This is a real join,
not a resemblance. `tool_response.agentType` is not usable for anything: a
background launch omits it.

**Nothing joins a launch to a model request.** The usage stream carries the
session, the turn, the invocation and the measurements, and no agent identifier
at all. In these captures the nested agents' model requests carried the parent's
turn identifier, pooled with the parent's own.

**Order does not bound anything.** A synchronous launch's nested calls are
recorded *before* the launch itself, because a hook sees a call only once it has
returned. A background launch's arrive *after*. Two parallel launches completed
in the opposite order from the one they started in, with both agents' nested
work interleaved between. A parent call's duration bounds its nested work in
neither direction.

## Decisions

### A launch is recognized from metadata, never from a tool name

`internal/turns` classifies a call as a launch when `metadata.subagent` is
non-nil, and on nothing else.

The tool's name is the agent's vocabulary rather than Axiom's, and it has
already changed once: the adapter's allowlist matches both `Agent` and `Task`
because Claude Code has shipped this tool under both. Recognition belongs where
the adapter already put it, and every analysis downstream reads the derived
shape. `internal/profiler` and `internal/activity` already did exactly this.

Nothing is read from `description` or `prompt`. Neither is persisted, and
neither is consulted.

### A launch carries the three-way outcome

`Composition.Launches` counts launches as succeeded, reported failing, or
outcome not established, using the same `Outcomes` type and the same rendering
as writes and edits.

The three states answer a different question here than they do for a write.
There, the outcome qualifies what may have persisted. Here it qualifies whether
the delegation happened at all: a launch call reported failing started no nested
agent, and one whose outcome was never established is evidence neither way. The
capture produced the failing case on the first attempt, so this is not a
defensive distinction.

Reusing the existing vocabulary is deliberate. A second way of saying "reported
failing" would read as a second kind of failure.

### A launch is not nested work, and neither is evidence for the other

A turn now reports two quantities that were previously one number and one
mislabel:

- **Subagent launches** — calls that declared work handed to a nested agent.
- **Calls by a nested agent** — calls carrying `Event.SubagentID`, which is the
  work a nested agent did. This is `Turn.SubagentCalls`, unchanged in meaning
  since ADR 0013 and previously labelled `Subagent calls`, which read as a count
  of launches.

Neither is derived from the other, and the report says so next to them. Each
occurs without the other in real logs: a nested agent need not call a tool, and
a log that begins mid-turn holds nested calls whose launch is not in it, because
the launch is recorded only after its call returns. Nothing recorded relates a
particular launch to a particular nested call, and the report never implies one.

### The type is recorded and not reported

`metadata.subagent.type` stays exactly as it is persisted, and nothing renders,
groups, ranks or counts it.

Three reasons, each sufficient. It is an open string, so a type breakdown has
unbounded cardinality. Values are author-defined, so rendering them puts a
user's own names in a report that otherwise holds no free text. And a failed
launch carries one too, so a count by type would count delegations that did not
happen.

### No schema change, and no backfill

Nothing about the event model changes. `ToolMetadata.Subagent` and
`ToolCall.Outcome` have both been persisted since ADR 0001, and the launch
classification is derived from them at read time.

Historical compatibility is stated conservatively, because the evidence for it
is partial. A record that already carries `metadata.subagent` gains the new
classification automatically, with no migration. A record without it stays
uninterpreted, which is what it has always said. Axiom does not claim that every
historical `Agent` record carries the metadata: the log available for checking
held 29 events and 25 tool calls with no subagent operations in it at all, so it
could neither confirm nor falsify the question, and ADR 0013's account of an
earlier capture is exactly the reading this ADR corrects. Nothing is backfilled,
and no record is assigned information it never contained.

### Launch to nested work is deferred, with the evidence recorded

`tool_response.agentId` would join a launch to the nested calls it produced, and
the capture established that the join is exact. Axiom does not persist it: the
adapter reads `tool_input` and never `tool_response`, so the relation is
available in the protocol and absent from the event model.

It is left for its own change. Persisting it is a new optional field and a new
question — what a launch's own work consisted of — and it would only ever
describe records written after it, which is a limit worth introducing
deliberately rather than alongside a classification fix.

### Consumption attribution stays out

Nothing recorded joins a model request to a launch. The usage stream carries no
agent identifier, and the nested agents' requests were observed carrying the
parent's turn identifier, so they are already inside a turn's observed
consumption rather than missing from it.

A synchronous launch's `tool_response` does report `totalTokens` and a `usage`
block, and that is a direct measurement rather than a join. It is still not
enough: a background launch reports neither, and a per-subagent total presented
beside a turn's observed consumption would double-count what is already in it.
ADR 0013's warning that a turn's consumption is not a subagent's total stands
unchanged.

### The two classifiers are held to a table rather than merged

`profiler.shapeOf` and `turns.shapeOf` describe the same shapes and are
duplicated deliberately: `internal/turns` may depend only on the event model and
the timeline, and importing the findings package to count reads would tie a
measurement to the analysis that judges it.

That duplication drifted, and the drift is the defect this ADR fixes. The answer
is not to merge them. The fix is three lines; extracting a shared classifier
would touch the profiler, turns, activity and their tests, and would dominate
the change it was meant to protect. A shared classifier would also not have
prevented this: the omission was deliberate and documented, and it would have
been made in the shared rule just as readily.

Instead a test in `internal/profiler` holds both to one table of metadata
shapes, driving each through the public behaviour that already reaches it — a
finding's interval on one side, a turn's composition on the other. Neither
function is exported, and nothing was added to production for the test. It fails
on the exact defect that prompted it.

If a fourth consumer appears, or if the two must diverge for a reason, that is
when to reconsider.

### What is deliberately not here

No agent types, no launch-to-nested-work attribution, no persistence of
`tool_response`, no per-subagent consumption, no prompt or description, no
inferred purpose, no change to `internal/activity`, `internal/profiler`,
`internal/reacquire` or `internal/timeline`, no schema version change, and no
new report section.

## Consequences

A turn's composition is now consistent with the rest of the report: the same
recorded call is counted the same way in every section that counts it.

The counts still reconcile exactly. Every recorded call falls in exactly one
category, and launches are part of that sum rather than beside it.

A reader can now overcount in one new way, and the wording is what prevents it:
the two subagent lines are not halves of one quantity, and adding them together
counts nothing meaningful.

`internal/activity` counts a failed launch in its `Subagent` bucket, which this
change deliberately leaves alone. That bucket partitions recognized operations
by shape and carries no outcome model at all, so giving it one is a change to
what its categories mean, and belongs to whatever revisits ADR 0007 rather than
here.

The report's outcome rendering can run past the width the report is written to
when a turn qualifies both non-successful states on one line. Writes and edits
have done this since they gained outcomes; the length comes from the shared
rendering and not from any label. Narrowing it is a change to that convention
everywhere it is used.
