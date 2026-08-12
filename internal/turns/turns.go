// Package turns reports the turns a log recorded tool work in, and what that
// work consisted of.
//
// A turn is the execution context an agent labels with its own turn or prompt
// identifier, and which several tool calls and several model requests may
// share. It is not established to be one user request, one task, one intent or
// one complete unit of execution: nothing recorded says so. It is an identifier
// the agent put on records, and this package reports the work that carried it.
//
// A turn is recorded here only where a tool call named it. Identifiers also
// arrive on session starts and ends, and on usage records, and a turn built
// from one of those would be a turn that did nothing: a capture produced a
// session end carrying an identifier no tool call ever named, and model
// requests under identifiers of their own. Counting them would inflate how many
// turns did work, which is the question this package exists to answer.
//
// Consumption is joined elsewhere, on the identity reported here. The two
// recorded streams are kept apart until something joins them deliberately, and
// this package sees only behavior.
//
// Membership and order follow the order records were appended, which is the
// only order a log establishes. Recorded times are carried for display and
// decide nothing: hooks are separate processes and can finish out of order.
package turns

import (
	"cmp"
	"slices"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/timeline"
)

// Ref identifies one turn.
//
// The session is part of the identity because a turn identifier is the agent's
// own and is not known to be unique beyond the session that issued it. Two
// sessions naming the same turn identifier are two turns.
type Ref struct {
	SessionID string
	TurnID    string
}

// Outcomes counts calls by what the record established became of them.
//
// The three are kept apart for the reason they are kept apart everywhere else
// in Axiom: an outcome that was never established is not a failure, and a
// failed write may still have applied in part, so neither is it nothing. The
// same three states carry a second question for a launch, where what the
// outcome settles is not what persisted but whether the delegation happened at
// all.
type Outcomes struct {
	Succeeded     int
	Failed        int
	Unestablished int
}

// Total counts the calls recorded, whatever became of them.
func (o Outcomes) Total() int { return o.Succeeded + o.Failed + o.Unestablished }

func (o *Outcomes) add(outcome event.Outcome) {
	switch outcome {
	case event.OutcomeSuccess:
		o.Succeeded++
	case event.OutcomeFailure:
		o.Failed++
	default:
		o.Unestablished++
	}
}

// Composition is what the recorded calls of a turn were.
//
// The shapes are the ones Axiom's evidence model already distinguishes, and
// every recorded call falls in exactly one of them, so the categories sum to
// the turn's tool calls. Writes, edits and launches carry outcomes, for two
// different reasons: what a write or an edit leaves behind is the part of a
// turn that could have persisted, while what a launch's outcome settles is
// whether a nested agent was started at all.
type Composition struct {
	WholeReads  int
	RangedReads int
	Searches    int
	Shell       int
	Writes      Outcomes
	Edits       Outcomes

	// Launches counts the calls that declared work handed to a nested agent,
	// by what the record established became of each.
	//
	// Only the succeeded ones establish that a launch call succeeded. A call
	// reported failing declared a launch that the record says did not
	// succeed, and is not a nested agent that started; one whose outcome was
	// never established is neither, and stays apart from both.
	//
	// This counts launches and never the work a launched agent did, which
	// Turn.SubagentCalls counts instead. Neither is derived from the other,
	// and nothing recorded relates a launch to any particular later call.
	Launches Outcomes

	// Uninterpreted counts calls this composition does not place: a tool
	// outside what the adapter extracts metadata for, and input it could not
	// read. A count here is Axiom's limit, not a call that did nothing.
	//
	// A launch recorded before the adapter derived metadata for it is here
	// too, and is not counted as a launch: the record carries no evidence
	// that it was one, and reading it as one would answer from information
	// the log never held.
	Uninterpreted int
}

func (c *Composition) add(t *event.ToolCall) {
	switch shapeOf(t) {
	case shapeWholeRead:
		c.WholeReads++
	case shapeRangedRead:
		c.RangedReads++
	case shapeSearch:
		c.Searches++
	case shapeShell:
		c.Shell++
	case shapeWrite:
		c.Writes.add(t.Outcome)
	case shapeEdit:
		c.Edits.add(t.Outcome)
	case shapeSubagentLaunch:
		c.Launches.add(t.Outcome)
	default:
		c.Uninterpreted++
	}
}

// Turn is one turn identifier that recorded tool work.
type Turn struct {
	Ref

	// Ordinal counts turns within a session identity from 1, in the order
	// their first call was recorded. It is not comparable across sessions,
	// and it is not a count of the agent's turns: a turn that recorded no
	// tool call has no ordinal here.
	Ordinal int

	// First and Last are the earliest and latest times recorded on the turn's
	// calls. They are for display. Ordering and membership come from append
	// order, and hooks are separate processes whose records can carry times
	// out of order, which is why this is a widened window and not the first
	// and last record's times.
	First time.Time
	Last  time.Time

	// Epochs are the context epochs the turn recorded work in, in the order
	// the work reached them. There is always at least one, and more than one
	// is a turn whose work straddled a recorded context reset. Nothing about
	// the epoch belongs to the turn: the epoch is the session's.
	Epochs []int

	// ToolCalls counts the calls that named this turn, and Composition says
	// what they were. The two agree by construction.
	ToolCalls   int
	Composition Composition

	// SubagentCalls counts how many of those calls a nested agent made. The
	// calls were observed carrying the turn that launched the nested agent,
	// which is why they are here at all, and are counted apart because a
	// nested agent reasons in a context of its own.
	//
	// This is the work a nested agent did, and Composition.Launches is the
	// declaration that one was asked for. They are separate measurements of
	// separate records and neither is evidence for the other: a launch whose
	// agent recorded no tool call is a launch with nothing here, and nested
	// calls whose launch was never recorded — which a log that begins mid-turn
	// produces, since a launch is recorded only once its call has returned —
	// are counted here with no launch to match. Nothing recorded relates one
	// of these calls to one of those launches.
	SubagentCalls int
}

