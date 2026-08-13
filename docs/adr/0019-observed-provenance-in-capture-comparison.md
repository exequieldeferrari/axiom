# 19. Observed provenance in capture comparison

- Status: accepted
- Date: 2026-08-13

## Context

ADR 0017 built `axiom compare` on an operator's assertion that two captures are
comparable, and said outright that the assertion is not evidence. ADR 0018 then
recorded the evidence — what Axiom observed of a project's configuration when a
session started — and deliberately left compare untouched, on the grounds that
comparing provenance across captures is a separate decision with its own
failure mode.

This ADR makes that decision. It is the smaller of two features that were
investigated together, and most of what follows is the record of why the larger
one was refused, because that is the part a future contributor is most likely to
undo.

## The feature that was rejected

The proposal was **Harness Change Attribution**: group recorded sessions by the
provenance they observed, and report what changed in behavior across the
boundary.

> After `CLAUDE.md` changed: tool failures decreased, retries increased, shell
> usage changed, session duration changed.

It was investigated before it was scoped, and the investigation returned a
feature roughly a tenth its size. Seven findings killed it, and they are
recorded here in full because each one is a way the report would have been
confidently wrong.

### The measured noise floor is larger than the effect

ADR 0017 recorded one delegation workload ten times with the repository, the
prompt, the model, the permissions mode, the Axiom binary and the hook
configuration all held constant. Shell calls ranged from 3 to 9 across those ten
recordings. Recorded tool calls ranged from 13 to 20. Turns with work ranged
from 1 to 2.

That is the variation with everything controlled — controls no user of an
attribution feature would have, because sessions differ by task and nothing
recorded says what the task was. A before-and-after difference in those
quantities, over samples that are not repetitions of one workload, is task
variance with a configuration label attached to it. ADR 0017 already refuses to
call any dimension stable on this evidence; attribution would have had to assume
several of them were.

This is one workload on one machine, and the ADR that recorded it says so. It is
cited here as the reason not to build something, which is the direction the
evidence supports: it cannot establish that a dimension is stable, and that is
exactly what attribution would need.

### Settings change what Axiom itself observes

`.claude/settings.local.json` is an eligible component under ADR 0018. It is
also where `axiom init` installs Axiom's own hooks by default, and where Claude
Code writes a permission the user approved.

So the most frequent observed harness change in a real log is either Axiom
installing itself or a permission being granted — and a permission being granted
changes what reaches the log. `event.ToolCall` already records that calls
rejected before execution are not observable, so a recorded call count is a
lower bound whose bound moves with that file. The digest changes, recorded calls
rise, failures shift, and none of it is a change in behavior. It is a change in
coverage that is indistinguishable, in the record, from a change in behavior.

An observation whose subject also governs the instrument cannot support a
before-and-after claim about what the instrument recorded.

### Project identity is not recorded, and must not be reconstructed

Axiom's data directory is machine-global by default. One `events.jsonl`
accumulates sessions from every project on the machine unless the operator sets
`AXIOM_DATA_DIR` per project.

Provenance records paths relative to the project root and deliberately does not
record the root. Re-deriving it at report time means asking the filesystem
whether a `.git` entry exists today, which is both the reconstruction ADR 0018
forbids and a description of this machine now rather than of the session then.
Grouping sessions by digest would therefore report a move between two
repositories as a configuration change, with every component differing at once,
and Axiom would have no way to tell the two apart.

`Event.Cwd` was considered as a proxy and rejected. It is observed, but it is
the session's working directory and not the root Axiom resolved, so it is not
project identity, and putting it beside provenance would invite it to be read as
though it were.

### The other four

- **Reverse causality is the ordinary case.** Instruction files get rewritten
  *because* a session went badly. The configuration change is downstream of the
  behavior, and the work after it is usually different work.
- **There is no task, model or agent-version control.** No task identity exists
  anywhere in the model, and ADR 0009 refused to invent one. `Session.Model` was
  absent from both of the controlled starts ADR 0018 recorded. ADR 0018 declined
  to record the version of Claude Code at all.
