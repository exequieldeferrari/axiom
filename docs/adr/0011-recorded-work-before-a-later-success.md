# 11. Recorded work before a later success

- Status: accepted
- Date: 2026-08-12

## Context

ADR 0006 gave `repeated_failure` a `LaterSuccess` flag: the same command, in the
same scope, observed succeeding after a sequence of failed attempts. It answers
one bit. Everything the agent did between the two observations was discarded.

That leaves two very different executions rendered identically. One agent read
two files, searched, edited a file and tried again. Another tried again with
nothing recorded in between. Both print `Same command later succeeded yes` and
stop. The operations exist in the same log — the activity profile counts them —
but nothing relates them to the gap they fall in, which is the same shape of gap
ADR 0010 described for reads around a context boundary.

The question this ADR answers is narrow and deliberately not a diagnosis:

> What work did Axiom record between the last failed attempt of a
> repeated-failure finding and the first later observed success of the same
> command?

It is recorded execution ordering attached to an existing finding. It is not an
explanation of the success, and the wording rules below are most of the work.

## What was observed

Four controlled captures, all outside the repository, each with its own
`AXIOM_DATA_DIR`. Claude Code 2.1.228, hooks installed per scratch project via
`axiom init` into `.claude/settings.local.json`, headless
(`claude -p --dangerously-skip-permissions`, so that no call was denied — a
denied call produces no event at all). No event log was edited by hand; every
record was written by Axiom's own hook process. No telemetry receiver was
running in any capture, which is itself a result: the interval is derivable with
none.

These are controlled captures and not organic behavior. They establish that the
records exist and what the code does with them. They do not establish how often
any of these shapes occurs in real work.

**Case A — operations between the two observations.** Session `e2726f72`, one
epoch, one turn `4ac51400`. Two failures of one command (digest `89293e3bd6cf`,
failure digest `29a4a91cf7e0`, exit 1), then `Read check.sh`, four `ToolSearch`
calls, `Read status.txt`, `Edit status.txt`, then the same command observed
succeeding.

The four `ToolSearch` calls were not scripted. The prompt asked for a `Grep` and
no such tool existed in that session's toolset, so the agent hunted for one.
`ToolSearch` is a first-party Claude Code tool outside Axiom's metadata
allowlist, and all four were recorded with no metadata at all. Four of the seven
operations in the interval are calls Axiom cannot describe.

**The failure run did not close at the last attempt.** Reads are not barriers,
so the run stayed open through `Read check.sh` and closed at the first
`ToolSearch`. The finding was therefore created two events after the interval
begins, and the read at event 4 sits between them.

**Case B — nothing recorded between them.** Session `e9f31581`, one turn
`becd2f24`. Two failures of one command, then the same command observed
succeeding, with no tool call in between.

The command was a counter script that increments a file and exits non-zero until
its third run. `counter.txt` finished at 3. Real state changed twice between the
last failure and the success, deterministically, and Axiom recorded none of it,
because the command changed it rather than a tool call.

**Boundary 1 — a context reset between them.** Session `adf2d590`: two failures
in epoch 1, a session end, then `--continue` emitting a `session_start` with
source `resume` under the same session identity, then the success in epoch 2.
`Same command later succeeded` is absent. `Profiler.reset` deletes the scope, and
the bookkeeping that lets a later success reach an earlier finding goes with it.

**Boundary 2 — a turn boundary without a reset.** Session `12aaf654`, driven with
`--input-format stream-json` so that two user messages reached one session:
failures in turn `1f56b511`, success in turn `396e0da9`, no `session_start`
between them. `Same command later succeeded yes` is reported. A turn boundary
ends the failure *sequence* and leaves the scope alive.

This produced the combination that matters most: **an empty interval spanning a
turn boundary.** Twenty-two seconds and one instruction Axiom never saw separate
the last failure from the success. On operation counts alone it is identical to
Case B, where the agent tried again on its own. Those are not the same situation.

