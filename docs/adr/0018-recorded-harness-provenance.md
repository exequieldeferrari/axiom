# 18. Recorded harness provenance

- Status: accepted
- Date: 2026-08-13

## Context

Axiom records what an agent did. It records nothing about the conditions the
work was done under, which leaves an obvious question unanswerable:

> What observable coding-agent configuration was this capture produced under?

ADR 0017 built comparison on top of an operator's assertion that two captures
are comparable, and said outright that the assertion is not evidence. The gap
this ADR fills is the evidence: something recorded, at the time of the
recording, about the configuration the session ran with.

The whole risk in the feature is that it is easy to build something that looks
like a complete answer. A single "harness fingerprint" is one line of code away,
and it would be a lie: the agent reads configuration Axiom cannot see, from
scopes Axiom cannot establish. This ADR is about building the small true thing
and refusing the large false one.

## What was observed

### Claude Code reports nothing about its configuration

A `SessionStart` hook was installed in an isolated project against Claude Code
2.1.228, in two conditions — an isolated `CLAUDE_CONFIG_DIR` with no
credentials, and an authenticated run in the user's own configuration — and the
raw payload was preserved verbatim both times. The two agreed:

```json
{"session_id":"…","transcript_path":"…","cwd":"/private/tmp/…/proj",
 "hook_event_name":"SessionStart","source":"startup"}
```

Five fields. There is no configuration in it, no permission mode, no settings,
no instruction file, no list of tools, and no version of Claude Code itself.
The `model` field the adapter reads was absent from both starts, which is
consistent with it being documented as sometimes absent and is a reminder that
what the agent volunteers is not a contract.

`transcript_path` is the only field pointing at anything richer, and ADR 0001
already refused the transcript: it is an internal format with no compatibility
promise.

So the evidence divides four ways, and the division decides the whole design:

| | Source | Available |
| --- | --- | --- |
| 1 | What Claude reports at a start | session id, transcript path, cwd, hook event, source |
| 2 | What Axiom can observe for itself | a fixed set of project-local paths, relative to the root it resolves from cwd |
| 3 | What the environment gives a hook | inherited variables, which may carry secrets |
| 4 | What may shape the agent and is not observable | user, enterprise and command-line configuration; imports; plugins; skills; MCP servers and their responses; the model; the permission mode; the prompt |

Everything recorded here comes from row 2. Row 1 already reaches the log. Row 3
is refused. Row 4 is the reason nothing here may be called complete.

### A capture keeps what it observed

Two isolated projects were built differing in one line of `CLAUDE.md`, each with
its own `AXIOM_DATA_DIR`, and a session was recorded in each against real Claude
Code 2.1.228. Both captures recorded provenance. The instruction digests
differed and every other component matched, which is exactly the difference that
was made. `CLAUDE.md` was then rewritten in both projects and a subagent
definition deleted, and `axiom profile` reported the original digests for both
captures unchanged. No configuration content appeared in either log.

### A hostile repository cannot redirect the reading

Eleven repositories were built against a populated home directory holding an SSH
key, AWS credentials and private notes, each repository carrying one attempt to
have the packaged binary read one of them: `CLAUDE.md` linked to `/etc/passwd`
and to the SSH key, a relative link climbing out with `..`, `.claude/agents`
linked to `~/.ssh`, `.claude` itself linked to `~/.aws`, a chain of three links,
a definition linked out, a link through a linked directory inside the project, a
ring of links, and `.git` linked to the home directory to try to move the
resolved root. Every attempt was refused, no attempted target's digest appeared
in any log, and no target path did either.

The twelfth was a hard link to `/etc/hosts`, which was read. It is the limit
named in the decision below, and `git` cannot create one.

## Decisions

### Provenance is an observation, not a description of the agent

The recorded claim is exactly this:

> When this session start was recorded, Axiom observed these paths, at the
> project root it resolved for that session, in these states.

That is a record of what Axiom looked at. It is not the configuration Claude
Code loaded, and the design never presents it as one. Every name in the feature
carries the limit: observed harness provenance, observed components, observable
configuration. Nothing is called a harness identity or a harness fingerprint,
because that is the claim the evidence does not support.

