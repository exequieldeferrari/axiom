# 13. Recorded turns

- Status: accepted, with one observation superseded by ADR 0014
- Date: 2026-08-12

> **Correction.** The observation below headed *Current `Agent` calls arrive
> uninterpreted*, and the paragraph in *The turn model is the evidence and
> nothing more* that rests on it, are wrong. Both are left in place and marked,
> rather than rewritten, because what this ADR concluded is part of why the next
> one exists.
>
> The raw-payload investigation behind ADR 0014 established that Claude Code
> 2.1.228 supplies `tool_input.subagent_type` on every `Agent` call, and that
> `internal/claude` derives `metadata.subagent` from it correctly. The adapter
> had no gap. What discarded the classification was `internal/turns`, which had
> no case for `metadata.subagent` and counted every launch as uninterpreted.
>
> The mistake was one of evidence, not of measurement. This ADR read the
> `Uninterpreted` count in its own new section as though it said something about
> the adapter. That count would have read the same with a perfect adapter, which
> is exactly what it did, so it was never evidence about one. ADR 0014 records
> what the payloads themselves contain.

## Context

Axiom reports work at two scales and neither is the one an agent operates at.
The timeline (ADR 0009) gives the session identity and the context epoch, which
are lifecycle boundaries: an epoch can hold a whole afternoon. The activity
profile (ADR 0007) gives the path, which is finer than any decision the agent
made. Between them sits the unit the agent actually labels its own records
with — the turn — and until this ADR nothing in Axiom reported one.

The turn was already load-bearing everywhere else. The profiler bounds a
repeated-failure run to a single turn (ADR 0006) and reports whether a turn
boundary fell inside an interval (ADR 0011). The correlation layer joins model
consumption on `(session, turn)` and reports it against findings (ADR 0005). The
timeline counts turns with work inside each epoch. Every one of those uses the
turn as a qualifier on something else. None of them answers the question
directly:

> What recorded work and recorded model consumption belonged to each recorded
> turn?

That gap has a visible cost. Consumption reaches the report only through a
finding, so a log with no findings reports no consumption at all, and a log with
findings reports consumption only for the turns those findings happened to touch.
On the capture below that is most of the evidence: 24 model requests were
recorded and a findings-only report could speak to none of them, because the
capture produced no finding.

This ADR adds the turn as a reported unit. It does not add a new judgement:
nothing here ranks, scores, compares or recommends.

## What was observed

A controlled capture, Claude Code on macOS with Axiom's hooks and an OTLP
receiver running, in an isolated `AXIOM_DATA_DIR` and a scratch project. One
interactive session, several prompts, a successful and a failed tool call, two
`Agent` calls, and a background `Bash` command. It produced 12 events and 34
usage records: 10 tool calls, one `session_start`, one `session_end`, 24 model
requests and 10 tool results.

**Tool calls named four turn identifiers**, with 3, 1, 4 and 2 calls. Every tool
call carried one. This is what makes the analysis possible at all.

**A fifth identifier appeared only on the `session_end`.** The `session_start`
carried none. An analysis that took every identifier it saw would have reported
five turns, one of which recorded nothing: a 25% overcount on a capture this
small, and the overcount grows with the number of sessions rather than with the
amount of work.

**Three further identifiers appeared only in the usage stream**, carrying 7 of
the 24 model requests and 17.7% of the observed cost estimate. No tool call ever
named them. Taking them as turns would have reported three turns that did no
work; dropping them would have hidden a sixth of the recorded consumption.

**The nested agent's tool calls carried the launching turn.** Both `Read` calls
made inside the subagent were recorded under the same identifier as the `Agent`
calls that launched it. The model requests recorded immediately after them
carried two of the identifiers no tool call named. Nothing in the record relates
those requests to the launching turn, and nothing in the record rules the
relation out either.

**Turns did not interleave.** With two parallel subagents and a background
command, every turn's records appeared contiguously in append order. Order and
membership can therefore be taken from the log without reconstructing anything.

**Current `Agent` calls arrive uninterpreted.** *(Superseded by ADR 0014: the
payload does carry `subagent_type`, the adapter does record it, and the gap was
in `internal/turns`.)* Claude Code's `Agent` input carries `description` and
`prompt` and no `subagent_type`, which is the field
`internal/claude/metadata.go` requires to classify a subagent spawn. Both spawns
in the capture reached the analysis with no metadata at all. That is an adapter
gap and is not fixed here.

## Decisions

### A recorded turn exists only where a tool call named it

`internal/turns` creates a turn from a `tool_call` record and from nothing else.
A `session_start`, a `session_end`, a `model_request` and a `tool_result` never
establish one.

This is the whole of the existence rule, and it is a claim about evidence rather
than about the agent. Axiom observes tool calls. An identifier on a lifecycle
record says the agent had a turn in progress when it wrote that record; it does
not say any work happened in it, and a section headed "recorded turns" listing a
turn with no recorded work would be answering its own question wrongly. The same
holds for the usage stream: a turn identifier on a model request establishes
consumption, not work, and the two questions are kept apart everywhere else in
Axiom for the same reason.

The rule is a lower bound and is described as one. A turn whose work Axiom did
not observe — because a hook did not fire, or because the work was not a tool
call — is a turn this section does not list. That is the same shape of limit the
timeline has with unrecorded boundaries.

### Identity is `(session, turn)`

A turn identifier is the agent's own. Nothing establishes that it is unique
beyond the session that issued it, so the session is part of the identity, as it
already is in `correlate.Key` and `correlate`'s turn join. Two sessions naming
the same identifier are two turns.

A call that named no turn is not assigned to the turn beside it: the neighbour
is a guess. A call that named no session has an identifier that identifies
nothing. Both are counted in `CallsOutsideTurns` and reported, so work the
analysis could not place does not disappear from the report.

