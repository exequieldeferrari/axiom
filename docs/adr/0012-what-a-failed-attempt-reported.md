# 12. What a failed attempt reported

- Status: accepted
- Date: 2026-08-12

## Context

ADR 0006 gave `repeated_failure` two confidence levels. `ConfidenceHigh` meant
every attempt reported an identical non-empty failure digest; `ConfidenceMedium`
meant that could not be established, because the attempts reported different
text or because one reported none. The digest is a domain-separated hash of the
whole error string the agent handed the adapter, and the string itself is never
stored.

That ADR was careful about what a shared digest means — "an identical
description, not a cause" — and still put it on a scale. A reader meeting
`HIGH` next to `MEDIUM` reads the first as better evidence of something. The
question this ADR settles is whether it is.

It is not. Controlled captures produced counterexamples in both directions, and
the corrected model replaces one grade with two observations that do not rank.

## What was observed

Three controlled captures, all outside the repository, each with its own
`AXIOM_DATA_DIR`. Claude Code 2.1.228, hooks installed per scratch project by
`axiom init`, headless. No event log was edited by hand. These are controlled
captures and not organic behavior: they establish what the records contain and
what the code does with them, not how often any shape occurs in real work.

**Weak evidence graded HIGH.** ADR 0011's capture ran four *different* commands
that each exited 1 having printed nothing. All four produced the failure digest
`29a4a91cf7e0`, because Claude Code reported the same seven-byte string for
every one of them: `Exit code 1`. Repeated silent failures of one command
therefore agree exactly, and the run is graded `HIGH` on the strength of the
agent having nothing to say.

**W1 — rich evidence graded MEDIUM.** A two-test Go package where
`Reserve(10, 4)` returns 6 instead of 7. `go test ./...`, three separate Bash
calls, one turn, nothing in between. All three reports named the same failing
test `TestReserveLeavesRemainder`, the same source location `stock_test.go:14`,
and the same assertion `Reserve(10, 4) = 6, want 7`. The three digests were all
different. The whole difference was a Unix timestamp inside an output path,
four seconds apart across the three runs, describing nothing about the failure.
The run was graded `MEDIUM` and reported as *identical failure reporting was
not established*, discarding the fact that every attempt described the same
assertion.

**W2 — a stable realistic failure.** A Go package calling an undefined
function, built three times the same way. One digest across all three, exit 1,
`HIGH`. Realistic failures do sometimes repeat byte for byte; the point of W1 is
that they need not, and that whether they do turns on content that has nothing
to do with the failure.

**Non-shell and interrupted failures.** A `Read` of a missing path reports
`File does not exist.`, with no exit-status line. An interrupted Bash call
reports `aborted`. Neither is a shape the exit-status recognizer knows.

**Organic logs could not measure this.** The user's own event log held no failed
tool calls at all, so the effect on real findings could not be counted, and the
persisted records carry no error text to reclassify retrospectively. The
captures are the whole of the evidence.

## Decisions

### Raw report identity is not a confidence scale, and confidence goes

Two byte-identical reports establish that the agent wrote the same string
twice. That is a fact about text. It is not more evidence, better evidence, or
evidence of a shared reason for failing, and the captures show it moving
opposite to the amount actually reported: the silent case agrees perfectly while
saying nothing, and W1 disagrees while saying the same thing three times.

`ConfidenceMedium` existed only to express this distinction, so it is deleted.
That leaves `ConfidenceHigh` as the only level, applied to every finding of
every kind — a constant, which carries no information for a reader to use.
ADR 0002 said a second level would appear only alongside a rule that earned it;
the rule that appeared to earn it did not, and the honest correction is to
remove the type rather than keep a label that ranks findings against nothing.

`Confidence` is therefore gone from `Finding` and from the report. A finding is
either established by Axiom's deterministic rules or it is not emitted, and the
`Findings` section says so in the sentence under each headline instead of in a
badge above it.

### Two orthogonal observations replace the grade

`Finding` gains `Reporting` and `Reports` for `repeated_failure`.

`Reporting` is what the attempts' failure reports were observed carrying:

| State | Established by | Establishes | Does not establish |
| --- | --- | --- | --- |
| `detail` | every attempt's report carried content beyond a recognized exit status | the agent wrote more than a status each time | what that content was, that it describes the failure, that the attempts described the same thing |
| `status_only` | every attempt's whole report was a recognized exit status | the agent wrote nothing beyond a status | that the command was silent, that either output stream was empty, that no description existed elsewhere |
| `no_text` | no attempt was reported with any text | the agent reported failures without text | anything about the commands |
| `mixed` | attempts were classified and did not all agree | the attempts were reported differently from each other | which of them was which |
| `unestablished` | at least one attempt could not be placed | nothing | that any attempt reported little or much |