Two records whose components match establish that those paths held the same
bytes. They do not establish identical prompts, model, model weights, context,
environment, MCP responses, repository state, external services, agent
behavior, or a reproducible execution. Different components establish none of
those either, and in particular do not establish that a configuration change
caused a behavioral difference. This is provenance and not causality.

### Collection happens when the session starts

Provenance describes the session that ran, so it is observed while the session
is starting and written onto the `session_start` record. The alternative —
reading these files when a profile is rendered — was rejected outright. It would
report the machine as it is today and attribute it to work recorded days
earlier, which is not a weaker claim than the one we want but a false one.

The consequence is deliberate: a log written before this existed holds no
provenance, and Axiom does not fill it in. Historical records are never
reconstructed and never mutated.

### It belongs to a recorded start, not to a session or a capture

One session identity can record several starts — compaction and a resume were
both observed keeping it (ADR 0009) — and the files can change between two of
them. Provenance therefore sits on each start. Where consecutive starts observed
identical components the report says so, which is a statement about the
observations and not a claim that one harness spanned them. Where they differ
the observations stay apart, because merging them would invent one harness for a
session observed under two.

A start recorded during compaction is an observation taken at that moment. The
agent's process did not restart, so what it holds is not established to be what
Claude Code loaded at the start of that process either. This is one more reason
the record is about what Axiom looked at and when.

The hook is a separate process, so the observation is taken very near the point
the agent read these files and not at it. A file rewritten in between would be
recorded as the agent did not have it. The record says when Axiom looked, which
is the only thing it can know, and that is why the claim is phrased as an
observation rather than as what the agent loaded.

### Components are a fixed list of project-local paths

Eligibility is a closed list, relative to the project root Axiom resolves from
the session's working directory with the same rule `axiom init` already uses:

| Kind | Path |
| --- | --- |
| `project_instructions` | `CLAUDE.md` |
| `project_settings` | `.claude/settings.json` |
| `local_project_settings` | `.claude/settings.local.json` |
| `subagent_directory` | `.claude/agents` |
| `subagent_definition` | `.claude/agents/*.md`, top level only |

This is a contract about **selection**. Nothing is discovered: no directory is
searched for candidates, the definitions directory is enumerated at a single
level, and the home directory is never searched. A file Axiom found by looking
around would not be evidence that the agent loaded it. Resolving the root asks
whether a `.git` entry exists in each ancestor of the working directory, which
opens nothing and reads nothing there.

Selection alone is not a boundary, because any of these paths can be made to
lead somewhere else, and that is the subject of the next decision.

The omissions this produces are real and are stated rather than papered over —
a session started in a subdirectory has a `CLAUDE.md` beside it that Claude Code
reads and this list does not name, and an eligible file may pull in more through
an import.
The definitions directory is enumerated one level deep, sorted by name, and a
nested directory inside it is not descended into. There is no generalized
filesystem fingerprinting here and none is wanted.

Each component records a status, and the four are held apart:

- `observed` — Axiom read it. A file carries a digest; the directory carries
  the fact that enumeration happened.
- `absent` — nothing was there. A fact about one path at one moment, and never
  a claim that the agent had no such configuration anywhere.
- `unreadable` — something was there and Axiom did not establish it: a
  permission it lacked, a path that was not a regular file, a file past the
  size it will read, or a directory holding more definitions than it will
  enumerate. A component Axiom could not read is not a component that was not
  there.
- `not_followed` — the path is a symlink Axiom did not read through, for the
  reason in the next decision. Something is there, and what stopped the
  observation was the observer, so it is neither of the two above.

The set of components is itself the evidence of what was looked at. A component
that is not in the list was not observed, which is different from one listed as
absent. That is what lets the eligible list grow later without an older record
appearing to deny the new component.

### Eligible paths are resolved inside the project root

A repository is not trusted input. It may have been cloned from anywhere, and a
symlink is an ordinary filesystem entry that `git` carries in a checkout, so any
eligible path can be an instruction to read a file the repository named and the
user never offered: `CLAUDE.md` pointing at `~/.ssh/id_ed25519`, or a
`.claude/agents` pointing at `~/.aws`, which would not read one named file but
list a directory of the repository's choosing and read everything in it. Storing
only a digest is not an answer. A digest of a low-entropy secret is a value that
can be tested offline against a dictionary, and the enumeration case leaks the
shape of a directory before any hashing happens.