**Current output discarded all of it.** For Case A the profile printed the
finding and nothing about the seven operations; `Observed operations` showed
`File 3 · Shell 3 · Unrecognized 4` elsewhere in the report with nothing relating
them to the gap. Case A and Case B were byte-for-byte the same shape.

Boundaries not reproduced experimentally are marked code-derived below.

## Decisions

### It attaches to `repeated_failure` rather than becoming a finding

The interval has no existence without the finding. It is bounded at one end by
the finding's last attempt and at the other by the success already recorded on
it, and both endpoints come from semantics the profiler already owns: the
sequence identity, the `(session, subagent)` scope, the barrier rules, and reset.

A separate finding would have to be ranked against redundant work, which asks a
reader to compare an ordering against a defect. A separate section would have to
name the sequence it belongs to, and to re-derive enough of the profiler to know
which sequence that was. A separate package would have to re-derive all of it,
which is the duplication ADR 0010 declined to add.

So it is a measurement on the finding, with no confidence level and no ranking of
its own, printed as a subordinate block under it.

### Start is the last attempt; end is the first later success

The interval begins immediately after the **last recorded failed attempt** of the
sequence, and ends immediately before the **first observed success** of the same
command digest in the same `(session, subagent)` scope. Neither endpoint is
inside it.

**Start is the last attempt and not where the run closed.** These differ, and
Case A is the proof: the last attempt is event 3, the run closed at event 5, and
a read sits between them. Run close is the structurally obvious place to snapshot,
because it is where the finding is created — and it would have silently dropped
that read. The snapshot is therefore taken when an attempt is recorded, and each
further attempt replaces it, so the run carries the one the last attempt left.

Reconstructing the start later from timestamps was rejected. Membership follows
append order; recorded times are not monotonic and concurrent sessions
interleave, which is the same reason ADR 0010 gave for placing records as they
arrive rather than by comparing times afterwards.

**The interval freezes on the first success.** A success patches every sequence
of that command awaiting one, and the profiler observes every later success too.
Without an explicit freeze, a second success would replace the interval with a
longer one that spans the first. The first success is the observation that bounds
the interval, so the sequences it froze are dropped from the scope's bookkeeping
and no later success can reach them.

### A context reset ends the scope, and that is left exactly as it is

Boundary 1 is empirical: a success after a reset does not become `LaterSuccess`
and produces no interval. `Profiler.reset` closes the runs and deletes the scope,
which takes the open intervals with it.

Nothing about reset changed. After a reset the agent's context may legitimately
no longer hold what it held before, so a success on the far side of one is not
the same agent getting past the same failure. The interval inherits that bound
for free, which is the main argument for extending the profiler rather than
building beside it.

### Every recorded call is accounted for, by the shape it carried

The composition partitions the calls: whole-file reads, ranged reads, searches,
shell commands, writes, edits, subagent calls, and calls Axiom cannot describe.
The categories sum to the operation count, and a test asserts it, so a reader can
reconcile the block against itself and a category added later without being
counted cannot pass unnoticed.

**Unrecognized calls are first-class.** Four of the seven operations in the only
real interval captured were `ToolSearch` calls with no metadata. Dropping calls
this version cannot interpret would have described that interval as three
operations while looking complete. They are counted for the same reason
`activity` has an `Unrecognized` bucket.

**Shape is independent of outcome, except for writes and edits.** A count of
reads is a count of read calls that reached the log, not of files the agent can
be shown to have obtained; the record says what became of a call and never what
it returned. Writes and edits carry their outcomes because what they leave behind
is the part of an interval that could have persisted, and the three states are
kept apart on ADR 0007's terms: an outcome that was never established is not a
failure, and a call reported failing may still have applied in part.

A write the agent reported succeeding is not a claim that the resulting file
state is known. That is unobservable in both directions, exactly as ADR 0010
found for its later write or edit.

**A background command is counted as a command.** Its effects are unbounded,
which is why it is a barrier, but that is a statement about what it could have
done rather than about what it was.

**Command text is never inspected.** Shell commands appear as a count. The
command-digest privacy model is unchanged, and the interval reads no field the
profiler did not already read.

