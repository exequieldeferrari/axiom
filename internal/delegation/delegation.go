// Package delegation relates a recorded subagent launch to the recorded tool
// calls that reported the identity that launch returned.
//
// It establishes one thing, and the wording is the whole of it:
//
//	These nested tool calls reported the same agent identity that the agent
//	returned for this recorded launch.
//
// That is an identity match on an opaque handle the agent itself put on both
// records.
//
// The same records establish one other thing, which this package also owns
// and reports beside the launches: which scope handed work to which. A launch
// record carries the identity of the scope that made it and the identity it
// returned, so it names a pair, and an analysis of how work moved between
// scopes has to read that pair from here rather than from the event stream a
// second time. What may establish a pair, and what may not, is one rule in
// one place; an analysis built on top decides what to do with the pairs and
// never what one is.
//
// Neither statement is a claim that every operation the nested agent performed
// was recorded, that any model request belongs to it, that its cost is known,
// that the launch caused anything else nearby, that it finished what it was
// asked for, or that delegating was useful. None of that is recorded.
//
// # Identity
//
// The key is the session and the returned identity, and deliberately nothing
// else. A session is part of it because an agent's identifiers are its own and
// are not known to be unique beyond the session that issued it, which is the
// rule the rest of Axiom already follows. A turn is not part of it: a capture
// observed a live nested agent whose lifecycle record carried a turn identifier
// other than the one its launch was recorded under, so a relation keyed by turn
// would break on evidence that already exists. Nested calls were observed
// carrying the launching turn, and this package does not depend on it.
//
// # Order
//
// Nothing here reads a timestamp, an append position, a tool name, a subagent
// type or a proximity. The relation is an identity match and is therefore the
// same whichever order the records arrive in, which is what makes it usable at
// all: a synchronous launch's nested calls were observed recorded before the
// launch itself, because a hook sees a call only once it has returned, and an
// asynchronous launch's arrive after it. Both were captured, and so was the
// interleaving of two agents' work between one pair of launches.
//
// Records are accumulated as they are read and the relation is resolved when
// the report is taken, so no observation ever waits on one that has not
// arrived.
package delegation

import (
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/work"
)

// Ref identifies one nested agent: the session that recorded it, and the
// opaque identity the agent reported for it.
type Ref struct {
	SessionID string
	AgentID   string
}

// Launch is one recorded call that handed work to a nested agent.
//
// SessionID, TurnID and InvocationID say where the launch call itself was
// recorded. They are provenance and are not part of the relation: only the
// session takes part in matching, and the turn takes part in nothing.
type Launch struct {
	SessionID    string
	TurnID       string
	InvocationID string

	// Outcome is what the record established became of the launch call,
	// carried unchanged from the call. A launch reported failing started no
	// nested agent; one with no outcome recorded settles nothing either way.
	Outcome event.Outcome

	// Work is the nested work attributable to this launch, and is nil when
	// the record carried no returned identity.
	//
	// Nil is not zero, and the difference is the point. Nil means the launch
	// named no agent Axiom could match — every record written before the
	// identity was persisted says this, and so does a launch that reported
	// failing. A non-nil Work with no calls in it means the identity was
	// recorded and no call in the log reported it, which is a statement about
	// the log and not about what the agent did.
	Work *Work
}

// Work is the recorded work attributable to one launch.
type Work struct {
	// Calls counts the tool calls that reported the launch's returned
	// identity, and Composition says what they were. The two agree by
	// construction.
	Calls       int
	Composition work.Composition

	// TurnIDs counts the distinct turn identifiers those calls named, and is
	// zero when they named none. The relation does not use it: it is
	// reported because a nested agent whose work spanned more than one turn
	// identifier is a fact about the log that a turn-shaped reading of this
	// section would otherwise hide.
	TurnIDs int
}

// Unrelated counts recorded work by a nested agent that no recorded launch
// accounts for.
//
// A log that begins after a launch produces this, and so does one that ends
// before the launch was recorded: a synchronous launch reaches a hook only
// once its call has returned, so its nested calls are already in the log while
// it is not. Every record written before the returned identity was persisted
// produces it too.
//
// It is counted rather than dropped, and never assigned to a launch nearby.
// Two launches of the same type, in one turn, with their nested work
// interleaved, were captured: proximity would have attributed half of it to
// the wrong agent.
type Unrelated struct {
	// Calls counts those tool calls, and Agents the distinct identities
	// behind them.
	Calls  int
	Agents int
}

// Relation is one delegation the record establishes: a launch recorded in one
// scope that returned the identity of another.
//
// It is the same evidence the launches above are built from, stated from the
// other side. A Launch says what the agent an identity names went on to do;
// a Relation says which scope handed work to it. Both come from one record,
// and holding them apart is what lets an analysis ask about the structure
// without taking on the work under it.
//
// Nothing here is a graph. It is a flat list of the pairs one field of one
// record established, and an analysis that wants more shape than that has to
// build it from these and say so.
type Relation struct {
	SessionID string

	// Launcher is the scope that made the launch call, taken from the
	// record's own subagent identity. Empty is the session scope, which is
	// the work recorded under no nested identity, and is never a missing
	// value: a launch always has a scope that made it.
	Launcher string

	// AgentID is the identity the launch returned, which is the scope the
	// work was handed to.
	AgentID string
}

// Report is one pass of delegation derivation.
type Report struct {
	// Launches holds every recorded launch, in the order the launch records
	// were appended, which is the only order a log establishes.
	Launches []Launch

	// Relations holds each distinct delegation the launches above
	// established, in the order it was first established.
	//
	// A launch with no returned identity recorded contributes none: there is
	// no scope to name. One returning the identity of the scope that made it
	// contributes none either, since a scope handing work to itself is not a
	// delegation and is not observable evidence of one. An identity returned
	// twice is one relation, because the pair it names is one pair.
	Relations []Relation

	// Unrelated accounts for the nested work no launch above holds.
	Unrelated Unrelated
}

