# 6. Repeated failed attempts

- Status: accepted
- Date: 2026-08-11

## Context

ADR 0002 ruled retries out of the redundancy detectors: re-running a command
after it failed is not repeating work that was already done, and it was left
for a milestone that could look at it properly. This is that milestone.

The behavior worth reporting is not that commands failed. It is that an agent
tried the same thing again with nothing to go on. Distinguishing the two is the
whole problem, and it depends on evidence Axiom did not have measurements for,
so the design was settled against a real capture before it was written.

Three headless Claude Code sessions were recorded in a scratch directory. They
produced five observed failures, and four facts that decided the design.

Every failure carried a duration, an invocation identifier, a turn identifier,
an exit code, and a failure digest. Three attempts of a command with fixed
output produced one command digest, one failure digest, and one exit code.
Two attempts of a realistic failing test command produced the same command
digest and exit code but **two different failure digests**, because the output
carries elapsed times. And a tool call the agent was not permitted to make
produced no event at all: it silently did something else instead.

## Decisions

### The finding is repetition, and the failures only grade it

A `repeated_failure` finding requires the same command digest attempted at
least twice, every attempt failing, with no observable intervention between
them. That much is structural and the capture shows it holds.

Failure identity grades the finding rather than defining it. `ConfidenceHigh`
adds that every attempt reported an identical non-empty failure digest;
`ConfidenceMedium` is the same finding where that could not be established,
either because the attempts reported different failures or because one reported
none. Both are the same behavior; they differ in what is known about it.

Defining the finding on identical failures instead would have been safer and
nearly useless: the one realistic command in the capture changed its output
between two runs of the same failing test. Medium is not a weaker suspicion, it
is a precisely stated smaller claim, and the report words it as what Axiom
could not establish rather than as a hedge: *identical failure reporting was
not established*. That sentence is true of attempts that reported different
failures and of attempts that reported none, without merging the two into a
claim that no evidence exists, and without reaching for a cause.

This is the second confidence level Axiom emits, and ADR 0002 said one would
appear only alongside a rule that earned it. This is that rule. There is no
`ConfidenceLow`.

### An identical failure digest is an identical description, not a cause

`Failure.Digest` is a domain-separated hash of the entire error text the agent
reported, and the text itself is never stored. Equality therefore proves that
the agent described two failures identically, and nothing more. Two attempts
can report the same text for different reasons, and the same reason can produce
different text — which is exactly what the capture shows.

Nothing in the model, the report, or the wording says "same cause", "same root
cause", or "same error". The finding says the same *observed failure*.

Exit codes are tracked separately, because an agent may report one without the
other, and are reported only when every attempt agreed on one. A status that
does not describe every attempt describes none of them, so disagreement
withholds the line rather than summarizing it. An exit code is not independent
corroboration: Axiom parses it out of the same error text the digest covers.

### A sequence stops at a turn boundary, and this detector alone works that way

The redundancy detectors let a run cross turns. This one does not.

The reason is epistemic, not architectural. A repeated read claims that work
was already done and nothing changed it, which is true whoever asked for it. A
repeated failed attempt claims the agent tried the same thing again with
nothing new to go on, and a turn boundary is precisely where input Axiom cannot
see may have arrived. "Run it again" from a user is not the agent repeating
itself, and Axiom has no way to tell the two apart.

Both trajectories in the capture were single-turn, so the recall this costs is
unmeasured but was zero there. The false-positive class it removes is entire.

This is not a general rule for future detectors. It follows from what this
particular claim depends on, and the next detector has to make its own case.

An agent that reports no turn identifier gets no finding at all. Without a
turn there is no boundary to respect, and a rule that cannot be enforced must
not be quietly assumed to hold.

### Barriers are the ones ADR 0002 already established

Anything that could make the next attempt worth trying ends the sequence: a
write or edit anywhere in the tree, a different shell command, a tool Axiom
does not recognise, a background command, a subagent, and a context reset.
Reads and searches do not: they cannot change state, which is the same
asymmetry the shell detector already relies on.

Scope is unchanged — one session and one subagent within it — so a parent and a
nested agent running the same failing command are two sequences, not one.

Interrupted calls are excluded. `FailureKindInterrupt` means a person stopped
the call, and what the agent does next answers them. An interrupt ends the
sequence and starts none.

### A later success is an observation, and its absence is nothing

`LaterSuccess` records that the same command was afterwards observed succeeding
in the same scope, including after a barrier ended the sequence, which is the
ordinary shape: fail, fail, edit, succeed. The finding is patched where it was
already reported rather than being reopened.

It is rendered as `Same command later succeeded  yes` and never as recovery,
resolution, or a fix. A successful later attempt does not establish that
anything in between caused it, and Axiom has no evidence about what did.

Its absence is never rendered. A command that is simply never tried again
leaves Axiom with nothing to say, and printing "no" would turn a gap in
observation into a claim about the agent.

### Failed attempts are measured in attempts and time, never in bytes

What the sequence costs comes from the event stream: the number of attempts,
how many followed a failure, and the tool time of the repeats, excluding the
first attempt, which was worth making.

Bytes are not available. In the capture every failed call produced a
`tool_result` telemetry record and none of them carried a result size, while
every successful call did. PR #5's measurement is therefore structurally absent
here, and the report never renders a redundant-output line for this kind: a
failed attempt returning nothing must not be described as work that produced
something.

Associated turn consumption from PR #6 attaches unchanged, because it needs
only a session and the finding's calls. It stays what it was: context around
the finding, never the cost of it.

### The flat finding keeps three more optional fields

`Finding` gains `FailureDigest`, `ExitCode`, and `LaterSuccess`, joining `Path`
and `CommandDigest` as fields only some kinds populate.

A tagged union, a per-kind payload interface, or a package of its own were all
rejected. Three optional fields on a flat struct is less machinery than any of
them, and the pressure is worth watching rather than pre-empting: the signal to
revisit is a kind that needs a *shared* field to mean something different, not
one that adds an unused field. `Occurrences` and `Redundant` still mean what
they say here, and correlation reads only the session and the calls, so nothing
downstream needed a change.

### The event model does not change

Everything this detector needs was already recorded. No new field is stored, no
command text, no error text, and no output, so the privacy posture is exactly
what it was.

## Consequences

- Axiom emits two confidence levels, and every report that shows one has to
  make clear it grades evidence rather than severity.
- Findings are no longer only about redundancy, so the report's section is
  `Findings` and the summary line no longer promises redundant work alone.
- A retry loop spread across turns is invisible, deliberately. If real logs
  show that is too quiet, the answer is evidence about what a turn boundary
  implies, not a looser rule.
- The detector is shell-only. `ShellOp.CommandDigest` is the only identity for
  an operation that can be repeatedly attempted, and generalizing it to reads
  or searches would invent semantics the model does not carry.
- Failures Axiom never sees stay invisible: a denied call produces no event,
  as the capture confirms, so a quiet report is not a claim that nothing went
  wrong.
- Ordering is still approximate. Hooks are parallel processes, so an
  intervening operation recorded late could in principle hide or invent a
  sequence, exactly as it can for the redundancy detectors.
