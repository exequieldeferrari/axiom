# 9. Context epochs as an analysis primitive

- Status: accepted
- Date: 2026-08-11

## Context

Axiom could judge work and describe it, and had no unit to do either in. Findings
are scoped to a session and subagent; the profile by path aggregates across the
whole log; the report's header counted sessions and said nothing about what
happened to their contexts. A reader could not tell a quiet report from a report
whose evidence had been divided by resets, and nothing in the system named the
span within which repetition is compared.

The evidence for that span was already being recorded and never read. Every
`session_start` carries the agent's own word for what started it, and the profiler
has reset its scopes on every one since PR #3 — but the boundary itself was
invisible in the model and in the report.

The design depended on facts about Claude Code that nobody had verified, so this
PR began with capture rather than code. What follows is what was observed, because
the decisions below are only as good as it is.

## What was observed

Claude Code 2.1.228 on macOS, hooks installed in a scratch project, one isolated
`AXIOM_DATA_DIR` per scenario, with the raw hook payloads teed to a file beside
Axiom's own ingestion so that the mapping could be checked against the source.
Both the headless (`claude -p`) and the interactive terminal paths were captured.

| Scenario | Recorded | Session identity |
| --- | --- | --- |
| Startup | `session_start`, source `startup` | new |
| `/compact` | `session_start`, source `compact` | **unchanged** |
| Automatic compaction | `PreCompact` with trigger `auto`, then `session_start`, source `compact` | **unchanged** |
| `/clear` | `session_end` reason `clear`, then `session_start` source `clear` | **new** |
| Resume (`--continue`, `--resume`) | `session_start`, source `resume` | **unchanged** |
| Fork (`--resume --fork-session`) | `session_start`, source `fork` | **new** |

Five observations mattered more than the table:

**Compaction keeps the session identity.** It is a boundary *inside* a session,
which is what makes an epoch a distinct concept from a session. `/clear` and a
fork are the opposite case: a new identity, with nothing recorded linking it to
the one before it.

**Automatic compaction emits the same boundary as the manual command**, and it was
observed opening a context in the middle of a turn: the prompt was submitted, then
`PreCompact` with trigger `auto`, then the `session_start`, and then the turn's
tool call — all under one `prompt_id`. Turn straddling is not hypothetical. It was
reached by lowering Claude Code's auto-compact window through an internal
environment variable rather than by filling a real 200K window; the threshold was
lowered, the path taken was the automatic one.

**A compaction can leave no boundary in the log.** In a one-shot headless capture,
`PreCompact` with trigger `auto` fired at the end of the turn and the process
ended before any `session_start` arrived. The next turn of that session, started
later, carried the `compact` start. So the boundary record is emitted when the
compacted context is next used, and a session that ends first records none.
Axiom's counts were always lower bounds; its boundaries are too.

**Subagents do not start sessions.** No `session_start` in any capture carried an
agent identifier. A subagent's tool calls carry `subagent_id` and share the
parent's session and turn, and arrive interleaved before the parent's own call
completes. Incidentally, compaction summarization itself runs as a subagent, which
is recorded and not used for anything here.

**A `session_end` reason is not a fixed set.** `other`, `prompt_input_exit` and
`clear` were all observed, from the same two commands.

## Decisions

### An execution is not observable, so nothing here is called one

Axiom records session identities, turns, and the fact that a context started. None
of those means "one attempt at one task": one sitting spans several identities
whenever `/clear` is used, and one identity spans several contexts whenever
compaction runs. Naming any of them "the execution" would put a unit of work into
the vocabulary that no record establishes.

So this PR introduces the two scopes the log does establish — session identity and
context epoch — and leaves "execution" unused. Comparing two runs later will need
a unit somebody asserts, because comparability is a claim about the task, and the
task is not in the telemetry.

### The recorded start is the only boundary

A `session_start` is the only thing that opens an epoch by boundary. There is no
time-gap heuristic, no stitching, and no inference from what a source string
means: the value is stored verbatim and never branched on, so a source no version
of Axiom has seen still reports as itself. The one other way an epoch begins is
implicit: work recorded for a session with no context open opens one, marked as
having no start recorded, because the alternative is discarding the work.

This is deliberately the same rule `Profiler.reset` already follows, so a start
begins an epoch exactly where the profiler stops comparing repetition across.
The two are not identical, and the report must not claim they are: an epoch also
ends at a `session_end`, which resets nothing in the profiler.

### Three states of "we do not know how it opened"

A start whose source the agent reported, a start that carried no source, and no
start at all are kept apart. The last is not an error: it is what every log holds
when Axiom was installed while a session was already running, and it is also what
a record arriving after a `session_end` produces. Collapsing the three into
"unknown" would turn "we were not listening yet" into "the agent told us nothing".

### A closing says what the log holds, never what is running

An epoch ends by a context reset, with the session (reporting the agent's reason,
or that none was given), or with nothing recorded after it. The last is stated as
exactly that. "Still open" or "active" would be a claim about a process Axiom
cannot see, and a session that was killed produces no `session_end` at all.

### Two session identities are never linked

