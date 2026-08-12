# 10. Reads across context epochs

- Status: accepted
- Date: 2026-08-12

## Context

ADR 0009 built a unit of analysis and nothing analyzed against it. The timeline
derived session identities and their context epochs, printed them, and stopped;
the profiler independently re-derived "a `session_start` is a boundary" for its
own resets. Axiom had a boundary and no analysis placed against one.

The gap that leaves is specific, and it is the profiler's own doing. `Profiler.reset`
ends every run at a recorded start, because after a reset the agent's context may
legitimately no longer hold what it held before, and repetition across one cannot
be called redundant. That is right, and it means the repetition that crosses a
boundary is exactly the class Axiom discards. The activity profile counts the
reads but aggregates over the whole log and knows nothing about boundaries. So a
file read once before a boundary and once after produced a line saying "2 reads"
and a findings section saying nothing, with no output anywhere relating the two.

This was verified rather than assumed, on the capture described below.

An earlier attempt at this territory was rejected. PR #8 investigated "context
rediscovery" and found that in the organic captures available, eight of nine reads
existed only because Claude Code's `Edit` tool refuses to modify a file that has
not been read in the current context. A detector built on read identity alone
would largely have been measuring a tool precondition. ADR 0007 recorded that
rejection. This ADR does not overturn it: it changes what is claimed, not what is
counted, and the wording rules below are the substance of the change.

## What was observed

Claude Code 2.1.228 on macOS, hooks installed in a scratch project, an isolated
`AXIOM_DATA_DIR`, headless (`claude -p`). Compaction was not forced; `--continue`
produces a `session_start` under an unchanged session identity, which is the
cheapest recorded boundary, and this analysis never branches on the source.

The existing organic capture on this machine held two session identities of one
epoch each, sharing nine read paths *between* the identities and crossing no
boundary within either. It produced no cross-epoch example at all, which is
itself the first observation: the log's most abundant repetition is across
identities, and it is exactly what this analysis refuses to report.

The controlled capture produced one identity, `a01159c5`, with five epochs:

| Epoch | Opening | Closing | Recorded |
| --- | --- | --- | --- |
| 1 | `startup` | session end (`other`) | `Read` notes.txt |
| 2 | `resume` | session end (`other`) | `Edit` notes.txt |
| 3 | `resume` | session end (`other`) | `Read` notes.txt |
| 4 | `resume` | session end (`other`) | `Read` other.txt, `Edit` other.txt |
| 5 | `resume` | session end (`other`) | `Read` other.txt, `Edit` other.txt |

Four observations mattered.

**Epoch 2 edited a file it never read.** The agent edited notes.txt with no read
of it in that epoch. A resume restores the conversation, so whatever precondition
the edit tool enforces was already satisfied, and the boundary produced no read at
all. This is a direct counterexample to the claim that a boundary makes a re-read
necessary, and it was recorded in the first capture attempted. Any wording that
called a read-then-edit pair "required" or "unavoidable" would already be
contradicted by the log it was built from.

**Every boundary here was a session end followed by a start, not a reset.** The
epochs are separated by `session_end` + `session_start` pairs. ADR 0009 already
warned that an epoch also ends where a session does, which discards nothing; the
capture makes that the ordinary case rather than the corner one.

**The path aliasing from ADR 0007 reappeared.** Every path was recorded under
`/private/tmp/...` while the shell had created them under `/tmp/...`.

**`axiom profile` on that log reported "2 reads" for notes.txt and no findings**,
with nothing connecting the two reads to the boundary between them. The gap this
ADR addresses was reproduced before it was closed.

## Decisions

### This is measurement, and it is not a finding

It follows ADR 0007's split. The profiler prefers false negatives and stays silent
unless the evidence rules the innocent explanations out; this counts what was
observed and rules nothing out. It therefore has no confidence level, no severity,
no score, and no recommendation, and it lives outside the findings section of the
report.

That is not a softer version of a finding. It is a different claim: a finding says
work repeated with no reason the record can offer, and this says two reads happened
in two epochs. The second is true whenever the reads are; nothing has to be ruled
out for it to hold.