// Report is one pass of turn derivation.
type Report struct {
	// Turns holds every turn that recorded work, grouped by the order its
	// session identity first appeared and ordered by ordinal within it.
	// Showing only part of it is a decision for a report, not for the
	// analysis.
	Turns []Turn

	// CallsOutsideTurns counts recorded tool calls that no turn above holds:
	// a call that named no turn, and a call that named no session, whose turn
	// identifier therefore identifies nothing. They are counted rather than
	// dropped so that work the analysis could not place does not disappear.
	CallsOutsideTurns int
}

// Accumulator builds a report from events as they are read.
//
// It is fed the placement the timeline derived for each record, so epoch
// membership comes from the state machine that owns it and is never
// reconstructed here.
type Accumulator struct {
	// order holds turns in the order they were first recorded, and sessions
	// the order they first appeared, so that a report can be grouped without
	// any of it depending on map iteration or on recorded times.
	order    []Ref
	turns    map[Ref]*turnWork
	sessions map[string]*sessionTurns
	outside  int
}

type sessionTurns struct {
	// first is the position of the session identity among those seen, and
	// turns is how many turns have been recorded for it so far.
	first int
	turns int
}

// New returns an accumulator with no observations.
func New() *Accumulator {
	return &Accumulator{
		turns:    make(map[Ref]*turnWork),
		sessions: make(map[string]*sessionTurns),
	}
}

type turnWork struct {
	session int
	ordinal int

	first, last time.Time
	epochs      []int
	seen        map[int]struct{}

	calls       int
	composition Composition
	subagent    int
}

// Add records one event and where the timeline placed it.
//
// Only a tool call establishes a turn. A session start, a session end and a
// record this version cannot interpret carry no work, and a turn built from one
// would be a turn with nothing in it.
func (a *Accumulator) Add(ev event.Event, at timeline.Placement) {
	if ev.Type != event.TypeToolCall || ev.Tool == nil {
		return
	}
	// A call that named no turn is not assigned to one: the neighbouring turn
	// is a guess, and an empty identifier pooled across a log would put one
	// session's work under another's. A call the timeline could not place
	// named no session, so its turn identifier names nothing either.
	if ev.TurnID == "" || !at.Placed {
		a.outside++
		return
	}

	ref := Ref{SessionID: ev.SessionID, TurnID: ev.TurnID}
	w, ok := a.turns[ref]
	if !ok {
		w = a.open(ref)
	}
	w.observe(ev, at.Epoch.Ordinal)
}

// open starts a turn at the first call that named it, numbering it within its
// session.
func (a *Accumulator) open(ref Ref) *turnWork {
	s, ok := a.sessions[ref.SessionID]
	if !ok {
		s = &sessionTurns{first: len(a.sessions)}
		a.sessions[ref.SessionID] = s
	}
	s.turns++

	w := &turnWork{session: s.first, ordinal: s.turns, seen: make(map[int]struct{}, 1)}
	a.turns[ref] = w
	a.order = append(a.order, ref)
	return w
}

func (w *turnWork) observe(ev event.Event, epoch int) {
	w.mark(ev.Timestamp)
	w.calls++
	w.composition.add(ev.Tool)
	if ev.SubagentID != "" {
		w.subagent++
	}
	if _, dup := w.seen[epoch]; !dup {
		w.seen[epoch] = struct{}{}
		w.epochs = append(w.epochs, epoch)
	}
}

// mark widens the window to include one recorded time.
//
// A record with no time contributes none: a zero time is the absence of one,
// and taking it as the earliest would date the turn to the year zero. The
// window is widened rather than assigned because separate hook processes can
// record times out of order, and a window that read backwards would look like
// a defect in the log.
func (w *turnWork) mark(at time.Time) {
	if at.IsZero() {
		return
	}
	if w.first.IsZero() || at.Before(w.first) {
		w.first = at
	}
	if at.After(w.last) {
		w.last = at
	}
}

// Report summarizes everything added so far. It does not consume the
// accumulator: adding more events and reporting again is valid.
func (a *Accumulator) Report() Report {
	out := Report{
		Turns:             make([]Turn, 0, len(a.order)),
		CallsOutsideTurns: a.outside,
	}

	refs := slices.Clone(a.order)
	slices.SortStableFunc(refs, func(x, y Ref) int {
		return compare(a.turns[x], a.turns[y])
	})

	for _, ref := range refs {
		w := a.turns[ref]
		out.Turns = append(out.Turns, Turn{
			Ref:     ref,
			Ordinal: w.ordinal,
			First:   w.first,
			Last:    w.last,
			// Cloned so that a report cannot be changed by adding more
			// events, and adding more events cannot be changed by a report.
			Epochs:        slices.Clone(w.epochs),
			ToolCalls:     w.calls,
			Composition:   w.composition,
			SubagentCalls: w.subagent,
		})
	}
	return out
}

// compare groups a session's turns together and orders them by ordinal, which
// is the order their work was first recorded. Both keys come from append order,
// so two runs over one log cannot disagree.
func compare(a, b *turnWork) int {
	if c := cmp.Compare(a.session, b.session); c != 0 {
		return c
	}
	return cmp.Compare(a.ordinal, b.ordinal)
}
