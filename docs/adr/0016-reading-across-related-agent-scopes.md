# 16. Reading across related agent scopes

- Status: accepted
- Date: 2026-08-12

## Context

ADR 0015 related a recorded launch to the calls that reported the identity it
returned. That gave Axiom, for the first time, an explicit statement about which
recorded work belonged to which nested agent, established by a handle the agent
itself put on both records.

Nothing yet read anything *across* that relation. The profiler judges repetition
inside one scope and stops at every context reset. `internal/reacquire` reports
reading across context epochs of one session identity and deliberately sets a
nested agent's reads aside, since the epochs it measures are the session's.
`internal/activity` counts reads by path over the whole log and knows nothing
about who made them. So a file the session read, then both agents it launched
read again, produced three counts in three places and no observation relating
them.

The question this ADR answers is one sentence:

> Which exact paths were read whole, successfully, in more than one agent scope
> that a recorded launch relates?

It is not a judgement about delegation, not an efficiency measure, not a
context-handoff recommendation, and not an execution graph.

## Evidence

The prerequisite was verified at HEAD before anything was built. A launch
recorded against Claude Code 2.1.228 with `description` and `prompt` and no
`subagent_type`, returning an `agentId`, is classified as a launch by
`internal/work`, is related to the nested call reporting that identity by
`internal/delegation`, and leaves nothing orphaned. That is pinned by
`TestTypelessLaunchRelatesToItsNestedWork`, which drives the adapter and the
relation together from the raw payload shape.

The observations this analysis is built on come from the captures behind ADR
0014 and ADR 0015, replayed here through the adapter rather than re-asserted:

**The identity is the only relation there is.** `tool_response.agentId` and the
`agent_id` on nested calls match exactly, within a session, with no near-misses.
Nothing else in the record relates two scopes.

**Order establishes nothing.** A synchronous launch's nested calls reach the log
*before* the launch, an asynchronous launch's after, and two parallel agents
interleaved their work between the launches that started them. Any analysis that
needed parent-before-child ordering would be wrong on evidence that already
exists.

**Fan-out is the common shape.** One scope launching several agents, in one
turn, is what the captures hold most of. Reading repeated across such a fan-out
is invisible to every existing section.

**Nested agents launch nested agents.** A launch record carrying both an
`Event.SubagentID` and a returned `agentId` was captured. Delegation is more
than one level deep in practice.

**Evidence is often absent.** Launches that reported failing return no response
and no identity; every launch recorded before Axiom persisted the identity has
none; and any log that begins mid-flight holds nested work no launch accounts
for.

**Scopes read the same files.** In the replayed capture the session scope and
both agents it launched each read the same module path, and one agent also read
a path nobody else did.

## Decisions

### It is a measurement section, not a finding

The section is *Read across related agent scopes*, printed under the recorded
turns where the launches it relates are described. It carries no severity, no
confidence, no score, no percentage and no recommendation, exactly as ADR 0010
established for reading across context epochs.

The reason is not caution for its own sake. Two agents reasoning separately over
one file is the ordinary shape of delegation: an agent that was handed a task
and no context has to obtain what it needs. Calling that repetition a defect
would judge the delegation strategy from a record that says nothing about what
either agent was given.

### A scope is the session or one agent identity

The session scope is the work carrying no `Event.SubagentID`. A nested scope is
one recorded agent identity. Nothing else divides a scope.

Context epochs are deliberately not part of the key. An epoch boundary and a
delegation boundary answer different questions — one is a break in the session's
own reasoning, the other is a separate reasoner — and crossing them would
produce an `agent × epoch × path` matrix that answers neither question better.
A scope spanning epochs is one scope here, and `internal/reacquire` continues to
answer the epoch question on its own terms.

### `internal/delegation` owns the relation, and reports it

Scope `L` is related to scope `C` when a call recorded in `L`, in session `S`,
was classified as a launch and carried a returned identity naming `C`.

That rule lives in exactly one package. ADR 0015 already made
`internal/delegation` the owner of what a launch record establishes, and this
ADR extends its result rather than reading the same records a second time:

```go
type Relation struct {
	SessionID string
	Launcher  string // the scope that made the launch, empty for the session scope
	AgentID   string // the identity the launch returned
}
```