- **Retries are not a quantity.** They exist only as `repeated_failure`
  findings, scoped to one session and subagent and confined to a single turn.
  ADR 0017 deferred findings from comparison precisely because their counts move
  with the boundaries they are counted within.
- **A digest has no magnitude.** Axiom stores no content, so a typo fix and a
  rewrite of `CLAUDE.md` are the same observation. Any behavioral difference
  printed beside either is noise beside a fact with no size. This limit is
  permanent and is the correct privacy trade-off.

### What the refusal costs

Nothing that was previously offered. It leaves Axiom unable to answer "did this
instruction change help", which is a question the recorded evidence cannot
answer honestly, and which no amount of presentation would make answerable.

## Decisions

### Provenance is compared; behavior is not compared against it

`axiom compare` reports what Axiom observed of each capture's project-local
configuration and compares the two observations component by component. It
reports no behavioral consequence of any difference it finds, and the report
says so in the section itself:

> Provenance describes what Axiom observed for itself at each capture's recorded
> session start. Nothing compared below is attributed to anything here.

That sentence is printed inside the block rather than only at the foot of the
report, which is a departure from ADR 0017's layout and is deliberate.
Everything after it is behavior, and a boundary a reader meets after meeting the
behavior is a boundary for readers who already agree.

The question the feature answers is therefore not "what did this configuration
do" but **"were these two captures recorded under the same observable
project-local state, as far as Axiom can establish"**. That is evidence about
the validity of a comparison, which is the gap ADR 0017 named when it recorded
that comparability is asserted rather than established.

### Compare is the right surface, and no command was added

Provenance is only interesting here against another capture, and `axiom compare`
is where two captures already meet. A command of its own would have needed its
own capture-resolution rules, its own refusals and its own contract, all of
which exist. `axiom profile` is unchanged.

### A comparison is component by component, with no composite

There is no fingerprint over the components, no count of how many differed, and
no summary of any kind. ADR 0018 refused a composite because a single value
invites being read as a harness identity, and a count of differing components
would be that value with a different name: the components are not
commensurable, and two settings files differing is not twice one instruction
file differing.

### Seven verdicts, and the last is the load-bearing one

| Verdict | Established by |
| --- | --- |
| `same` | Observed on both sides, digests equal |
| `differed` | Observed on both sides, digests unequal |
| `appeared` | Established absent on one side, observed on the other |
| `disappeared` | The same, the other way round |
| `absent` | Established absent on both sides |
| `enumerated` | Observed on both sides, with no digest to compare |
| `not established` | Everything else |

`not established` covers a component either side recorded as `unreadable` or as
`not_followed`, and a component one observation holds that the other does not.
The rule behind it is one sentence: **the observer's limit is never a change in
a project.** A link Axiom declined to read through is not a file that vanished,
and a path an older Axiom never looked at is not a path that was not there — ADR
0018 holds "was not observed" apart from "was observed to be absent" precisely
so that the eligible list can grow without an older record appearing to deny a
new component, and a comparison that collapsed the two would spend that.

`enumerated` exists because the definitions directory carries no digest. Its
observation *is* the enumeration, so both sides reaching it establishes that
both enumerated, and nothing about what either found — that is established by
the definitions, which are components in their own right.

`appeared` and `disappeared` name the two sides of the comparison and never two
points in time. Nothing establishes an order between two captures; which one is
the baseline is the operator's choice, which is why the rendering is "observed
in the candidate only" rather than anything with a tense in it.

### Definitions compare as a set, and only where the set was established

A definition may be reported as appearing or disappearing only where **both**
observations established which definitions there were. An observation does that
by enumerating the directory, and also by finding nothing at it: a directory
that was not there held no definitions.

