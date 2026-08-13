# 21. Structural reconstruction over finding recall

- Status: accepted
- Date: 2026-08-13

## Context

ADR 0017 built `axiom compare` and deferred one question: whether findings
belong in a comparison. ADR 0020 answered it as a refusal. A finding is a
predicate over a bounded run of calls, and moving a context, turn or agent-scope
boundary changes whether the predicate holds without changing a single recorded
call. Two captures do not share the boundaries, so they cannot share the
findings.

That refusal closed the comparison question and opened a larger one, which ADR
0020 wrote down in its consequences and left open: ten controlled recordings of
a delegation workload had produced zero findings, and it was not established
whether that meant repetition as Axiom defines it is rare in real agent
behavior, or that a run of calls delimited by barriers is the wrong primitive
altogether.

The tempting response was to loosen the predicates. The README, the report and
much of the ADR history give findings a prominent position, and a detector that
fires on nothing looks broken. The alternative response was to check first
whether findings were ever the reason `axiom profile` was worth running.

This ADR records that check and the product decision it produced. It changes no
code in `internal/profiler`.

## The investigation

Fourteen sessions were recorded with Claude Code 2.1.231 on macOS, each in its
own temporary project and its own `AXIOM_DATA_DIR`, with hooks installed and
`axiom observe` receiving telemetry. The workloads were deliberately different
from each other rather than repetitions of one prompt: implementation, small-
and large-codebase exploration, debugging a failing test, iterative repair,
refactoring, delegation twice, a multi-file audit, a resumed multi-turn session,
and three recordings of one debugging workload for comparison. Half ran against
a small seeded service and half against a 119-file Go repository. No prompt
asked the agent to behave badly.

### Observed

The corpus recorded 177 tool calls: 95 shell calls and 74 file operations. The
95 shell calls carried 95 distinct command digests. Nine calls failed, and no
command digest failed twice. The structural surfaces reconstructed 18 context
epochs, 20 recorded turns and 20 agent scopes, including one session whose
identity spanned five epochs, four of them opened by `resume`, and one turn that
launched three subagents which went on to make 18 calls between them.

`axiom profile` reported zero findings across all fourteen.

Exactly two paths in the corpus were read more than once inside one scope, and
neither is a repeated read as Axiom defines one:

| Capture | Path | What was recorded |
| --- | --- | --- |
| K-bigdebug | test output | two reads at disjoint ranges, offset 150 and offset 400 |
| L-bigdelegate | one test file | a subagent read it whole; the parent later read two adjacent ranges of it |

A fifteenth session was recorded as a positive control. It read one file whole,
three times, with nothing in between. `axiom profile` reported one
`repeated_read`, three occurrences, two of them potentially redundant.

Claude's own transcripts were read separately, because Axiom records a digest of
a shell command and never its text. Of 61 shell commands the agent issued, 27
were inspecting files through the shell rather than through a file tool. In four
of the four debugging sessions the agent ran the project's test suite twice and
wrote the command differently each time — `go test ./...` and the same command
with output piped elsewhere. One session used no file-reading tool at all and
did its reading entirely through the shell.

### Deterministically interpreted

The natural sessions did not satisfy the current finding predicates. The control
shows the predicates are wired up and fire when they are satisfied, so the
absence is a property of the recorded activity under those predicates rather
than a defect in the detector.

What blocked a finding was identity, not a barrier. The predicates never got as
far as the barriers: 95 shell calls with 95 distinct digests cannot form a run
of length two under any barrier rule, and two reads at different offsets are not
two reads of the same thing. Loosening barriers — the change the zero-findings
result first suggested — would have changed nothing in this corpus.

The transcript reading establishes that the agent repeated logical operations
whose recorded identities differ. It does not establish that those repetitions
were redundant, and Axiom has no basis on which to call them so: running a test
suite twice while editing between runs is how the work is done.

### Not established

The corpus does not establish that these sessions were efficient, that no work
was wasted, that nothing repeated, or that there was nothing in them worth
investigating. Zero findings establishes that no recorded activity matched the
predicates. It is not a verdict on the session, and this ADR does not treat it
as one.

Fourteen sessions of one agent on one operating system are not a sample of
coding-agent behavior in general. Nothing here supports a claim about what
agents usually do. It supports a claim about what the current predicates matched
in the sessions that were actually recorded, which is the only claim being made.

The three repeated recordings differed from each other by one shell call.
Nothing about that difference is attributed to anything.

## Decisions

### Structural reconstruction is the primary value of `axiom profile`

The report's value is the structure it derives from records that do not state
it, and that value does not depend on a finding being present. It survived the
corpus because every session had structure to reconstruct, while only the
control had a finding.

The surfaces that carried the corpus, each already shipping:

- Context epochs make the boundary explicit, which is the fact a transcript
  never states and the reason the resumed session was legible at all.