Not by adjacency, not by a matching `clear` reason and `clear` source on either
side of a boundary, not by time. Concurrent agents interleave in one log, so
adjacency is not continuity, and a pairing rule would read as linkage no matter
how it was worded. `/clear` is reported as two sessions; compaction as two epochs
of one. Those are the facts the log contains.

### What can belong to an epoch, and what cannot

Tool calls can: each record sits at one position, so per-epoch counts add up to
the session's own. Subagent calls can, on the same grounds.

Turns can only be counted *within* an epoch. A turn was observed spanning a reset,
so per-epoch turn counts overlap and are never summed into a session total; the
report says so where the counts appear.

Model requests, tokens and cost cannot, and are not reported per epoch at all.
They join at `(session, turn)`, and a turn that spans a boundary cannot be divided
between its epochs by any evidence Axiom has. Splitting them by record position
would be inventing an allocation, which is the failure ADR 0005 exists to prevent.
Tool-result bytes would in fact be safe, since an invocation is one record in one
epoch — they are left out because they are not needed to establish the primitive,
not because they are unsound.

Findings are not safe, and that is the reason they are deferred. `reset` ends
every run at a recorded start, so no finding crosses one; a `session_end` is
different, because it closes an epoch here while the profiler keeps its scope. A
record appended after an end — hooks are separate processes and can finish out of
order — therefore continues a run into the next epoch, and a finding built from
it belongs to two. Attributing findings will need the profiler to stamp an epoch
as it observes, and a rule for that case; it must never be done by testing a
finding's window against an epoch's, because recorded times are not monotonic.

### Epochs key on the session identity alone

The profiler scopes by session *and* subagent, because repetition by a nested
agent is not repetition by its parent. A context reset is not like that: it is an
event in the session's lifecycle, and no subagent was ever observed reporting one.
So epochs key on the session identity, and a subagent's calls are counted inside
the epoch they arrived in and reported separately on its line.

### Derivation only, in `internal/timeline`

The package reconstructs structure and computes nothing else. It depends on
`internal/event` and nothing more — not the profiler, not `correlate`, not
`activity` — following ADR 0007: measurement must not drag findings in behind it.

It is not called `internal/epoch`. An epoch is one element of the structure, and
naming the package after it would be narrower than what comparison needs, which is
the whole shape — identities, their epochs, and the ability to select against
them. It is not called `internal/scope` either: the profiler already uses "scope"
for something narrower, and two meanings of one word in one codebase is a bug
waiting for a reader.

One duplication is accepted and named: `timeline` and `profiler` independently
encode "a `session_start` is a boundary". Inverting the dependency so the profiler
consumes the timeline would reopen PR #3's semantics for no gain today. Instead
both state the rule, and a test at the seam asserts what actually matters — that a
read repeated across a reset produces two epochs and no finding.

### Selection is exact identity, applied while scanning

`axiom profile --session <id>` filters on the identifier the agent recorded,
exactly. A prefix selects nothing, because a prefix is not an identity and
matching one would silently analyze a different session. The filter is applied in
the existing single pass, so the structure shown and the analysis reported come
from the same records.

Skipped-record warnings stay whole-log even under a scope. A record Axiom could not
decode cannot be attributed to a session: what was lost is exactly what would have
said which one it belonged to.

`--last` is deliberately absent. It has two defensible meanings — the last session
identity, or the last epoch — and now that the report prints identities,
`--session` closes the loop without either. It also cannot be resolved in one pass.

## Corrections

Two shipped comments claimed that Claude Code starts a *new session* on
compaction. That is false: the identity is unchanged, as the captures above show.
No behavior was wrong — resetting the scopes of the session named on the start is
exactly right, and compaction is the case that actually exercises it — so the
correction is to the words in `internal/event/event.go` and the README, and to the
comment on the reset itself. `/clear` turns out never to exercise that path at
all, because the new identity separates the work already.

## Consequences

- The report has a third section, printed first, and the profiler's scoping is
  legible for the first time: a reader who sees no findings can see whether the
  evidence was cut into pieces, and by what.
- Axiom now has a unit of analysis smaller than the log and larger than a turn.
  Everything deferred here — re-acquisition of files across a boundary, findings
  attributed to epochs, per-epoch measurement, comparison — is expressed against
  it rather than inventing its own span.
- Compaction boundaries are a lower bound, for the reason recorded above: a
  compaction whose session ends before the context is used again records nothing.
  Any future analysis of what crossed a boundary has to survive a missing one.
- Turn counts in the timeline are non-additive, which is unusual in a report whose
  other columns reconcile. It is stated where it appears, and the alternative —
  hiding turns per epoch — would hide the straddling that makes it true.
- Nothing about compaction is called waste, forgetting, rediscovery, or cost.
  Axiom reports that a context was reset, how it was reported, and what was
  recorded on each side of it.
- Epochs and sessions are truncated in the display, keeping the most recently
  recorded, with everything omitted accounted for on a line of its own.
- `--session` proves the mechanism a future `compare` needs — run the same
  analyses over a chosen subset of records — with the simplest possible selector.