Three policies were considered.

**Follow every link**, which is what the first implementation did. Its only
argument is a project keeping `CLAUDE.md` in a dotfiles repository and linking
it in — a real arrangement, and the agent does read through the link. It has to
be weighed against a repository choosing what Axiom reads, and it loses: the
convenience belongs to a user who can also record nothing there, and the risk
belongs to a user who cloned something.

**Refuse every link**, which is the simplest contract to audit. It refuses the
dotfiles case and also `CLAUDE.md -> docs/instructions.md`, which is entirely
inside the project, is what the agent reads, and is not a security question at
all: whatever it resolves to, Axiom could have read directly.

**Follow a link only while it stays inside the project**, which is what was
chosen. The boundary is the project root, which is the same boundary the
eligible list already assumes, and it costs nothing over refusing every link
because both need the same primitive: a check on the final target is not enough
when any intermediate component can also be a link.

That primitive is `os.Root` (Go 1.24). The root is opened once, every eligible
path is resolved through it, and the standard library refuses a path that leaves
it — through `..`, through a chain, through a linked directory, or absolute. On
the four release targets, all Unix, it resolves with `openat` against a held
directory descriptor rather than by checking a string and reopening it, so there
is no window between deciding a path is inside the project and reading it. Go
documents `os.Root` as vulnerable to exactly that race on `GOOS=js`, which Axiom
does not release for; were that to change, the guarantee would need restating
rather than the code.

The guarantee is published as: **no entry a `git` checkout can create will make
harness-provenance collection read a file outside the project root.** The
qualification is deliberate and is not hedging. `os.Root` bounds path
resolution, and there are ways to reach a file that are not path resolution: a
**hard link** inside the project is indistinguishable from the file it names,
and `os.Root` does not prohibit a bind mount or a device node either. None of
those is something a checkout produces — `git` stores files, directories and
symlinks — so the threat this decision exists for, a repository cloned from
somewhere unknown, is covered. An archive extracted with enough privilege is
not, and the wording says so instead of implying otherwise.

The same open is the type check. The file is opened non-blocking and its type
read from the descriptor, so a pipe committed under an eligible name is refused
instead of holding the session start open until Claude Code times the hook out,
and nothing is checked by a name that is then reopened.

A refused path is recorded as `not_followed` and never as `absent`: something is
there, and calling it absent would state that the project has no instructions
when what happened is that Axiom declined to read them. The record does not say
why, and does not separate a link that left the project from one inside it whose
target would not open, because separating them means resolving the link to see
where it went — the thing being avoided. What is recorded is what can be
established without doing that: the path is a link, and Axiom did not read
through it. The eligible path is what appears in the record and the report; the
target is not hashed, not persisted and not printed.

`axiom init` still writes through a symlink into a dotfiles repository (ADR
0008), and that stays as it is. It is a command a user runs deliberately, on a
path that user configured, writing content Axiom generated. Provenance
collection is the opposite in every one of those respects: unattended, on paths
a repository chooses, reading content a repository wrote.

### MCP, user and global configuration are excluded

`.mcp.json` is project-local and was the strongest candidate for inclusion. It
is excluded, and the reason is not caution alone: a server declared there is not
active until it is approved, the approval lives in user-scope state Axiom does
not read, and servers also arrive from user and enterprise scope. Recording the
file would state that these servers were part of the session when Axiom cannot
establish that any of them was. What an MCP server returned is not observable at
all, so even a correct record of the configuration would not describe what the
agent was given.

Skills are excluded for a structural reason. A skill is a directory whose
behavior depends on files beyond `SKILL.md`, and representing it by one file's
digest would present part of a component as the component. Recording it properly
needs a recursive contract this ADR deliberately does not create.

User, enterprise and command-line configuration are excluded because Axiom
cannot establish that any of it applied to the session. Environment variables
are excluded because they routinely carry credentials and a hook inherits them
wholesale.

### The version of Claude Code is not recorded

`agent = claude-code` is already on every event and is a different claim from
`Claude Code version = X`. The version is not in any hook payload. It could be
obtained by running `claude --version` from the hook, and that was rejected: it
puts a process spawn on the session-start path, it invites recursion, it is a
reading of the binary on `PATH` rather than of the process that fired the hook,
and it would be recorded as though the agent had reported it. A version that
cannot be observed is left out rather than guessed.

