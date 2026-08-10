# 2. Evidence rules for redundancy findings

- Status: accepted
- Date: 2026-08-10

## Context

Axiom now analyzes the events it records. The first question a profiler has to
answer is not "what repeated?" but "what repeated *without a good reason*?".

The recorded stream shows operations, never intent. Every repeated operation has
at least one innocent explanation: the agent lost the earlier result, the file
changed, the command's output would now differ, the work happened in a context
that no longer exists. A detector that ignores those explanations produces
findings that are technically true and practically worthless, and a profiler
that cries wolf is worse than no profiler.

The first real measurements make this concrete. Two Claude Code sessions
recorded 25 tool calls: 22 reads and 3 shell commands. Inside each session every
file was read exactly once. Across the two sessions, all nine of the second
session's reads repeated a file the first session had already read — and every
one of them was legitimate, because the second session started with no memory of
the first. A cross-session detector would have been wrong nine times out of
nine on the very first log we looked at.

## Decisions

### Repetition only counts inside one context scope

A scope is one agent session and one subagent within it. Work repeated in a
later session is never a finding, because the agent's context may legitimately
have been lost in between. A subagent gets its own scope for the same reason:
it reasons in a context of its own, so re-reading a file the parent already read
is not waste.

A scope also ends when the agent's context is discarded mid-session. Claude Code
starts a session on compaction and on `/clear`, and any `session_start` arriving
for a session already under way is treated as a reset. If that is ever
over-cautious — a resumed session, say — the cost is a missed finding, which is
the direction we want to be wrong in.

### A run ends as soon as repeating the work could be worthwhile

Both detectors track *runs*: sequences of the same operation with nothing in
between that would justify doing it again. A run of two or more is a finding;
anything that could have changed the answer ends the run.

For a repeated read, the run ends on a write or edit to that same path. Writing
a different file is not a barrier, because it says nothing about this one.

For a repeated shell command, the run ends on any write or edit at all, since a
code change is the ordinary reason to run something again, and on any *other*
shell command, since commands change the world and Axiom only stores a digest of
them.

For both, the run ends on any operation whose effects Axiom cannot observe. That
is the rule that turns "we saw no modification" into "there was no window in
which a modification could have escaped us", and it is the difference between a
finding we can defend and a guess.

### Unrecognised tools are opaque, not harmless

Metadata extraction is an allowlist, so a tool Axiom has not reviewed —
`NotebookEdit`, any MCP tool — produces a tool call with no metadata at all.
Treating those as harmless is exactly how a legitimate re-read gets reported as
waste, so they end every run in the scope. Subagent calls are treated the same
way: a nested agent can do anything, and its own tool calls belong to a
different scope.

Background shell commands are opaque for a related reason: they keep running
after the call returns, so what happened between two of them cannot be bounded.

### Only successful operations count, but failures still end runs

A failed read returns nothing, so it neither repeats earlier work nor makes
later work redundant; it is ignored entirely. Re-running a command after it
failed is a retry, not redundancy, and retry analysis is deliberately a later
milestone.

Failed writes and edits are treated as barriers anyway. A failed edit may still
have applied in part, and suppressing a finding costs less than inventing one.

### One confidence level

`ConfidenceHigh` is the only level Axiom emits. It means the repetition happened
inside one context scope and every operation in between is known to leave the
observed state unchanged.

Confidence describes evidence quality, not severity: it says nothing about how
much the repetition cost. The field exists as part of what a finding *means*,
and a second level will only appear alongside a rule that genuinely earns it.

### Repeated search is deferred

The obvious third detector cannot be built honestly on today's schema. Search
metadata records the pattern digest, root, glob, and output mode, but not
`head_limit`, `offset`, or the context-line and case-sensitivity flags. An agent
paging through results produces two byte-identical canonical events for two
different operations, which is a guaranteed false positive rather than a
theoretical one. Recording those parameters is privacy-neutral — they are
numbers, booleans, and enums, never user content — so the detector becomes
straightforward once the schema carries them and there is real search traffic to
validate against.

### The reader accounts for what it could not read

A dropped record is not neutral to a profiler: losing a write is exactly what
would turn a legitimate re-read into an apparent redundancy. The JSONL reader
therefore counts malformed records, a truncated final record, and records from a
schema it does not know, and `axiom profile` prints those counts rather than
presenting a partial analysis as a complete one.

## Consequences

- Axiom reports far less than a naive detector would, and silence is a valid
  result. The dogfooded sessions produce zero findings, which is correct.
- Findings claim only that no modification was *observed*. A file changed by
  something outside the agent — the user's editor, a file watcher, a formatter
  on save — is invisible, and no wording in the report suggests otherwise.
- Sessions that lean on shell commands will produce few read findings, because
  every command ends every read run. If real logs prove that too quiet, the
  answer is a second confidence level with its own stated rule, not a quieter
  barrier.
- Events are analyzed in the order they were appended. Hooks run as parallel
  processes, so that order only approximates execution order, and a barrier
  recorded late could in principle hide or invent a run.
- The profiler depends only on the canonical event model, so a second agent
  needs an adapter and nothing else.