`Report.Relations` holds each distinct pair, in the order it was first
established. The launcher is `Event.SubagentID` on the launch record, which the
package already had and did not previously keep; the launched scope is
`ToolCall.Result.Subagent.AgentID`, which it already read. Both are persisted
today: nothing new is recorded and `event.SchemaVersion` stays 1.

An earlier draft of this change had `internal/crossread` derive the pairs from
the event stream itself. That was wrong and is superseded. Sharing
`internal/work` stops two packages disagreeing about *what a launch is*, but it
does nothing to stop them disagreeing about *what a launch establishes* —
session scoping, an absent identity, an identity a scope returned for itself,
and whatever a future launch shape turns out to carry are relationship
semantics, and two copies of those drift the way ADR 0015 described two copies
of a classifier drifting.

So every guard now sits with the relation:

- the session is part of the pair, as everywhere else in Axiom, since an
  agent's identifiers are its own and are not known to mean anything outside
  the session that issued them;
- a launch whose record carried no identity establishes no pair and is still
  counted as a launch;
- a launch returning the identity of the scope that made it establishes no
  pair, since a scope handing work to itself is not a delegation between two
  scopes — the calls reporting that identity are still related to the launch,
  because that relation is about an identity and this one is about a pair;
- an identity returned twice is one pair;
- no timestamp, append position, turn identifier, tool name, subagent type or
  proximity is consulted, so there is no heuristic to get wrong.

Nested work whose identity no launch returned is never attached to a launch
nearby: two parallel agents interleaving their work in one turn is in the
capture, and proximity would have attributed half of it to the wrong scope.

### The unit of comparison is a group: one launcher and what it launched directly

A group is one launching scope together with the scopes it launched directly. A
path is reported where more than one member of one group read it.

The alternatives were considered against the captures:

- **One connected component per session** would put nearly every agent in one
  group, since almost all of them descend from the session scope. "Related"
  would collapse into "same session", and the section could no longer say *why*
  two scopes were compared.
- **Direct launcher-to-launched pairs only** would lose the fan-out case, where
  two agents launched by one scope each read the same file and neither launched
  the other. That case is common in the captures and is the most interesting
  one the analysis can see.
- **A shared explicit ancestor at any depth** is the same collapse as the first
  option one step later.

One launcher and its direct children keeps both the fan-out and the topology:
every group has a single, nameable reason for existing, which is printed above
its members. Nesting is handled without recursion — a nested agent that launches
others is the launching scope of its own group — so `root → A → B` yields a
group for `root` and a group for `A`, and `root` and `B` are not compared.

A path in two groups is listed once, with both groups under it. Pairwise
findings are never derived from a group: one recorded read is counted once per
group it takes part in, and the section's total counts paths rather than groups
so that no reading is added to itself.

### Acquisition semantics are ADR 0010's, without its scope restriction

A qualifying read is a successful, whole-file, non-ranged read of a non-empty
recorded path, classified by `internal/work` exactly as every other section
classifies one. Failed reads and reads whose outcome was never established do
not qualify: the record says what became of a call and never what it returned.
Ranged reads acquire part of a file, which is a different operation. Searches,
shell calls, writes and edits are not acquisitions here.

ADR 0010 also excluded a nested agent's reads. That exclusion was about *its*
question — epochs are the session's — and is not carried over: comparing scopes
is the entire point of this one. The discipline is reused; the accidental scope
restriction is not.

Several reads of one path in one scope are one scope, with the count carried for
detail. Repetition inside a scope is the profiler's subject, under rules this
analysis does not apply.

### Path identity is the exact recorded string

No normalization: `/tmp` and `/private/tmp` stay apart, and so do symlinks,
relative and absolute spellings, and case variants. This loses relations and
cannot invent one, which is the trade Axiom makes everywhere. It is stated in
the section and in the README rather than left for a reader to discover.

### Nothing is ordered, and the observation does not need an order

The section never says which read came first. The captures hold nested work
recorded before its launch, after it, and interleaved with a sibling's, so an
ordering claim would be wrong as often as right.

This is also why the section is not called re-acquisition: that word implies a
first time and a later one, which the core model does not establish. *Read
across related agent scopes* is the narrowest wording that survives the
implementation.

### A new package that consumes the relation, and no graph framework