**Further attempts of the same command fall inside the interval.** The interval
ends at the first success, not at the next attempt, so where a second sequence
of the same command occurs in between, the first sequence's interval contains
the second's attempts and the second's interval is empty. Intervals nest
(code-derived, and covered by a test).

### The turn boundary is reported as an ordering limit, in three states

Not as "turns spanned", which would be a performance metric over something that
measures no performance. The record establishes one of three things:

- no turn boundary was recorded;
- one or more were;
- the question was never settled, because a call carried no turn identifier.

The third is not the absence of a boundary and is reported ahead of the second,
because answering from the calls that happened to carry an identifier would state
more than the record does.

**The question is asked of a closed span, and both of its ends are in it.** The
span runs from the last attempt, through everything recorded after it, to the
first later success. The two endpoints' own turns are part of the comparison —
Boundary 2 is precisely a boundary between an attempt and a success with nothing
recorded between them — and no call outside the span takes part in it.

It follows that a missing identifier counts the same wherever it falls, including
on either end. This is stated as a rule rather than left to the mechanism,
because the two ends are not reached by the same code path and an implementation
can easily treat them differently without anyone noticing.

The accounting is therefore per **transition** between consecutive recorded
calls, not per call. A transition where both calls reported a turn and the turns
differ is a boundary; one where either reported none is unsettled. The start
snapshot is taken after the attempt, so the transition out of it is counted, and
the interval freezes at the success, so the transition into it is counted — which
is exactly the closed span and nothing else.

Counting unsettled turns per *call* instead would have made the ends behave
differently: the attempt's own missing identifier would fall inside the start
snapshot and cancel out, while the success's would not. Today that difference is
unreachable, because an attempt with no turn identifier cannot join a sequence of
failed attempts at all, so an interval never starts at one. Relying on that would
have made a rule about turn boundaries depend on a guard in the failure-sequence
code three functions away. A test pins the guard, and the transition model makes
the rule hold whether or not the guard survives.

A call with no turn identifier is never compared across the gap to the nearest
call that did report one. Those two were not adjacent, and treating them as
though they were would answer the question from evidence the record does not
hold.

A recorded boundary does **not** establish that human input caused the later
success. What it establishes is that input Axiom does not observe may have
arrived, which is the same reason ADR 0006 confines a failure sequence to one
turn. Boundary 2 is why this is reported at all: without it, that capture and
Case B are indistinguishable.

The boundary is evaluated across the endpoints and not only across the calls
between them. Boundary 2's interval is empty and the boundary still falls inside
it, because the last attempt and the success reported different turns.

### An empty interval is rendered explicitly, and means only what it says

It means: Axiom recorded no tool operation between the last failed attempt and
the first later observed success.

It does **not** mean the command is flaky, that a retry succeeded, that timing or
external state was involved, that no work occurred, that nothing changed, or that
the same execution state was tried again. Case B is the counterexample to all of
them at once: `counter.txt` advanced 1 → 2 → 3, so state changed twice,
deterministically, and the command was flaky in no sense. The discovery report's
claim that an empty interval is directly actionable is withdrawn.

The empty case therefore reads as a fact about the log, immediately followed by
the two ways the log is not the execution: a call rejected before it ran is never
recorded, and a command can change state that no tool call reports.

### Write and edit paths are retained exactly, and bounded

Only writes and edits. Read paths are not listed in this change — the interval
would become a second activity profile, and the paths that matter for what could
have persisted are the ones a call wrote to.

Paths are the exact recorded strings. No normalization, as everywhere else in
Axiom: ADR 0007 and ADR 0010 both observed `/tmp` and `/private/tmp` aliases in
real captures, and keeping two names apart can only fail to report a path, never
invent one.

Retention is bounded at five distinct paths per interval. Beyond that the count
of distinct paths left out is reported rather than silently truncated, and the
operation counts stay complete, so the bound costs detail and never accuracy.
What is retained is the set of paths, not the calls that named them, so what
grows with a long interval is the number of distinct files written.