### Cross-epoch repetition is deliberately not `repeated_read`

`repeated_read` requires one context scope and ends at every recorded start,
because after a reset the agent may legitimately need the file again. Extending it
across boundaries would delete the reason it is trustworthy.

So the two analyses answer different questions over the same records, and the seam
test asserts it in both directions: a path read on either side of a reset produces
no finding and exactly one relation here, and a path read twice inside one epoch
produces a finding and no relation. Neither is a weaker form of the other.

### Evidence rules

An acquisition is a **successful whole-file read** with a **non-empty recorded
path**, **not made by a subagent**, **placed in an epoch** by the timeline.

- Ranged reads are excluded, in both directions. A read of part of a file acquires
  something else, and comparing ranges adds a class of mistake the profiler
  already declined to take on.
- Failed reads are excluded. The record says what became of a call, never what it
  returned, so a failed read is not established to have delivered the contents.
- Reads whose outcome was never established are excluded on the same grounds, and
  are tested against `OutcomeSuccess` by name rather than as "not failure".
- A path is reported only when it was acquired in **two or more distinct epochs of
  one session identity**. Several reads inside one epoch are one acquisition; the
  count is carried, and the repetition itself is the profiler's subject.

### Path identity is the exact recorded string

No normalization, as everywhere else in Axiom. The alias observed again in this
capture means two names for one file stay apart — which can only fail to report a
relation, never invent one. ADR 0007's conditions for revisiting this are unchanged.

### Session identities are never compared

Not by adjacency and not by time. The organic capture is the argument: two
identities read nine of the same paths, and nothing recorded says they were one
sitting or two attempts at one task. Linking them would invent the relation the
report exists to state.

### Subagent reads are excluded, and counted

A nested agent reasons in a context of its own, and these epochs are the session's.
A reset of the session's context establishes nothing about a subagent, and a path
read by a subagent in one epoch and by the parent in another is not the parent
reading twice — the parent received a summary and never held the content.

They are counted in the report rather than dropped, so that work the analysis did
not look at does not silently disappear, on the same principle as `activity`'s
`Unrecognized` bucket.

### A later write or edit is an ordering of operations, and nothing else

For each acquisition, Axiom records whether a write or edit of **the same exact
path** was observed **after the first read of it, in the same epoch**.

That is the whole claim. Two recorded operations, and which came first.

**It is not interpreted as a change to the file.** The record establishes that a
call was made and what became of it. It does not establish what the call left on
disk, and a call the agent reported failing is recorded here alongside one that
succeeded. The field is therefore named `WriteOrEditAfter` and rendered as
"later write or edit recorded" — an earlier draft said "modified afterwards",
which asserted an outcome the record does not carry. Whether a file was left
different is unobservable in both directions, and the wording now says only the
part that is observable.

**It is not interpreted as necessity or causality.** Axiom does not say the read
was needed for the operation that followed it, that the boundary caused the read,
or that the tooling required it. Claude Code's edit tools do refuse to modify a
file that has not been read in the current context, and that is worth a reader
knowing — it is stated in the README as background about the agent, not as a
conclusion about a line in the report. The capture above is why the distinction
is enforced rather than assumed: epoch 2 edited a file it had not read in that
epoch, so the precondition plainly does not always produce a read.

**Absence is not interpreted as unnecessary work.** "No later write or edit
recorded" says none was recorded. It is not evidence that the read achieved
nothing, and the report says so where the phrase appears.

The report's wording is therefore part of the contract, and a test rejects
"waste", "unnecessary", "redundant", "forget", "memory loss", "context loss",
"rediscover", "required", "unavoidable", "explained", "unexplained", "caused by",
"cost of", "efficiency", "lost", "should", "recommend", "savings" and "modif"
from the section. Several of those are exactly what an earlier design would have
printed.

### Ordering matters, and only one direction counts

A write or edit recorded *before* any read in that epoch is not after the
acquisition and is not noted: it is a different observation. One recorded
between two reads of the epoch is after the acquisition, which begins at the
first read.

### A failed call counts; an unestablished one is held apart

