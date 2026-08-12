# 17. Comparing captures

- Status: accepted
- Date: 2026-08-12

## Context

Every analysis Axiom has is about one recorded log. The question that motivated
this ADR is about two:

> What differs in the recorded structure between two recordings an operator
> declares comparable?

The reason to want it is that a harness — a prompt, a set of hooks, a subagent
policy — is changed and someone wants to know what changed in the recorded work.
That is not the same as knowing whether the change was an improvement, and this
ADR is careful to build only the first.

The feature was investigated before it was scoped, and the investigation
returned a smaller feature than the one proposed. Two things it established are
load-bearing here and are recorded below: what a repeated recording of one
workload actually holds still, and what a directory of records can be said to
be.

## Evidence

### Ten recordings of one workload

The same delegation workload was recorded ten times against Claude Code 2.1.228,
with the repository contents, the prompt, the model, the permissions mode, the
Axiom binary and the hook configuration held constant, one isolated
`AXIOM_DATA_DIR` per recording, and no telemetry configured. Each run asked for
two subagents to investigate two failing tests, report back, and then be fixed by
the launching agent.

Unchanged across all ten:

| Dimension | Value |
| --- | --- |
| Session identities | 1 |
| Context epochs | 1 |
| Records skipped | 0 |
| Whole-file reads | 6 |
| Ranged reads | 0 |
| Searches | 0 |
| Writes | 0 |
| Edits | 2 |
| Subagent launches | 2 |
| Launches returning an agent identity | 2 |
| Delegation relations | 2 |
| Launching scopes | 1 |
| Paths read across related scopes | 2 |

Changed across the same ten:

| Dimension | Min | Max | Values |
| --- | --- | --- | --- |
| Recorded tool calls | 13 | 20 | 16, 14, 20, 18, 16, 15, 17, 19, 17, 13 |
| Shell | 3 | 9 | 4, 4, 9, 7, 5, 5, 5, 5, 5, 3 |
| Uninterpreted | 0 | 4 | 2, 0, 1, 1, 1, 0, 2, 4, 2, 0 |
| Turns with work | 1 | 2 | 2, 1, 1, 1, 2, 2, 2, 2, 2, 1 |
| Calls by nested agents | 4 | 8 | 6, 6, 8, 5, 6, 6, 8, 8, 6, 4 |

Why each of the movers moved was established rather than assumed:

- **Shell.** The read set was identical in all ten — the session scope read the
  same two files and the two agents read the same four — but each agent chose
  for itself whether to run the test suite, and how often. Nested shell calls
  ranged from 0 to 4 and session-scope shell from 1 to 6.
- **Uninterpreted.** Every uninterpreted call in all ten recordings was the same
  tool, `ScheduleWakeup`, which Claude Code uses while waiting on asynchronous
  subagents. It is outside the metadata allowlist, so it is classified
  correctly; its count tracks scheduling, not work on the repository.
- **Recorded tool calls.** Each recording is `10 + shell + uninterpreted`. It is
  a stable part plus the two noisy parts, which is exactly why it is the worst
  available headline.
- **Calls by nested agents.** Nested reads were always 4; the entire range is
  nested shell.

**This does not establish that any dimension is stable.** It is one workload,
one repository, one model, one permissions mode, and one agent version. The read
count held still because this task fixes which files must be read; a task where
the agent chooses what to open would move it. Nothing in the implementation
treats any dimension as stable, and no threshold, tolerance or variance model
exists anywhere in it.

### A real context reset with a re-read across it

A separate recording established the cross-epoch observation end to end on real
evidence rather than a constructed log. One session read a file; `/compact` was
then issued; the resumed session was asked to read the same file from disk
again. A `tee` hook preserved the raw `SessionStart` payloads beside Axiom's own.

Claude Code emitted four starts under **one unchanged session identity**, with
sources `startup`, `resume`, `compact` and `resume`. Axiom derived four context
epochs, two of which recorded work, and `reacquire.Report` reported one path read
in epoch 1 and again in epoch 4. The profiler reported no finding for it, which
is correct and is the complementarity ADR 0010 describes: a reset ends the run
the profiler compares within, and this section is what makes the crossing
visible without judging it.

