# 7. Execution profile by path

- Status: accepted
- Date: 2026-08-11

## Context

Axiom could judge behavior before it could describe it. Six PRs produced findings
— repeated reads, repeated commands, repeated failed attempts — each withheld
unless the evidence ruled out the ordinary reasons an operation repeats. That
discipline is right for an accusation and useless as a description. A real
capture of four turns of ordinary work produced 39 tool calls, 20 of them file
operations across nine paths, and the report said: three totals and "no
findings". Nothing told the reader whether the execution was clean or whether the
analysis was blind.

This PR adds the description. It began as an investigation into "context
rediscovery" — whether Axiom could show an agent reacquiring repository
information it already had. That hypothesis was rejected on the evidence: in the
organic captures available, no two separated groups of reads shared more than one
file, and eight of nine reads existed only because Claude Code's `Edit` tool
refuses to run without a prior read of the same path in the current context
("File has not been read yet. Read it first before writing to it."). A detector
built on read identity would mostly have been measuring a tool precondition. The
research is recorded in the pull request; what survives here is the part the
evidence does support: counting what was observed, and saying where.

## Decisions

### A profile is measurement, and it is not a finding

The profiler prefers false negatives and stays silent unless the evidence rules
the innocent explanations out. A profile counts everything observed and rules
nothing out. It therefore has no confidence level, no barriers, no scope
isolation, and no run semantics, and it counts the very operations the profiler
treats as barriers.

Because the two contracts contradict each other, the profile lives in its own
package, `internal/activity`, which depends on `internal/event` and nothing else.
It cannot depend on `internal/correlate`, which imports the profiler: measurement
would have dragged findings in behind it.

### Observed, recognized, attributable and measured are four different things

They are kept apart by one exhaustive partition of the observed tool calls
instead of four flags. `File`, `Search`, `Shell` and `Subagent` are the shapes the
metadata allowlist recognizes; `Unrecognized` is everything else. *Recognized* is
every bucket but the last. *Attributable* is `File` alone, because only a file
operation names a path — a shell command is recognized and unattributable
forever, since the command text is deliberately never recorded, and a search
records a root, which is a place to look rather than a resource that was read.

The buckets sum to the tool calls above them, and the operations attributed
across paths sum to the `File` bucket. Those two invariants are what let a reader
reconcile the profile with the execution instead of trusting it.

### No single coverage number

An earlier design proposed "analysis coverage". It was rejected: the denominators
differ — tool calls for interpretation, reads for measurement — the concepts
differ, and the base is itself a lower bound, because a call rejected before it
ran is never recorded. A percentage over that base would promise completeness
Axiom cannot establish.

So there is no ratio. There is a composition, in counts, and completeness stated
where each value appears. Coverage of the *findings* analysis — how often a
barrier ended a run — is deliberately left out: its denominator is runs, its
subject is the other analysis, and adding it would have made the profiler start
counting things to explain itself.

### Identity is the exact path the agent named

Axiom does not invent canonical identity in v1. There is no normalization of any
kind: no cleaning, no symlink resolution, no relative-to-absolute resolution.
Every path observed in real captures was absolute, which is not a canonical rule,
and resolving a relative path would mean trusting a working directory Axiom did
not observe at the moment of the call.

The consequence is accepted openly: two strings that refer to the same filesystem
object become two rows. This is not a theoretical risk. In a capture with two
nested agents on macOS, one agent reported paths under `/private/tmp/...` and the
other reported the same directory as `/tmp/...`, because the system symlinks one
to the other. Those two agents happened to read different files, so no row was
actually duplicated — but the aliasing was observed, in an ordinary session, by
Axiom itself.

That observation is evidence for a future decision, not authority to make it now.
Any normalization rule needs its own evidence and its own design: which alias
classes it covers (agent-reported symlink prefixes, relative paths, case-folding
filesystems), what it does when two normalized paths disagree about content, and
how a report shows that it altered identity. Until that exists, Axiom reports what
the agent said and nothing more.

Trimming a shared directory from the printed lines is display only: the prefix is
named once above them, and the paths compared are never altered.

### Categories are what the record says, not what the work was for

`Read`, ranged read, `Write`, `Edit` and failed are fields of `FileOp` and
`Outcome`. An earlier design proposed roles — acquire, modify, verify — and
verify required inferring intent from the order of tool calls. It was dropped:
the profile is measurement-shaped, and an inferred role is interpretation
wearing a count's clothes.