### Only digests are stored

The contents of instruction files, settings and agent definitions are read,
hashed, and dropped. What is stored is a SHA-256 of the file's exact bytes as
they were read: in full, with no trimming, no line-ending translation, no
decoding and no re-encoding. Normalizing anything would silently call two
different files one file.

The digest is domain-separated the way every other digest in Axiom is (ADR
0001), so a configuration file whose bytes match a shell command does not
collide with it.

Recorded paths are the eligible paths, never the resolved ones. A link followed
within the project means the bytes came from another path in the same project,
and the record still names the eligible one: where a link led is not a fact
about the project and moves without the configuration changing. The target is
neither persisted nor rendered, and a test pins that it appears nowhere in the
encoded record.

Four of the five paths are constants; the fifth is a definition's filename,
which the user chose and which is recorded in clear text under the same
trade-off ADR 0001 already made for file paths. No error text from reading a
configuration file reaches the log or the terminal, because an error can quote
the file.

### The schema version does not change

Provenance is a new optional field on an existing structure and changes the
meaning of nothing already recorded, which is the rule ADR 0012 set and ADRs
0015 and 0016 followed. `SchemaVersion` stays 1. A record written before this
existed decodes unchanged and reports no provenance; a record written with it is
decoded by an older Axiom, which ignores the field. A bump would have been
actively harmful: the store rejects records whose version it does not know, so
every historical log would have become unreadable.

### Where the model lives

Collection is Claude-specific and sits in `internal/claude`, beside the adapter
that already knows what a Claude Code hook is. The persisted representation is
in `internal/event` and is agent-neutral in shape, which is the split ADR 0001
established. `internal/harness` owns the reported view and reads records only —
it never touches a file, which is the only way a report about a session recorded
on Monday can still describe Monday. `internal/analysis` carries the report and
derives nothing from it. Nothing about provenance is decided in the renderer.

There is no composite fingerprint over the components. One would be a single
value inviting exactly the reading this ADR refuses, and a comparison can be
built on the components themselves when there is a reason to build one.

### Collection cannot cost a recording

Axiom is a passive observer. Provenance is collected once per session start and
never on a tool call, every failure is recorded as a component status, and a
project that cannot be resolved records no provenance rather than a project full
of absent components. The session start is written either way. Observing a
project at the bounds — a full definitions directory of sixty-four files — was
measured at about 2.5 ms, against a hook timeout of five seconds. Resolving
every path inside the project root roughly doubled that, from about 1.3 ms,
because each component of each path is opened rather than the whole path being
handed to the kernel at once. It buys the boundary for a twentieth of a percent
of the budget.

A pipe left under an eligible name would be the one input that could cost a
session start: opening one the ordinary way blocks until a writer appears, and
a repository could stall every start it sees. Eligible paths are opened
non-blocking and their type read from the descriptor, so one is refused
immediately.

The bounds exist for a second reason. The store refuses a record over 64 KiB,
and a refused record would cost the session start itself, so what one
observation can produce is capped by construction and pinned by a test.

### `axiom compare` is untouched

Provenance is an evidence primitive and is worth reviewing as one. Comparing it
across captures is a separate decision with its own failure mode — the
temptation to attribute a behavioral difference to a configuration change — and
nothing in this model needed compare to change in order to be validated.

## Consequences

- A capture records what Axiom observed of the project's configuration when the
  session started, and keeps it when the files change.
- Logs recorded before this exist report that no provenance was recorded, which
  is never rendered as a default harness, as no configuration, or as the same as
  another session that recorded none.
- The eligible list can grow without invalidating what is recorded, because the
  components present in a record say what was looked at.
- Axiom now reads a small, named set of project-local paths. It reads them once
  per session start, stores no byte of them, and reports nothing about their
  contents. Every one of them is resolved inside the project root, so a
  repository cannot redirect the reading elsewhere.
- A project that keeps `CLAUDE.md` outside the tree and links it in gets
  `link not followed` rather than a digest. That is the cost of the boundary,
  and it is paid by a configuration Axiom cannot safely distinguish from an
  attack.
- Nothing here supports a claim that two sessions ran under the same harness,
  and the wording in the model, the report, the tests and the documentation is
  what keeps it that way.