- Recorded turns place calls and observed consumption in the unit the agent
  worked in.
- Delegation reconstructs which turn launched which agent and what that agent
  went on to do, which is otherwise buried in a nested transcript.
- Reacquisition names paths read again in a later epoch of one session identity,
  under its own defined semantics, and reports nothing when there are none.
- Harness provenance records the status and digest of the agent's configuration
  paths at each session start, so a capture keeps that evidence without the
  machine it came from having to be reconstructed. It attributes no behavior to
  what it found.
- `axiom compare` reports the structural difference between two captures and
  refuses what two captures cannot establish.

### Findings remain, unchanged and conservative

Findings are attention markers for exact recorded repetition. They are not a
diagnosis of the session and were never established as the reason Axiom is
useful. They are also not removed, demoted out of the report, or deprecated: the
control shows they work, and rare high-precision markers are worth keeping in a
report whose other sections are descriptive.

No predicate, barrier, identity, threshold or scope rule changes. ADR 0002 and
ADR 0006 stand as written.

### Recall is not raised to make findings appear more often

A detector change now requires a demonstrated investigative problem first, and
evidence that Axiom's persisted records can answer it deterministically. "The
sessions produced no findings" is not that problem. It is the observation that
prompted the question, and this investigation answered it by locating the value
elsewhere rather than by making the detector louder.

This is not a permanent bar on new findings. It is a bar on new findings
justified by their own absence.

## Rejected

Each of these was considered against the corpus and rejected:

- **Loosening barriers.** The corpus shows barriers were never reached. The
  change would have produced no additional finding here while weakening a rule
  ADR 0002 grounded in evidence.
- **Treating equivalent commands as one identity.** `go test ./...` and the same
  command piped elsewhere are the same intent and different text. Deciding they
  are one operation is a semantic judgment Axiom cannot make from a digest, and
  it cannot be made from the text either without recording the text.
- **Fuzzy matching, embeddings or model-classified findings.** All three replace
  a stated predicate with a similarity judgment, which cannot be explained to a
  user in the terms the report is written in and cannot be reproduced from the
  records alone.
- **Comparing overlapping read ranges.** Two reads at different offsets are two
  different reads. Calling them one repetition requires a threshold on overlap
  that nothing in the evidence sets.
- **An efficiency, quality or health score.** Axiom does not know intent, and
  every input to such a score would be a count it already prints without the
  claim attached.
- **Reading "no findings" as a good session.** This is the specific
  misinterpretation the corpus made concrete, and it is corrected in the report
  wording rather than encoded as a judgment.

## Consequences

- The README now leads with reconstruction and presents findings as the narrower
  observations they are. The hierarchy the report already had is the one the
  document now describes.
- The empty findings state names the predicate before the scope. The previous
  wording explained only the session and reset boundaries, which invited the
  reader to conclude that findings were absent because the evidence had been cut
  up by resets. In this corpus that was not the reason, and the new wording says
  what was not matched and that the absence establishes nothing about whether
  work repeated.
- Findings will stay rare in sessions like these. That is now a documented
  property rather than a symptom.
- Repetition a human would recognize stays unclassified. The subagent that read
  a file whole and the parent that later read two ranges of it is recorded in
  full and reported by no section. Nothing in this ADR fixes that, and it is
  named here so the gap is not mistaken for coverage.
- Shell remains the largest blind spot. Shell was 54% of recorded calls, and
  Axiom records a digest rather than the text by design. Almost half of those
  commands were inspecting files, which means a large part of the agent's
  reading is recorded as opaque shell. Recording command text would close this
  and is refused on the privacy model ADR 0001 set.
- Understanding some sessions still requires reading the transcript. The
  transcript-level facts in this ADR were obtained that way, not from the report.
- The report is verbose relative to a short session. A seven-call session
  produces a full set of section headings, most of them empty or near-empty.
  This is a real signal-to-noise question and it is deliberately not addressed
  here: collapsing or reordering sections is a rendering decision that deserves
  its own evidence, and it is left as a post-release investigation.
- No new telemetry, no schema change, no new persisted field, and no change to
  what leaves the machine, which is nothing.

## When to revisit

- Repeated real-session evidence exposes an investigative question that the
  structural surfaces cannot answer efficiently, and Axiom's persisted records
  can answer it deterministically.
- A candidate predicate survives the adversarial cases that killed the ones
  above: a file modified between reads, a command legitimately re-run against
  changing state, a parent and subagent both consulting a shared file, and a
  boundary moved without the underlying work changing.
- A different arrangement of the report is shown to reduce the work of reading
  it, which is the verbosity question above and is about rendering rather than
  about detection.
- An adapter for a second agent records activity whose shape differs enough that
  the identity rules need re-examining against it.