`internal/crossread` owns exactly four things: qualifying acquisition state per
session, scope and path; grouping those acquisitions against relations it is
given; the one-step launcher-and-direct-children rule; and the deterministic
numbering and report model. It recognizes no launch, reads no returned
identity, and counts no delegation of its own — `Report` takes a
`delegation.Report` and uses `Relations`, `Launches` and nothing else from it.

There is still no node type, no edge type and no traversal. The relations are
sorted into one map from a launching identity to the identities it launched,
and the group is read off it directly. A generic execution graph would be
larger than the question and would invite traversals the evidence does not
support.

The dependency direction is `crossread → delegation`, and never the reverse:
delegation knows nothing about reading, so a second analysis over the same
relations can be built beside this one.

The CLI renders and derives nothing. `profileLog` takes the delegation report
once and hands it to both the turns section and this one, so the launches
described under a turn and the relations grouped here are the same derivation.

### State is per scope and per path, and resolves at report time

The accumulator holds, per session, two things: an ordinal per agent identity,
assigned in the order the log first records that scope making a call, and a read
count per scope per path. No delegation state and no event is retained.

Grouping happens when the report is taken, against relations resolved the same
way, so nested work observed before the launch that names it is handled by
construction rather than by a rule, and the answer is identical whichever order
the log holds. Both sides of the join are order-independent, which is what lets
them be joined at the end at all.

Output is deterministic: paths are ordered by the number of groups, then by
total scope memberships, then by session and path; groups and scopes are ordered
with the session scope first and nested scopes by their ordinal.

### Scopes are numbered, and identities are never printed

An agent identity is an opaque handle that names nothing a reader can act on,
and printing several of them would turn the section into the identifier dump
ADR 0015 avoided. Each nested identity is numbered within its session, in the
order the log first mentions it — as the scope that made a call or as the
identity a launch returned — and the report carries only `Root` and the ordinal,
so the identity cannot reach the page. The numbering is stated in the section as
Axiom's own.

### Four empty states, held apart

Absence of evidence is never rendered as an observation of nothing:

1. **No recorded call handed work to a nested agent.** There is no delegation
   for reading to be placed against.
2. **Launches were recorded and none carried a returned agent identity.** No
   delegated scope was established. Every log written before the identity was
   persisted says this, and so does a launch that reported failing.
3. **Related scopes recorded no qualifying read.**
4. **Related scopes read nothing in common.**

Reads in scopes that no launch relates to another are counted and reported at
the end of the section rather than dropped, so work Axiom observed does not
disappear.

### Historical data degrades honestly

Nothing is reconstructed. A log with no returned identities reports empty state
2 forever, whatever proximity it holds. No backfill, no schema bump, no new
persisted field.

### Privacy is unchanged

The analysis reads structural evidence that is already persisted: the session,
the agent identity, the operation shape, the outcome and the recorded path. No
prompt, description, response, model output or tool payload is read, and none
could make the section more useful without changing what Axiom stores.

### What is deliberately not here

No judgement of whether reading in two scopes was good or bad, no efficiency
score, no token or cost estimate, no attribution of consumption to an agent, no
recommendation, no context-handoff suggestion, no path normalization, no search,
shell, write or ranged-read comparison, no content similarity, no ordering
claim, no tree rendering, no aggregation across sessions, and no change to
`internal/profiler`, `internal/activity`, `internal/reacquire`,
`internal/timeline` or `internal/correlate`.

The one change to `internal/delegation` is additive: it keeps the launching
scope it was already reading and reports the pairs it established. Its existing
report, its launch-to-work relation and its incomplete-evidence semantics are
untouched.

## Consequences

A user who delegates now sees, in one place, which files were obtained
separately by scopes that a launch put in one group, with how many times each
scope obtained them. Neither the raw log nor a parent/child trace tree states
that: the log holds the reads and the launches as separate lines, and a trace
tree shows structure without applying acquisition outcome discipline or exact
resource identity to it.

The section is bounded: eight paths, three groups under a path, six scopes in a
group, with everything omitted counted on a line of its own.

The counts are factual and additive, which is what makes the primitive useful
later: the same observation collected under two delegation strategies could be
compared without changing its semantics. This ADR claims nothing about such a
comparison, and the section states no causality.

Two known limitations follow from the decisions above and are stated in the
report itself. Scopes more than one delegation step apart are never compared,
so a relation the log holds may go unreported. Paths that name one file in two
spellings stay apart, for the same reason everywhere else in Axiom.