Where either side recorded the directory as unreadable, as a link it did not
follow, or did not record it at all, the set is not established, and a
definition present on one side alone is `not established`. Reading a directory
Axiom declined to enumerate as an empty one would turn a refusal into a deletion.
A definition the enumeration named but Axiom did not read is `not established`
too: the name is evidence, the file is not.

### A capture with two observations has none to compare by

Provenance belongs to a recorded start, and one session identity can record
several. Where consecutive starts observed identical components, ADR 0018
already reports them as one observation, so a capture that started five times
under unchanging files has exactly one and compares like any other.

Where a capture holds more than one, it has no single provenance, and the
comparison says so instead of choosing. Choosing the first or the last would
present the conditions part of a capture was recorded under as the conditions
all of it was, which is the merging ADR 0018 refused.

Two things put a capture in that state, and the report deliberately does not
distinguish them, because neither leaves one observation to compare by. The
components can have differed between two starts. A start that recorded no
provenance can also sit between two that recorded identical components, which
ADR 0018 does not join across: closing that gap would report a match over a
stretch of the capture where there is no evidence of one.

This is stated and never refused. Adding a refusal would change ADR 0017's
refusal semantics, and it would fail a comparison over a section that is context
for the rest of the report rather than the point of it.

### Silence is reported, and the section is always written

Four states are held apart, because they are four different silences and none of
them is a capture that ran unconfigured:

- the capture recorded no session start at all;
- its starts recorded no provenance, which is what a log written before ADR 0018
  says and what a start Axiom could resolve no project for says;
- it recorded more than one distinct observation;
- it recorded one, and some other start alongside it recorded none, which is
  printed beside the observation as the gap it is.

The section is written even when there is nothing to compare, and it says in
words that whether the two captures observed the same components is not
established, which is not a statement that they differed. A block that vanished
would leave the reader to supply the missing conclusion, and the conclusion they
would supply is that the two captures matched.

### Nothing is read from the filesystem

Every value comes from a record a hook wrote. `internal/harness` still never
opens a file, `project.Root` is not called at report time, and a test pins that
a comparison is byte-for-byte unchanged after the project it describes has been
rewritten and deleted. This is the property the whole feature rests on: a
comparison of two captures recorded weeks apart describes those two moments, not
the machine reading it.

### No schema change

`SchemaVersion` stays 1. Everything here is derived at report time from records
ADR 0018 already persists, and nothing new is written, stored or cached.

### Where the vocabulary is enforced

The causal and statistical vocabulary — caused, because of, resulted in,
responsible for, led to, due to, improve, degrade, effect, impact, correlated,
associated, explains — is tested over the provenance block in every shape it
takes, and the safe majority of it over the whole report beside ADR 0017's
existing judgement list.

The block-scoped test is deliberately narrower than the report-wide one. These
are substring bans, and the rest of the report uses two of these words to deny
exactly what they name: a capture does not *explain* another, and nothing below
is *attributed* to provenance. A report-wide ban would forbid the sentences that
make the point, which is word-policing rather than semantic protection.

## Consequences

- A comparison now states whether the two captures were recorded under
  observably the same project-local configuration, which is evidence about
  whether the comparison below it is worth reading.
- It states that far more often as "not established" than users may expect. Any
  capture recorded before ADR 0018, any capture Axiom could resolve no project
  for, and any component behind a link out of the project reports as a silence.
  Those are honest silences and the report explains each one.
- Two captures whose components all match are still not established to be
  comparable. Provenance is a handful of project-local paths, and the model, the
  prompt, the task, the agent version, user and enterprise configuration, MCP
  servers and everything else ADR 0018 lists remain unobserved.
- Behavioral attribution stays absent, and the evidence that would be needed to
  reconsider it is now written down: repeated recordings of a fixed workload
  across a deliberate configuration change, showing a difference distinguishable
  from the variation ADR 0017 measured with nothing changing at all, together
  with a way to tell a project apart from another project and a way to hold the
  recording's own coverage still across the change. Until those exist, a
  before-and-after report would be a confident answer built on the noise ADR
  0017 already measured.