Two limits of that recording matter. The re-read had to be asked for: after a
plain resume the agent answered from its restored context and made no second
read at all. And the two reads fell in epochs 1 and 4 while the `compact`
boundary opened epoch 3 — which is why nothing attributes a reacquisition to
compaction. The recording also settled a reporting question: four epochs, two
with work, so an epoch count alone would overstate what the capture did.

### A directory is not an execution

The same recording produced **one** session identity across **three** CLI
invocations. An earlier recording accidentally acquired a **second** session
identity because one extra command was run against the same directory, and
`axiom profile` reported `Sessions analyzed 2` without complaint. The number of
invocations, the number of session identities and the number of epochs are
mutually independent, and only one of them is a thing Axiom can verify.

## Decisions

### The noun is "capture"

A capture is *the records Axiom wrote into one `AXIOM_DATA_DIR`, narrowed to
exactly one session identity, that an operator declares comparable to another
capture*.

"Run" and "execution" were both rejected on the evidence above. ADR 0009 already
refused to name an execution, because nothing an agent reports says "this was
one attempt at one task". The three-invocation, one-session, four-epoch
recording is the case that falsifies "run" outright: it is either one run or
three depending on which layer the reader has in mind, and the report cannot
know which. "Capture" names what the tool can point at — records in a directory,
narrowed to an identity — and says nothing about intent.

The word is used in the command, in the report, in the code and here.

### Comparability is asserted by the operator, and the report says so

Axiom does not establish that two captures are the same task, that they are
equivalent attempts at one, that either explains anything about the other, or
that a difference is good or bad. The operator chose the two directories; that
choice is the only thing making them comparable, and it is not evidence.

This is printed in the report itself, under *What this compares*, rather than
only in documentation. A caveat that lives somewhere else is a caveat for
readers who already agree.

### Exactly one session identity per side, or the comparison is refused

A directory holding more than one session identity is refused unless
`--baseline-session` or `--candidate-session` selects one, using the same
exact-match rule `axiom profile --session` already applies.

The alternative — comparing multi-session directories with a prominent
qualification — was evaluated and rejected. The decisive reason is not
ergonomics: several of the compared dimensions are *defined per session
identity*. `internal/reacquire` counts epochs within one identity and never
compares two; `internal/crossread` groups scopes within a session because an
agent identity means nothing outside the session that issued it; the profiler's
scopes are per session and subagent. A capture-level number computed across two
identities would sum over a boundary ADR 0010 and ADR 0016 both refuse to cross,
and the result would look exactly like a single-session number.

The accidental second session above is the practical half of the argument. A
qualification can be skimmed; a refusal cannot. The refusal names the identities
it found and the flag that selects one, so it is one step from being resolved.

### The comparison surface is four structural blocks and a shape

Preceded by the shape of each capture — selected session, context epochs, epochs
with recorded work, recorded tool calls, records skipped, and whether a usage
log exists — the report compares:

1. **Recorded work by shape**, using `work.Composition` unchanged: whole-file
   reads, ranged reads, searches, shell, writes, edits, subagent launches and
   uninterpreted, with writes, edits and launches keeping their outcome states.
2. **Delegation**, from `delegation.Report`: launches recorded, launches
   returning an agent identity, relations established, launching scopes.
3. **Read across related agent scopes**, from `crossread.Report`: the path count,
   never alone, always beside launches, relations and groups.
4. **Read again in a later context epoch**, from `reacquire.Report`: the path
   count beside sessions with more than one epoch, context epochs, and epochs
   with recorded work.

The shape rows carry no difference column. They are what makes the blocks
readable, and a difference printed beside the recorded tool calls would make the
one number the evidence least supports into the headline of the report.

### The partition stays complete, including the parts that move