### No bytes, tokens, cost or duration

Not for soundness reasons. Tool-result bytes and durations are recorded per call
and could be summed over an interval.

The reason is what the number would say. A size or a duration printed inside a
block that sits between a failure and a success reads as the price of getting
past the failure, and that is a causal claim about work none of which is
established to have made the difference. Tokens and cost are worse: they join at
`(session, turn)`, and Boundary 2 observed an interval spanning a turn, so they
cannot be divided across one by any evidence Axiom has. This is ADR 0010's
reasoning applied to a smaller window.

A shorter interval is likewise never described as better or more efficient. There
is no evidence that any of the work was needed, so there is none that less of it
would have done.

### No causal or diagnostic language, anywhere

The block lists work between a failure and a success. A reader supplies the
causal reading for free unless every line refuses it. Neither the CLI, the
README, this ADR, nor a code comment intended as product semantics may claim that
the intervening operations caused the success, fixed the problem, recovered the
execution, resolved the failure, unblocked the agent, explain the success, or
were needed, useful or wasteful.

The wording guard test is extended to reject `caused`, `root cause`,
`explains the`, `explains why`, `thanks to`, `flaky`, `flake`, `intermittent`,
`unblocked`, `trajectory`, `was needed`, `were needed`, `necessary`, `useful`,
`wasteful`, `efficient`, `efficiency`, `retry`, `retried` and `remediat`,
alongside the terms ADR 0006 already rejected. The capability is not called
failure recovery, recovery trajectory, a fix, a resolution, unblocking, or
flakiness analysis. It is called what was recorded before the later success.

### Extending the profiler, with a snapshot and a difference

The profiler already owns sequence identity, scope, barriers, reset and the
`LaterSuccess` association. A parallel package would have to re-derive all five.

Each scope keeps a running total of what it has recorded. An interval is the
difference between two copies of that total: one taken after the last attempt,
one taken at the success. Accounting is therefore constant per event and no list
of events is retained. The snapshot lives on the open failure run, so the finding
created at run close carries the start the last attempt left, which is what makes
the Case A ordering come out right.

No stored-event schema field was added, no telemetry is required, and no
dependency on `correlate`, `activity` or `timeline` was needed. The interval reads
the events the profiler was already given.

Interval accounting classifies a call separately from the barrier rules. The two
answer different questions, and one function cannot answer both: the barrier
rules deliberately treat a background command, a subagent and an unrecognized
tool alike, because their effects are equally unbounded, while an interval has to
tell them apart to describe what was recorded.

## A limitation this capture exposed, and does not address

The failure digest `29a4a91cf7e0` is identical across all four captures, which
are four different commands. `digest.Error` is an unsalted SHA-256 of the agent's
error text, so identical digests mean identical text: every command failed
silently with exit 1, and Claude Code reported the same string for all of them.

"Each reporting the same observed failure" — the evidence for `ConfidenceHigh` on
a `repeated_failure` — is therefore trivially satisfied for any command that fails
without output. The HIGH badge carries less discriminating power than ADR 0006's
capture, which used commands with distinct output, would suggest.

This is recorded as an observation and a candidate for future investigation. It
does not affect the interval, which is derived from ordering rather than from
digests, and neither confidence nor failure digests are redesigned here.

## Consequences

- `repeated_failure` gains a subordinate block, printed after the finding's own
  facts and before its measurements, because it comes from the same behavior
  stream the finding does. It has no ranking and no population of its own.
- Case A and Case B stop being indistinguishable. So do Case B and Boundary 2,
  which differ only in the turn boundary.
- Three states of the turn boundary are kept apart, on the principle ADR 0010
  used for its three empty states: an unsettled question must not read as a
  settled one.
- Reset behavior, barrier semantics, confidence and failure digests are unchanged.
  The interval is derived from records the profiler already had.
- The report now says, in the block and in the explanation below the findings,
  that none of the work listed is established to have made the difference. That
  sentence is the feature as much as the counts are.