// Accumulator builds a report from events as they are read.
type Accumulator struct {
	launches []launch
	// claimed holds the identities recorded launches returned, and nested
	// the work each identity was observed doing. The two are filled
	// independently and joined only when the report is taken, because
	// neither is known to arrive first.
	claimed map[Ref]struct{}
	nested  map[Ref]*nested
}

// launch is a recorded launch call as it was observed.
type launch struct {
	ref Ref
	// identified says whether the record carried a returned identity, so
	// that an empty one is never mistaken for one that was recorded.
	identified bool
	// launcher is the scope the launch call was recorded in, empty for the
	// session scope. It takes no part in relating a launch to its nested
	// work, and is kept because it is the other half of what the record
	// establishes: which scope handed the work over.
	launcher     string
	turnID       string
	invocationID string
	outcome      event.Outcome
}

// nested is the work one identity was observed doing.
type nested struct {
	calls       int
	composition work.Composition
	turns       map[string]struct{}
}

// New returns an accumulator with no observations.
func New() *Accumulator {
	return &Accumulator{
		claimed: make(map[Ref]struct{}),
		nested:  make(map[Ref]*nested),
	}
}

// Add records one event.
//
// A record can be both things at once and is counted as both: a nested agent
// that launches another agent made the call it is recorded under and created
// the agent it returned. Nothing special is written for that case, because the
// relation is between an identity and the calls reporting it, and a launch's
// own identity plays no part in matching its nested work.
func (a *Accumulator) Add(ev event.Event) {
	if ev.Type != event.TypeToolCall || ev.Tool == nil {
		return
	}

	// A call that named no session cannot be related to anything: the
	// identity on it is scoped to a session the record does not name.
	if ev.SessionID == "" {
		return
	}

	if ev.SubagentID != "" {
		a.observe(Ref{SessionID: ev.SessionID, AgentID: ev.SubagentID}, ev)
	}
	if work.Of(ev.Tool) == work.SubagentLaunch {
		a.open(ev)
	}
}

// observe counts one call against the identity that made it.
func (a *Accumulator) observe(ref Ref, ev event.Event) {
	n, ok := a.nested[ref]
	if !ok {
		n = &nested{turns: make(map[string]struct{}, 1)}
		a.nested[ref] = n
	}
	n.calls++
	n.composition.Add(ev.Tool)
	if ev.TurnID != "" {
		n.turns[ev.TurnID] = struct{}{}
	}
}

// open records one launch call.
//
// A launch is recognized from the metadata the adapter derived, exactly as a
// turn's composition recognizes one, so the two cannot come to disagree about
// how many launches a log holds. A record carrying a returned identity and no
// launch metadata is not promoted into a launch here: the evidence that it was
// one is what the metadata carries, and reading it out of the response instead
// would count a launch in this section that every other section does not.
func (a *Accumulator) open(ev event.Event) {
	l := launch{
		ref:          Ref{SessionID: ev.SessionID},
		launcher:     ev.SubagentID,
		turnID:       ev.TurnID,
		invocationID: ev.Tool.InvocationID,
		outcome:      ev.Tool.Outcome,
	}
	if r := ev.Tool.Result; r != nil && r.Subagent != nil && r.Subagent.AgentID != "" {
		l.ref.AgentID = r.Subagent.AgentID
		l.identified = true
		a.claimed[l.ref] = struct{}{}
	}
	a.launches = append(a.launches, l)
}

// Report summarizes everything added so far. It does not consume the
// accumulator: adding more events and reporting again is valid.
func (a *Accumulator) Report() Report {
	out := Report{Launches: make([]Launch, 0, len(a.launches))}
	related := make(map[Relation]struct{}, len(a.launches))

	for _, l := range a.launches {
		out.Launches = append(out.Launches, Launch{
			SessionID:    l.ref.SessionID,
			TurnID:       l.turnID,
			InvocationID: l.invocationID,
			Outcome:      l.outcome,
			Work:         a.workFor(l),
		})
		if r, ok := relate(l); ok {
			if _, seen := related[r]; !seen {
				related[r] = struct{}{}
				out.Relations = append(out.Relations, r)
			}
		}
	}

	for ref, n := range a.nested {
		if _, ok := a.claimed[ref]; ok {
			continue
		}
		out.Unrelated.Agents++
		out.Unrelated.Calls += n.calls
	}
	return out
}

// relate states which scope a launch handed work to, where the record
// establishes one.
//
// A launch with no returned identity names no scope. A launch returning the
// identity of the scope that made it names one scope twice, which is not a
// delegation between two of them; the calls reporting that identity are still
// related to the launch above, since that relation is about the identity and
// not about the pair.
func relate(l launch) (Relation, bool) {
	if !l.identified || l.launcher == l.ref.AgentID {
		return Relation{}, false
	}
	return Relation{
		SessionID: l.ref.SessionID,
		Launcher:  l.launcher,
		AgentID:   l.ref.AgentID,
	}, true
}

// workFor resolves what a launch's returned identity was observed doing.
//
// A launch with no identity gets nothing, and one whose identity nothing
// reported gets an empty measurement rather than nothing: the two states are
// held apart here so that no reader downstream has to reconstruct which is
// which.
func (a *Accumulator) workFor(l launch) *Work {
	if !l.identified {
		return nil
	}

	w := Work{}
	if n, ok := a.nested[l.ref]; ok {
		w.Calls = n.calls
		w.Composition = n.composition
		w.TurnIDs = len(n.turns)
	}
	return &w
}