Shell and uninterpreted calls varied by a factor of three and four respectively
across ten recordings of one workload, and they are still printed. The
categories are a partition of the recorded calls: one removed to make the rest
look steady would leave a table that no longer accounts for what was recorded,
and would describe the remaining categories as the whole of the work. The report
says both moved between repeated recordings, in the same place it says what a
difference is.

Nothing summarizes the partition. There is no total-work number, no score, and
no derived rate.

### A difference is a signed count, and nothing else

`same`, `+2`, `-1`. No percentages, no ratios, no rankings, no adjectives, no
causal language. An unchanged count is written as a word rather than as a zero,
so that a dimension that did not move cannot be misread as one that measured
nothing.

The vocabulary this rules out — efficiency, waste, savings, better, worse,
improvement, regression, optimal, caused — is enforced by a test over the whole
output, including the refusal messages.

### Consumption is excluded

No tokens, no model requests, no cost, no consumption difference. Whether a
usage log exists is shown, because a reader has to know what each side holds,
and it is shown as presence only.

Three reasons, in order of weight. Telemetry exists only while a receiver was
running, so a missing measurement is unrecorded consumption and not consumption
of zero, and two captures rarely have equal coverage. Consumption is recorded
under turn identities that need not match the ones behind the behavior, which
ADR 0005 handles by reporting coverage rather than a total. And the investigation
measured a 19% cost difference between two recordings of one identical workload,
which is a number that would be read as a result.

### Findings are deferred

Ten real recordings produced zero findings, so there is no evidence about how a
finding count behaves across repeated recordings — and what evidence exists says
it is boundary-dependent: the same three repeated reads yield one finding of
three occurrences, or two of two, depending on where a context reset fell and
which agent scope made them. A count that changes with the boundaries is not
something to subtract. This is a deferral, not a refusal: it needs captures that
exercise it on both sides.

### Paths and command digests are not compared

The path count is compared; the paths are not. Two captures record their own
absolute paths, so an equal count is comparable and the strings are not — and
normalizing them would mean trusting a working directory Axiom never observed,
which ADR 0007 already refused for one log and is worse across two. A command is
recorded only as a digest of one exact string; the investigation observed digests
differing between two recordings of one workload.

Neither is printed. A path shown side by side invites the reader to do the
comparison the report declined to do.

### The seam is a container of existing reports

`internal/analysis` reads one log and returns every domain report derived from a
single pass over it. It exists because `axiom profile` and `axiom compare` need
the same pass, and the alternative was a second copy of it that would eventually
disagree with the first about something as ordinary as what counts as a tool
call.

It is deliberately not a report. It has no combined total, no cross-field
derivation and no vocabulary of its own; every field is a type an existing
package owns and documents. The one assembled value is the log-wide
`work.Composition`, and the counting there belongs to `internal/work` — the seam
only decides which records to hand it, by the same test the accumulators beside
it apply. A test pins that the composition totals exactly the tool calls the
profiler counted, so the two cannot drift.

Rendering stays in `internal/cli`. `axiom profile` output is unchanged, which is
pinned byte for byte by a golden generated before the refactor.

### What is deliberately not here

No experiment definitions, stored baselines, repeated-run averaging, variance
thresholds, CI quality gates or dashboards. No comparison of turns with work,
calls by nested agents, read bytes, event counts, or individual paths and
digests. No generic execution report. No schema, event or storage change. No
change to `axiom profile`. No aggregation of sessions, ever.

## Consequences

Axiom can now answer, for two captures an operator declares comparable, what
differs in the recorded structure — and the answer is a table of signed counts
with no verdict attached.

The refusal will be met by anyone who ran two agents against one data directory,
which the investigation did by accident. That is the intended cost: the
alternative is a number that silently spans two identities.

Most comparisons of ordinary work will report `same` for the reacquisition block
on both sides, because natural re-reading after a reset was not observed. That
is an honest zero and not a defect.

A future ADR may add findings to the comparison once captures exist that
exercise them on both sides. Nothing in this design forecloses it, and nothing
in it depends on the four blocks staying four.
