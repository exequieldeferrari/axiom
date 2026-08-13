# 20. Findings are not cross-capture evidence

- Status: accepted
- Date: 2026-08-13

## Context

ADR 0017 built `axiom compare` and deferred one thing explicitly. Findings were
left out because ten real recordings of one workload produced none of them, so
there was no evidence about how a finding count behaves across repeated
recordings, and because what evidence existed said the count was
boundary-dependent. That ADR called it a deferral rather than a refusal and
named what would resolve it: captures that exercise findings on both sides.

This ADR resolves it, in the other direction. The captures were built, the
question was investigated before it was scoped, and the answer is that findings
are not the kind of thing two captures can be compared by. The deferral becomes
a refusal.

The reason is not that findings are noisy, and an ADR that said so would be
undone by the first person who produced two captures with a finding on each
side. The result is structural:

> A profiler finding is a predicate over a sequence of calls, and that sequence
> is delimited by boundaries in the recording. So the presence of a finding, its
> absence, and every quantity on it can change while the recorded work does not.

Everything below is the evidence for that sentence, and then what follows from
it.

## What was established

The cases were run against the packaged binary, on synthetic logs written to
isolated data directories, with `axiom profile` and `axiom compare` reading them
unmodified. They are deterministic fixtures rather than recordings of an agent:
they control the input exactly, which is what lets them falsify a claim about
semantics. How often each shape occurs in real work is a separate question and
is not established here.

### A context reset changes the finding without changing the reading

Three successful whole-file reads of one path, by the session scope, in one
turn, with nothing in between:

| Recorded | Whole-file reads | Findings | Read again in a later context epoch |
| --- | --- | --- | --- |
| all three in one epoch | 3 | one `repeated_read`, 3 occurrences, 2 redundant | nothing — there was no boundary |
| one read, reset, two reads | 3 | one `repeated_read`, 2 occurrences, 1 redundant | one path, across two epochs |
| one read, reset, one read, reset, one read | 3 | **none** | one path, across three epochs |

`axiom compare` over the first and the third reports `Whole-file reads 3 3 same`.
The recorded work is identical and Axiom says so. A findings comparison over the
same two captures would have reported one finding against none, and a reader
would have taken the reading to have stopped.

This is `Profiler.reset` working exactly as ADR 0002 intended: after a reset the
agent's context may legitimately no longer hold what it held before, so
repetition across one is not repetition the profiler will judge. Nothing here
argues for changing that. The point is narrower and it is about comparison: the
rule makes a finding a statement about a span of the recording, and two captures
do not have the same spans.

### The distinction from reacquisition is preserved, and it is what breaks the comparison

The third column above is the other half of the same evidence. The reading that
leaves the findings section arrives in `internal/reacquire`, which is the
complementarity ADR 0010 designed and which the seam test pins in both
directions. The two analyses answer different questions and neither is a weaker
form of the other.

That is a feature within one capture and a defect across two. The same three
reads move between two sections depending on where a `session_start` fell, so a
findings difference would move in one direction while the block beneath it moved
in the other, and a reader would have to reconstruct the migration by hand to
avoid the wrong conclusion. Merging the two concepts to make the comparison
easier was considered and rejected outright: their boundary is the reason either
of them can be trusted.

### Delegation moves the finding to another scope, and the kind and path still match

Three successful whole-file reads of one path, recorded two ways:

| Recorded | Whole-file reads | Finding |
| --- | --- | --- |
| the session scope reads it three times | 3 | one `repeated_read`, session scope, 3 occurrences |
| the session scope reads it once, a launched agent reads it twice | 3 | one `repeated_read`, **subagent scope**, 2 occurrences |

Both captures record one `repeated_read` at the same path string. A comparison
keyed on the finding kind and the path would match them and report no
difference.

They do not establish the same thing. The first is one reasoner reading a file
it had already read. The second is a nested reasoner obtaining a file
separately, which ADR 0016 established is the ordinary shape of delegation and
declined to call a defect, because the record says nothing about what either
agent was given. Relating the two would turn a difference in delegation topology
into a statement about behavior, which is the failure ADR 0019 refused for
harness attribution.

Scope cannot rescue it either. The only scope identity in the model is an
agent-generated handle that means nothing outside the session that issued it,
so there is no way to say that two captures name the same scope. The rest of
the product already treats it that way: reading across related agent scopes
prints `agent 1` and `agent 2`, numbered within the one report. The finding
above prints `session 11111111 · subagent agent-a`, which is the handle itself,
and it is legible only because the capture it came from is on screen with it.

### A repeated failure survives or vanishes on a boundary the command did not move

The same command digest, the same two failures, the same reports:

| Recorded | Finding |
| --- | --- |
| both attempts in one turn | one `repeated_failure`, 2 attempts |
| the attempts in two turns | **none** |
| an `Edit` recorded between the attempts | **none** |

The first boundary is ADR 0006's: a sequence of failed attempts is confined to
one turn, because a turn boundary is where input Axiom cannot see may have
arrived. The second is ADR 0002's barrier rule. Both are right, and both mean
that whether the finding exists is decided by something other than the commands
that were run.

The command digest itself is the strongest cross-capture identity in the model
and it was tested as one. `digest.Command` is an unsalted, domain-separated
SHA-256 of the exact command text, so two independently recorded captures of the
same text produced the same digest, on the same terms everywhere: no session, no
machine, no checkout in it. It is also brittle in exactly the way exact text is.
One extra space produced a different digest. `cd /repo && go test ./...`
produced a different digest from `go test ./...`. A command carrying an absolute
path digests differently under a different checkout, and Axiom cannot tell that
this is what happened.

Two limits matter more than the brittleness. A digest is a stable identity for
one exact string and not for a finding: matching it establishes that both
captures recorded that text, and the finding around it still exists or not
according to the boundaries above. And Axiom records no project identity —
ADR 0019 established that it must not be reconstructed — so the same digest in
two captures does not establish that they are recordings of the same task, the
same repository, or the same intent. `go test ./...` is one digest everywhere it
is typed.

### A path is not an identity two captures share

Path identity is the exact recorded string, with no normalization of any kind,
which ADR 0007 decided and ADR 0010, ADR 0011 and ADR 0016 each restated. The
same behavior recorded under a different checkout root produced a finding naming
a different string, so no match is possible in either direction. The `/tmp` and
`/private/tmp` aliasing that ADR 0007 observed in an ordinary session and
ADR 0010 met again splits one file across two rows inside a single capture,
which is the same defect before a second capture is involved at all.
Normalizing to close the gap would mean asserting an equivalence
on the strength of a project root Axiom never observed, which is the
reconstruction ADR 0018 and ADR 0019 refuse.

### Absence is not a zero, and it has no representation at all

A finding that is not there is produced by at least: a context reset between the
calls, an agent scope between them, a turn boundary for a repeated failure, an
intervening write, edit, other command, background command or unrecognised tool,
a call rejected before it ran and therefore never recorded, a record the reader
could not decode, an attempt carrying no turn identifier, and a detector that
does not look for the behavior at all.

None of these is distinguishable from any other in the output, because a missing
finding produces nothing. Every other absence in Axiom carries its own state: a
harness component is `absent` or `unreadable` or `not_followed`, a withheld byte
total is a dash under a stated rule, telemetry is `absent` or `unreadable`, a
turn boundary is `unknown` rather than none, and the reading sections print
denominators so that three different zeros can be told apart. Findings have no
denominator, and ADR 0007 declined to give them one on purpose: its denominator
would be sequences of calls, which is the quantity this ADR has just shown moves
with the boundaries.

So a comparison cannot state that a finding `appeared`, `disappeared`, was
`introduced`, `removed`, `fixed` or `no longer` occurs. Those words all assert
that one side established the absence, and no side ever does. This is the same
rule ADR 0019 applied to provenance, arriving at the opposite outcome for the
opposite reason: there, `appeared` is available precisely because the other
observation established that the path was not there.

### Findings are derived now, and the detector has already changed

Findings are not persisted. They are derived at report time from the records, so
the current binary applies today's detector to both sides, which is symmetric
and is also not what either capture reported when it was recorded.

Nothing distinguishes the two readings. No detector identity is stored, and
`SchemaVersion` cannot stand in for one: it is pinned at 1 and, by the rule
ADR 0012 set and ADRs 0015, 0016 and 0018 followed, is deliberately not bumped
for additive fields, so it establishes record compatibility and says nothing
about the analysis. That the difference is real rather than theoretical is
already recorded: ADR 0012 deleted `ConfidenceMedium` and then `Confidence`
altogether, so bytes that once reported `MEDIUM` now report two observations
that do not rank. A capture recorded before that change and one recorded after
are analyzed alike today and were not described alike then, and a comparison
would silently present the first as the second.

Historical evidence degrades in the same direction. A record written before
`Failure.Reporting` existed still produces a `repeated_failure`, reported as
`Failure reporting  not established` forever, because the error text was never
stored. That is the honest degradation ADR 0012 designed, and across two
captures it is one more way the same behavior is described differently for
reasons that have nothing to do with the agent.

## Decisions

### Findings are not compared, and this is a refusal rather than a deferral

