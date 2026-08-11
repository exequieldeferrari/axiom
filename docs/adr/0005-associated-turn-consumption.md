# 5. Associated turn consumption

- Status: accepted
- Date: 2026-08-11

## Context

ADR 0004 joined findings to the results their repeated calls returned, and left
token and cost evidence unused. That evidence exists: an agent reports what each
model request consumed, against the turn it served.

The question this answers is not "what did the repetition cost". Nothing
recorded can answer that. It is "what was going on where this happened" — a
finding on its own says a file was read three times, and says nothing about
whether that happened in a turn that consumed a hundred tokens or a hundred
thousand. The second is what tells a reader whether the finding is worth their
attention.

That makes this the most dangerous number in the product. A token count printed
beside a defect reads as the price of the defect, and no amount of care in the
data model survives a layout that invites the wrong reading. The decisions below
are as much about how it is presented as about how it is computed.

This ADR extends ADR 0004 in one respect: model requests are now indexed, by
turn. Its decision that model requests are never charged to an individual tool
call or presented as the cost of a finding stands unchanged.

## Decisions

### Association is not attribution

Consumption is reported for the turns a finding's calls happened in. A turn is
an execution context identified by the agent's own turn or prompt identifier,
and captures show that several model requests and tool calls can share one. The
repetition a finding describes is one part of what happened under that
identifier, and nothing in the recording separates it from the rest.

So the two kinds of evidence are named differently and never mixed. The
finding's own measurements are attributable to it. Consumption is context that
was observed nearby, in the agent's own accounting sense of nearby, and the
report says so on the line above the numbers.

Nothing is ever labelled wasted, saved, avoidable, or redundant tokens or cost.
Not as a default, not with a caveat, not behind a flag.

### A turn is a session and a turn identifier

Consumption is keyed on `session_id + turn_id`, the same identifiers the tool
join already uses, minus the invocation. That key is the whole definition Axiom
relies on: a turn is the execution context an agent labels, not a claim about
what an agent does per prompt. Turn identifiers are the agent's own and are not
known to be unique across sessions, so the session stays part of the key for the
reason given in ADR 0004.

A record naming no turn is dropped rather than pooled under an empty identifier,
which would merge unrelated sessions into a single bucket.

### Every model request in the turn counts

Several model requests can carry the same turn identifier. A real captured turn
with five tool calls reported four requests, and the fixture carries two so that
the summing is exercised against a shape the agent actually produces.

All of them count. Selecting one — the largest, the first, the one nearest in
time — would be picking a number rather than reporting one.

### Every affected turn counts once, including the first call's

A finding's affected turns are the distinct turns of all its calls. `Calls[0]`
is excluded from measured redundant output because it did the work, but its turn
is included here, because a run is one piece of behavior and the context it
happened in began there.

Distinctness matters more than it appears. Three calls in one turn are one turn,
not three: counting per call would multiply the consumption by exactly the
repetition being reported, which is the specific wrong number a reader would
most readily believe.

### Affected turns and observed turns are different facts

`Calls` establishes where the behavior happened. Telemetry establishes what
Axiom saw of it. The two are recorded separately, as `AffectedTurns` and
`ObservedTurns`, and the report states both whenever they differ.

Missing telemetry reduces coverage; it never redefines the finding. Counting
only the turns that happened to be recorded would let a receiver that was
started late make a finding look like it took place somewhere smaller than it
did, and the reader has no way to detect the difference. A finding that spans
three turns with one recorded is reported as one of three, not as one.

The wording follows from that. The heading names the coverage, and the sentence
under the numbers names the turns they actually came from, so that neither can
be read as speaking for the turns where nothing was recorded. Nothing is
inferred for those turns and nothing is rendered as zero for them.

### Findings sharing a turn each report all of it

Two findings in the same turn both report that turn's full consumption. Neither
is a share of it and there is no way to divide it, so each is presented whole
and independently.

The consequence is that these totals must never be added across findings. The
report therefore has no session-wide consumption total: a column that cannot be
summed must not be presented as though it could.

### A call with no turn withholds the whole block

If any of a finding's calls names no turn, no consumption is reported, even when
the other calls do name one. Reporting the turns that happen to be identified
would describe a narrower context than the finding actually spans, while looking
like the whole of it.

### Counts are summed together or not at all

Input, output, cache read, and cache creation are kept as the four separate
dimensions the agent reports. There is no total: adding cache reads to output
tokens produces a number that means nothing and prices nothing.

If any observed request reported no counts, all four are withheld. The canonical
model records an unreported dimension as zero, so Axiom cannot tell a missing
count from a measured one, and a rule that dropped dimensions individually would
be enforcing a distinction the record does not preserve.

Cost is withheld independently, because it is recorded independently: one
missing cost estimate says nothing about the counts, and one missing count says
nothing about the cost. Cost is also the agent's own estimate rather than a
billing figure, and is labelled as observed rather than incurred.

Result bytes are never converted into tokens, and turn cost is never divided
into a per-call figure.

### Consumption is observed, never total

A receiver started midway through a session records part of a turn, and nothing
in the data says so. Every total here is therefore what was observed, and the
report never claims it is what the turn consumed. The report says "observed"
rather than "consumed" for that reason, and does not claim to describe
everything the agent did in a turn — only what was recorded of it.

This is also why a turn with no records contributes nothing rather than zero,
and why a finding with no observed request in any of its turns shows no block at
all. Nothing is rendered as unknown, for the reason ADR 0004 gives: rows saying
"not measured" train a reader to skip the rows that are. Coverage is the one
exception, because a total that covers part of a finding while looking like all
of it is the failure this ADR exists to prevent.

One consequence is accepted rather than solved. A canonical usage record for a
model request carries no identity of its own, so the same request delivered
twice would be counted twice.

This is a boundary Axiom drew, not a gap in the wire format. Claude Code's
export carries `request_id`, `client_request_id`, and `event.sequence`, none of
which the canonical model keeps: PR #4's allowlist admits only fields with a
use, and identifiers exist to be stored carefully or not at all. Deduplication
would therefore mean deciding which of them is stable enough to rely on and
whether it is safe to persist, and neither question is answered here. Until it
is, "observed" is the honest description of a stream Axiom does not control.

### The profiler still does not know telemetry exists

Correlation remains the only component that sees both models. The profiler
produces findings from behavior, the correlation layer adds what was measured
and what was observed, and no OTLP type reaches either.

## Consequences

- A finding can now be read with some sense of scale: the same repeated read
  means something different in a turn that consumed four hundred tokens than in
  one that consumed a hundred thousand.
- The report shows numbers it explicitly refuses to interpret. That is the
  intended trade: a reader who wants to know whether a turn was expensive gets
  the evidence, and a reader looking for a savings claim does not find one.
- Consumption cannot be aggregated, ranked, or summed by later features without
  revisiting this ADR. Anything that sums it across findings will double-count
  shared turns.
- Coverage is now part of the product's vocabulary. Anything added later that
  reports observed evidence about a finding has to say how much of the finding
  it covers, or it will read as covering all of it.
- Model request duration is recorded and still unused. Adding it would put a
  latency figure next to a finding that does not explain it.
- The evidence needed for turn-level comparison — the same turn under different
  agent behavior — is now indexed. Nothing here presumes what that comparison
  should look like.