A failure is still a recorded call whose outcome the record establishes, which is
the whole of what the flag claims. Reporting nothing for it would say no such
call followed the read, which the record contradicts — and a failed edit may
still have applied in part, which is why the profiler treats one as a barrier.
Both established outcomes therefore set the flag.

An outcome the record never established is neither, because such a record does
not establish that the call ran. Folding it into "later write or edit recorded"
would report an observation the record does not support; folding it into "no
later write or edit recorded" would report that nothing followed the read. It is
counted in a field of its own and printed as itself, preserving the outcome
discipline ADR 0007 set.

### Boundaries are a lower bound, and the report says so

ADR 0009 recorded a compaction that emitted no `session_start` because the session
ended first. An unobserved boundary makes the reads on either side of it look like
one epoch, so the relation is not reported. The error direction is toward false
negatives, which is Axiom's stated preference, and the section states the limit
rather than leaving a quiet report to be read as a clean one.

### Epoch placement comes from the state machine

`Timeline.Add` now returns a `Placement`: the `EpochRef` the record was placed in,
the epoch's `Opening`, and whether it was placed at all. This is the mechanism ADR
0009 named as the prerequisite for any per-epoch analysis, and it is the only
sound one — membership follows append order, recorded times are not monotonic, and
concurrent sessions interleave, so reconstructing membership afterwards by
comparing a record's time against an epoch's window would place records by
something the log does not establish.

The ordinal is derived as the position of the open epoch, exactly as `epochs()`
derives it, so a placement and the report cannot disagree.

A record that belongs to no epoch reports `Placed: false` and is given none: no
session identity, a session end that closed nothing, a `tool_call` carrying no
call, and a record this version cannot interpret. A start belongs to the epoch it
opens and an end to the epoch it closes, which is what keeps an epoch that
recorded nothing else observable as an epoch.

`Add` returning a value is source-compatible: a caller that wants only the
structure ignores it.

### `internal/reacquire`, depending on `event` and `timeline` only

Not the profiler, not `correlate`, not `activity`, not the CLI. The rule from ADR
0007 holds: measurement must not drag findings in behind it. Consuming the
timeline is not that — it is a derivation package with no findings in it — and it
is what avoids a third independent encoding of "a `session_start` is a boundary".
ADR 0009 accepted one such duplication and named it unwanted; this ADR does not
add another.

The name is a verb, like `correlate`. `internal/epoch` would have been narrower
than the timeline it depends on, and `internal/rediscovery` would have named the
inference this ADR spends most of its length refusing.

### Bytes, tokens, cost and duration are deferred

ADR 0009 established that tool-result bytes are sound per epoch, since an
invocation is one record in one epoch. They are still left out.

The reason is not soundness. A size printed beside a re-acquisition reads as the
price of the boundary, and that is a claim about consumption caused by a
structural event, which nothing here establishes. Tokens and cost are worse: they
join at `(session, turn)`, and a turn was observed spanning a boundary, so they
cannot be divided between epochs by any evidence Axiom has. Duration is tool
execution time and would read as the same thing.

## Consequences

- The context epoch stops being a display and becomes something analyses are
  expressed against. `Placement` is the mechanism, and per-epoch findings and
  per-epoch measurement — both deferred by ADR 0009 — now need only their own
  rules rather than their own machinery.
- `axiom profile` gains a fourth section, after the epochs that give it its
  coordinates. Every line names the session, the epoch ordinals and each epoch's
  recorded opening, so a reader can find the reads it came from.
- Three empty states are kept apart: no session had more than one epoch, there
  were boundaries and nothing was read across one, and reads were set aside. The
  first two would otherwise both read as "nothing happened", when one of them
  means there was nothing to look for.
- Silence has a stated cause. A log with no boundary says so instead of implying
  a clean session.
- The analysis is deliberately blind to the log's most common repetition — the
  same paths read under two session identities — and the section says that too.
- Nothing here is called waste, cost, rediscovery, forgetting, or context loss.
  Axiom reports that a path was read, that a boundary was recorded between two of
  those reads, and whether a write or edit of the same path was recorded after
  one of them.