### Append order is authoritative

Membership, ordering and the ordinal come from the order records were appended,
which is the only order a log establishes. `internal/turns` follows the pattern
`internal/reacquire` set: it is fed the `timeline.Placement` derived for each
record during the existing single pass, so epoch membership comes from the state
machine that owns it.

Recorded times are carried for display only. Hooks are separate processes whose
records can carry times out of order, so the window is widened to cover every
recorded time rather than assigned from the first and last record. A record with
no time contributes none.

The report is grouped by the order a session identity first appeared and ordered
by ordinal within it. Nothing is sorted by size, cost or time.

### The turn model is the evidence and nothing more

A recorded turn exposes its identity, its ordinal within the session, the window
of recorded times, the epoch ordinals its work reached, its tool call count, the
composition of those calls, and how many of them a nested agent made.

Composition uses the shapes Axiom's evidence model already distinguishes: whole
reads, ranged reads, searches, shell calls, writes, edits, and calls this version
cannot describe. Every call falls in exactly one, so the categories reconcile
against the count. Writes and edits carry the three-way outcome discipline used
everywhere else — succeeded, reported failing, outcome not established — because
an outcome that was never established is not a failure, and a failed write may
still have applied in part.

There is no subagent-spawn category. *(Superseded by ADR 0014, which adds one.
The premise below is false: the metadata was being reported and recorded, so the
category would not have been empty in any real log.)* The current agent does not
report the metadata Axiom needs to recognize one, so a category for it would be
empty in every real log and would say more about Axiom than about the work.
Spawns are counted as uninterpreted, which is what they are to this version.
Nested calls are counted separately, by the subagent identifier they carry.

The classification is duplicated from the profiler rather than shared. This
package may depend on the event model and the timeline; importing the findings
package to count reads would tie a measurement to the analysis that judges them,
which ADR 0007 established it must not. The duplication is a few dozen lines and
is deliberate, in the same way the timeline derives context boundaries the
profiler also derives.

### Consumption is joined on the exact identity, under the existing rules

`Index.MeasureTurns` joins on `(session, turn)` exactly, reusing the same
accumulation and the same withholding that ADR 0005 established: tokens are
summed over the observed requests or withheld entirely, cost is withheld
independently of tokens, and a sum missing part of itself is never reported as
though it were whole.

What is reported is the observed model requests, the four token dimensions and
the agent's own cost estimate. What it is called matters more than what it
contains. It is *observed model consumption*: what the agent reported for
requests it labelled with that turn. It is not the cost of the tool calls above
it, not the cost of the turn, not a session cost, and not a billing figure.
Nothing recorded says which request served which call, and requests are recorded
that no call caused.

A turn with nothing observed reports that nothing was recorded, on one line. It
does not report zero, and it does not omit the subject: telemetry exists only
for the time a receiver was running, and consumption is half of what this
section answers, so its absence has to be visible. This departs from the
finding-level rule in ADR 0005, where an unobserved block is dropped entirely —
there, consumption is optional context for something else; here it is the
question.

### Consumption outside recorded turns is counted, not attributed

Model requests recorded under identifiers no tool call named are neither turned
into turns nor dropped. The section states how many requests belonged to how
many such identifiers, and stops.

It does not say why they exist. The capture makes several readings available and
supports none of them, so the report offers no interpretation, no label, and no
attribution to a neighbouring turn. Only counts are reported: their tokens and
cost would be a second consumption total under a heading unable to say what it
covers, and the smaller claim is the one the evidence carries.

### A turn's consumption is not a subagent's total

The capture established that a nested agent's tool calls kept the launching
turn while model requests around them carried identifiers of their own. A
turn's observed consumption therefore does not contain everything a subagent
launched from it spent. This is stated in the report next to the numbers, not
only here, because a total is read as everything the turn caused unless
something says otherwise. Axiom does not attempt to repair or infer the missing
relation.

### A turn may span epochs

Compaction has been observed opening a context in the middle of a turn, so a
turn's work can be recorded in more than one epoch. Every epoch its work reached
is named. One is rendered as one; several are listed, and past a few the rest
are counted so the line stays inside the report's width.

Nothing about the epoch belongs to the turn. Epoch-level analysis — the
cross-epoch reading of ADR 0010 above all — is not attributed to a turn that
happened to straddle a boundary.

### What is deliberately not here

No per-turn path lists, no findings by turn, no interval or reacquisition
attribution, no session-wide consumption total, no comparison support, no prompt
text, no wall-clock duration, no per-call model cost, no attribution of a model
request to a tool call, and no ranking, ratio, average, score, efficiency or
recommendation. The section answers the product question and audits its own
join. Each of those additions would answer a different question, and most would
require evidence the log does not carry.

## Consequences

Consumption now reaches the report without a finding. On the validation capture,
a report that previously showed none shows what was observed in each of four
turns, plus the 7 requests that belonged to none of them.

The report gains a section between the epochs and the reading analysis, bounded
to the most recently recorded turns, with omissions accounted for on a line of
its own.

`internal/correlate` now depends on `internal/turns`, as it already depends on
`internal/profiler`: it is the layer that joins the two streams. `internal/turns`
depends only on the event model and the timeline.

A reader can now overcount in one new way, and the wording is what prevents it:
turn consumption is per turn and does not sum to a session, both because
telemetry coverage is partial and because consumption exists outside the
recorded turns entirely. The section says so rather than printing a total.

The `Agent` metadata gap is now visible in a second place: those calls appear as
uninterpreted in a turn's composition. Fixing the adapter is left to its own
change, where the evidence for what the current agent reports can be gathered
properly.

*(That change gathered the evidence and found no adapter gap to fix. See ADR
0014.)*
