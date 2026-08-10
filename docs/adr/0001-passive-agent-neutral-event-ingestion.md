# 1. Passive, agent-neutral event ingestion

- Status: accepted
- Date: 2026-08-10

## Context

Axiom profiles AI coding agents. Before it can explain why an agent wasted
context, time, or money, it has to record what the agent did.

Claude Code is the first supported agent, but the goal is to profile agents in
general. That forces four decisions at once, and they are coupled: how we
obtain telemetry, how failure is handled, what we are willing to store, and
where it goes. Deciding any of them in isolation produces a design that leaks,
interferes, or locks Axiom to one vendor.

## Decisions

### Documented hooks, not transcript parsing

Telemetry comes from Claude Code's documented hook contracts. Claude Code also
writes a transcript JSONL, and reading it would be easier: no installation
step, one file, complete history.

We do not use it. It is an internal format with no compatibility promise, and
building the core event model on top of it would mean the model drifts whenever
the vendor changes an internal detail. The hook payloads are documented, are
versioned in practice, and describe events rather than conversation state.

Only four events are consumed: `SessionStart`, `PostToolUse`,
`PostToolUseFailure`, and `SessionEnd`.

`PreToolUse` is deliberately excluded. It runs *before* a tool call and can
block or rewrite it, which is exactly the position a passive observer should
not occupy. The cost is real and is documented in the README: Axiom cannot see
tool calls that were denied or rejected before execution, so its tool counts
are a lower bound.

### Fail open, always

The hook entrypoint always exits `0` and never writes to stdout.

Both halves matter. Claude Code interprets a non-zero exit as a hook error and,
on some events, feeds the hook's stderr back to the model; a crash in Axiom
would become input to someone's coding session. And stdout from a
`SessionStart` hook is injected into Claude's context as instructions, so even
a stray debug line would silently alter agent behaviour.

Losing an event is therefore always preferable to reporting a problem. Malformed
JSON, an unknown event, a missing identifier, an oversized payload, and an
unwritable data directory all produce the same outcome: nothing is recorded and
the agent never learns that Axiom exists.

Payload reading is bounded. Hook payloads embed tool input, so a large `Write`
carries an entire file; unbounded reads would let a hostile or unusual payload
grow a hook process without limit.

### Metadata-first, with an allowlist

Axiom stores what an operation *was*, not what it *contained*. File contents,
edit strings, tool output, error text, prompts, shell command text, and search
patterns are never written to disk.

Repetition is still detectable because Axiom stores SHA-256 digests of the
sensitive values. A digest answers "did this exact command run before?" without
storing the command, which routinely carries credentials. Digests are
domain-separated — the hash input is prefixed with a domain and a NUL byte — so
an analyzer that groups operations by digest cannot mistake a shell command for
a search pattern with the same text.

Extraction is an allowlist keyed on tool name. An unrecognized tool, including
every MCP tool, yields no metadata. A denylist would leak the first time an
agent gained a tool nobody had reviewed.

File paths and the working directory are the exception: they are stored in
clear text, because a finding that names `internal/acm/manager.go` is actionable
and one that names a hash is not. Paths appear in exactly three places in the
schema, so a future strict mode has a bounded job.

Metadata is grouped by operation shape (file, shell, search, subagent) rather
than flattened into one struct. A flat struct would have to overload a single
`path` field for both "the file that was read" and "where a search started",
and an analyzer counting touched files would silently include search roots.

### Append-only JSONL

Events are appended to a single local file as one JSON object per line.

The workload is append-only, single-user, and modest. JSONL needs no dependency,
is inspectable with `tail` and `jq`, and degrades well: a damaged line costs one
event rather than the log. SQLite would add a dependency and a schema migration
story to a project that has no queries yet.

Each record is serialized completely before the file is opened, so an event that
cannot be encoded writes nothing and leaves earlier records byte-identical.
Records are capped so that a single append stays small, which is what makes
concurrent appends from parallel hook processes safe in practice.

The location follows platform convention, with `AXIOM_DATA_DIR` as an override.
Go's standard library has no `UserDataDir`, and the cache directory is the wrong
home for data that must not be purged.

## Consequences

- Adding an agent means writing an adapter, not changing the event model.
- Axiom under-reports tool activity, and every future analyzer must treat its
  counts as lower bounds.
- Sessions may never end, and a Claude session is not a unit of work: `/clear`
  and compaction start new ones.
- Analyses that need file *content* identity, such as "read repeatedly while
  unchanged", are not possible from the stored data yet. Capturing a content
  digest is deliberately deferred to the milestone that needs it, where its
  semantics can be validated. Filesystem metadata gathered after the fact was
  rejected: `stat` on a `Read` reports the whole file rather than the range that
  was read, and on a `Write` or `Edit` it reports the post-operation state,
  which invites misleading evidence.
- `schema_version` is written from the first event so stored history stays
  readable as the model evolves.