`axiom compare` gains no findings section, no per-side rendering of findings, no
count of them, no comparison of command digests and no comparison of paths. The
existing report already says findings are not compared; the sentence is extended
so that it also says what a finding recorded on one side alone does not
establish, and that no finding is an established zero.

The refusal is about what the evidence supports today and not a claim that no
future model could compare anything about repeated work. What would have to
change is written down rather than left implicit: a comparison would need a unit
of repetition whose formation does not depend on where recording boundaries
fell, an identity for the thing repeated that survives two checkouts, and a way
to distinguish a behavior that did not occur from one the detector could not
form a finding for. None of the three exists, and none of them is a wording
problem.

### The evidence properties are a review standard, not a type

Comparing findings against harness provenance exposed a pattern worth writing
down. Harness provenance is the surface that has all five of the properties
below, and profiler findings are the surface that has none of them:

| Property | Harness provenance | Profiler findings |
| --- | --- | --- |
| Identity chosen by the observer | a closed list of project-relative paths Axiom itself names | an absolute path the agent named, or a digest of text the agent wrote |
| Grounded at capture time | observed by the hook at the session start and written onto the record | derived at report time, by whichever detector is running |
| Established absence held apart from `not established` | four recorded states, and `unreadable` and `not_followed` never become a change | no representation for absence of any kind |
| A refusal path | `not_established` for every pairing the evidence cannot support, and a capture with two observations is declared uncomparable rather than having one chosen | nothing to refuse with |
| Semantics that do not move with recording boundaries | an observation of bytes at one moment, taken before any behavior | the whole of what a finding is |

The count blocks ADR 0017 shipped sit between the two, and saying where is what
keeps this list honest. They have the observer-chosen identity, the accounting
that tells three zeros apart, the refusal path, and the boundary independence —
the same three whole-file reads count as three however many resets fell between
them, which is the case at the top of this ADR. What they do not have is the
second property: a category is assigned at report time by `internal/work`, not
observed when the call was recorded. They survive anyway because that
classification is a fixed partition of the records rather than a predicate over
a span of them, which is a narrower dependency than a finding's and is why the
second property is stated as grounding rather than as literal capture-time
observation.

So these are the properties the current evidence and the currently defensible
comparison surfaces exhibit, with one surface short of one of them for a reason
worth knowing. They are not proven necessary, they are not proven sufficient,
and a future feature may well be comparable on grounds this list does not
anticipate. What they are is the standard the next cross-capture feature should
be reviewed against, and a feature that fails several of them should expect to
be refused for the reasons recorded above rather than for new ones.

Deliberately, this is a checklist in an ADR and not code. No `Evidence` type, no
`Claim`, no `Confidence`, no evidence framework and no package: Axiom already
shipped a confidence scale, discovered it graded the wrong thing, and deleted it
(ADR 0012). An abstraction over five properties observed on two features would
be the same mistake with more machinery.

### Nothing else changes

`internal/profiler` is untouched: no detector rule, no barrier, no scope, no
reset and no finding field is changed by this ADR, and every one of them is
correct for the question it answers within one capture. `axiom profile` is
unchanged and remains where findings are read. Ingestion, the event model,
`SchemaVersion`, path identity, project identity, detector versioning and the
existing comparison blocks are all unchanged. Nothing is persisted, backfilled
or reconstructed.

The one guard added is a test: the concrete renderings a finding has — the kinds
the profiler emits and the headlines and detail lines the profile prints them
under — must not appear in a comparison, over captures that demonstrably produce
all three kinds. It reads those renderings from the packages that own them, so a
renamed kind stays covered, and it fails if a future change starts leaking
finding detail into compare rather than if the prose is reworded.

## Consequences

- The deferral in ADR 0017 is resolved. That ADR keeps its original text, with
  its status and the two paragraphs concerned marked, on the convention ADR 0013
  established: what an earlier ADR concluded is part of why the later one exists.
- A user who wants findings for two captures runs `axiom profile` on each. That
  is not a workaround. The profile carries the epochs, the turns, the delegation
  and the reading sections that make a finding interpretable, and a findings
  block in a comparison would have stripped exactly the context that keeps it
  from being misread.
- Axiom now refuses a second cross-capture claim on the same grounds as the
  first, and the grounds are written down as a reviewable standard rather than
  rediscovered each time.
- The strongest question this investigation exposed is left open and is not
  started here. Ten controlled recordings of a real delegation workload produced
  zero findings. Whether that means repeated work as Axiom defines it is rare in
  real agent behavior, or that a sequence of calls delimited by barriers is the
  wrong primitive for describing what agents actually do badly, is answerable
  from records Axiom already writes, and it bears on `axiom profile` rather than
  on comparison.