`Reports` is whether the reports were the same string, by the existing digest:
`identical`, `differed`, or `unestablished` where an attempt reported no text
and there was nothing to compare. Having no report is not having a different
one, and the two are held apart.

The dimensions are independent, and all four established combinations are
reachable and pinned by tests: W1 is `detail` + `differed`, W2 is `detail` +
`identical`, the silent capture is `status_only` + `identical`, and two silent
attempts that exited differently are `status_only` + `differed`.

Neither is a grade. The report states both and the caveat says outright that
neither ranks the other, because reports differ over elapsed times and paths,
and match most easily when there was nothing in them to differ.

### Classification happens at ingestion, from one reading

The adapter is the only place the raw report exists, so it classifies there and
stores the classification in `Failure.Reporting`. Nothing about the text
survives: not the text, not its length, not a prefix, not a fragment, and no new
hash. A closed set of four words leaks less than the string it replaces, which
is the point.

The exit status and the classification come out of a single reading of the
report. `parseExitCode` became `recognizeStatus`, which returns the status it
recognized on the first line along with whatever followed it, and the
classification is derived from that same result. A second parser would be free
to disagree with the first about what was recognized.

The conservative outcome is `unrecognized`, never `status_only`. A report in an
unfamiliar shape is a report Axiom could not read, not a report that said
nothing, and sparse is never taken for empty. Three shapes exercise the
boundary: a status followed only by whitespace, a status followed by a bare
newline, and a report whose status arrives after the output are all
`unrecognized`, and only the first line is ever read for a status, so
`make: *** [test] Exit code 2` inside the output cannot be mistaken for the
status the call exited with.

### The aggregate keeps disagreement

The finding folds every attempt in, not the last one. Both dimensions only ever
weaken, and neither can re-form: a third attempt matching the first does not
undo the second having differed from it.

One attempt that could not be placed leaves the whole run `unestablished`
rather than `mixed`. `mixed` says the attempts were read and came out apart;
a run holding an attempt that was never read has not established that.

### No schema-version bump

`Failure.Reporting` is additive and optional. Absence means the record carries
no classification, which is exactly what every record written before this field
says, and it is never read as a positive state — the profiler maps absent and
`unrecognized` alike to `unestablished`.

A bump would be actively destructive here. `store` rejects any record whose
`schema_version` is not the current one, so raising it would make every existing
log unreadable in order to add a field that older readers ignore and newer
readers already treat as absent. The rule earns a bump when an existing field
changes meaning. Nothing here does.

### The failure digest leaves the report

`Failure digest  30303e9585c1…` is no longer printed. A command digest earns its
line because the same command carries the same digest everywhere, so a reader
can match findings by it. A failure digest names one exact string of the agent's
display text, which recurs nowhere, and its only use in the report was to be
read as an identity the failures shared. The digest stays in the event and
inside the profiler, where it answers `Reports` and nothing else.

### A timed-out command is recorded as a success

The captures found that Claude Code reports a Bash tool timeout through
`PostToolUse` as `outcome=success` with a null error and no timeout marker
anywhere in the payload. Axiom therefore records a timed-out command as having
succeeded, which can end a failure sequence and set `LaterSuccess` on it.

This is left exactly as it is. Axiom follows the outcome the agent exposes, and
inferring a timeout from a duration or a missing result would be Axiom deciding
that the agent was wrong. It is recorded here as a limit on what
`Same command later succeeded` establishes.

## Consequences

- Axiom emits no confidence at all. A future finding that genuinely needs
  grading has to bring the rule with it, as ADR 0002 asked, and it should not
  reuse a type whose last occupant graded the wrong thing.
- The `Findings` section lost its leading badge column, so every finding's
  detail lines moved left with it. `repeated_read` and `repeated_shell`
  detection is untouched; only the column they were printed in is gone.
- `repeated_failure` gained two lines and lost one. Both new lines are always
  printed, including where nothing was established, because a missing line
  reads as the answer the other one gave.
- Records written before this change classify as `unestablished` forever. The
  error text was never stored, so nothing can be recovered, and a log has to
  accumulate new failures before the reporting line says anything.
- The classifier depends on an undocumented Claude Code format. That dependency
  is bounded on purpose: when the format changes, reports become
  `unrecognized`, findings keep being emitted, and the line says nothing was
  established rather than saying something false.
- Nothing here claims the attempts failed for the same reason, met the same
  state, or need the same fix. Two attempts can report identical text for
  different reasons, and W1 shows one reason producing three different texts.