Writes and edits stay separate in the model even though a report shows their sum:
the record distinguishes them, and a model that folded them could not be
unfolded later. Ranged reads stay separate from whole-file reads because they
acquire something else. Failed operations are counted as neither reads nor
modifications, and never dropped — a path with five failed edits did work, and
dropping it would break the reconciliation with the `File` bucket.

### Failed means an observed failure, never an absent success

`Failed` counts the operations the agent reported failing. It is tested against
`OutcomeFailure` by name, not as everything that is not `OutcomeSuccess`, because
those are different claims. Nothing validates the outcome field on the way into or
out of the log: a record can arrive with no outcome, and a later model may add a
state this version does not know. Either would have been counted as a failure by a
"not success" test, which is failure inferred from missing evidence — exactly what
the evidence rules forbid.

An operation whose outcome was not established is therefore counted as
`Unrecognized` and attributed to no path. It was observed, so it is counted; Axiom
cannot say whether it read anything, changed anything, or failed, so it claims
none of the three. That also preserves the reconciliation, because `File` stays
equal to the operations attributed across paths.

### Where and how long belong to the call, not to its outcome

A failed read still happened in a turn and still took time, so both contribute.
Turns are counted as distinct session-and-turn pairs, because a turn identifier
is the agent's own and means nothing outside the session that issued it.

Time is the sum of the durations the agent reported and is not elapsed time:
nested agents run in parallel, so these sums can exceed the wall clock they
happened in.

### Read bytes are direct measurement, all or nothing per path

A path reports what its successful reads returned only when there was at least
one of them and every one was measured exactly once. Anything absent, ambiguous,
duplicated, or lacking an invocation identifier withholds the whole total,
because a partial sum understates the truth while looking exactly like a complete
one. Only reads are measured: what an edit returns is the agent confirming its
own change, not repository content.

A failed read is outside the read counts and outside the byte totals. That is a
boundary on what those values describe — bytes returned by reads the agent
reported succeeding — and not a claim that a failed read returned nothing.
Nothing recorded establishes that claim: the hook reports whether a call failed
and never what it returned, and in the captures telemetry reported no size for
failed calls, which is a measurement that is absent rather than a measurement of
zero. Counting a failed read as zero bytes would be the same inference from
missing evidence that `Failed` itself refuses to make.

Nothing is derived from these bytes — no tokens, no cost, no savings, and no
attribution of consumption to a path.

### Withheld is never zero, and known zero is not printed

A value Axiom could not establish is printed as a dash under a stated rule. A
count of zero is a fact Axiom did establish, so a category with none observed is
left off a path's line and a shape with none observed is left out of the
composition. When nothing at all was measured, the byte field is dropped from
every line and explained once, rather than repeated as a column of dashes.

### `correlate` grows primitives, not per-feature accessors

Measurements reach the profile through `ByteLookup`, a function the CLI supplies,
so `internal/activity` knows nothing about how the two streams are joined. To
serve it, `correlate` exposes `ResultBytes(Key)` — the join primitive the package
already had inside `redundantBytes`, which now calls it. The rule going forward:
`correlate` may grow primitives over the identifiers both streams carry, and may
not grow a method per consumer.

## Consequences

- Silence in the findings section is now interpretable. A reader who sees no
  findings can also see that only 8 of 17 observed calls named a path and that
  the other 9 were shell commands whose effects Axiom does not observe, which is
  the honest explanation for most silences.
- The profile ranks by operations, never by bytes or time, so it cannot be read
  as saying which work mattered most.
- Two agents working at one path add up there. That is a sum of operations and
  never a claim of repetition, which remains the profiler's judgement under its
  own rules.
- Byte totals are formatted in kilobytes, like every other size in the report, so
  a large total reads as thousands of kilobytes. Changing that would change
  existing output for findings, which this PR is not about.
- The profile aggregates across every session in the log, keyed on the path
  string. A machine that analyzed two checkouts at the same absolute path would
  see them as one place. Turn counts stay correct regardless, because a turn is
  identified with its session.
- Exact identity has an observed cost, recorded above: agent-reported macOS path
  aliases can split one filesystem object across two rows. The first thing a
  normalization rule will have to handle is therefore alias prefixes, not just
  relative paths.
- The `Unrecognized` bucket now holds two kinds of ignorance — an operation Axiom
  cannot interpret, and an operation whose result was never established. Both are
  "counted, not guessed about", but if unestablished outcomes ever appear in real
  logs, they will need to be told apart from unreviewed tools.
- A profile can be taken repeatedly from the same accumulator, and it is built in
  the same single pass over the log as the findings.
