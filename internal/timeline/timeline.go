// Package timeline reconstructs the structure a log recorded: the session
// identities it contains, and the context epochs observed within each of them.
//
// Axiom cannot observe an execution. An agent reports a session identity, a
// turn, and the fact that it started a context; nothing it reports says "this
// was one attempt at one task". What can be derived is three nested scopes —
// session identity, context epoch, turn — and this package derives the first
// two. Anything that needs "one sitting" has to be asserted by someone who
// knows what the task was, and is deliberately not invented here.
//
// A context epoch is the work recorded for one session identity between the
// points where the agent reported starting a context. Those points are the only
// boundaries: membership follows append order, and timestamps never decide it.
// An epoch also ends where the agent reported the session ending, and where the
// log does.
//
// Every reported start is a point the profiler resets its scopes on, so an epoch
// opened by one begins exactly where repetition stops being compared across.
// The two are not the same everywhere, though: an end closes an epoch here and
// resets nothing there, so a record written after one — hooks are separate
// processes and can finish out of order — continues a run into the next epoch.
package timeline

import (
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// OpeningKind says how much is known about the start of an epoch.
//
// The three cases are kept apart on purpose. A start Axiom saw and a start it
// did not are different observations, and a start whose source the agent left
// out is a third: collapsing them would turn "we were not listening yet" into
// "the agent told us nothing".
type OpeningKind string

const (
	// OpeningRecorded is a session_start whose source the agent reported.
	OpeningRecorded OpeningKind = "recorded"
	// OpeningUnspecified is a session_start that carried no source.
	OpeningUnspecified OpeningKind = "unspecified"
	// OpeningAbsent is an epoch opened by work arriving for a session with no
	// start observed before it: a log that begins mid-session, or a record
	// that arrived after the session was reported ending.
	OpeningAbsent OpeningKind = "absent"
)

// Opening is how an epoch began.
type Opening struct {
	Kind OpeningKind
	// Source is the agent's own word for what started the context, stored
	// exactly as recorded. Axiom does not interpret it, so a value no version
	// of Axiom has seen before still reports as itself.
	Source string
}

// ClosingKind says how an epoch ended.
type ClosingKind string

const (
	// ClosingReset is a later session_start for the same session identity:
	// the agent reported starting a context while one was already under way.
	ClosingReset ClosingKind = "reset"
	// ClosingEnded is a session_end for the session identity.
	ClosingEnded ClosingKind = "ended"
	// ClosingOpen is an epoch nothing was recorded after. It says what the log
	// contains and nothing about whether the agent is still running.
	ClosingOpen ClosingKind = "open"
)

// Closing is how an epoch ended.
type Closing struct {
	Kind ClosingKind
	// Reason is the agent's own word for why the session ended, stored exactly
	// as recorded, and empty when it reported none or when the epoch was not
	// closed by a session_end.
	Reason string
}

// Epoch is one context epoch of one session identity.
type Epoch struct {
	// Ordinal counts epochs within a session identity from 1, in append
	// order. It is not comparable across session identities.
	Ordinal int
	Opening Opening
	Closing Closing

	// First and Last are the recorded times of the first and last records
	// that belong to the epoch. They are for display: ordering comes from
	// append order, and two concurrent sessions interleave in one log, so
	// these windows may overlap and need not be monotonic.
	First time.Time
	Last  time.Time

	// ToolCalls counts the tool calls recorded in the epoch. Every record
	// belongs to exactly one epoch, so these do sum to the session's total.
	ToolCalls int
	// SubagentCalls counts how many of those a subagent made. A subagent has
	// its own context; the reset the epoch boundary records is the session's.
	SubagentCalls int
	// Turns counts the distinct turns that have work in this epoch. A turn can
	// span a reset — compaction has been observed opening a context in the
	// middle of one — so these must never be summed into a session total.
	Turns int
}

// Session is one session identity and the epochs recorded for it.
type Session struct {
	ID     string
	Epochs []Epoch
	// EndsWithoutEpoch counts session_end records that arrived with no epoch
	// open. A log that starts after a session did contains one, and it is
	// reported rather than dropped: it is the only trace of that session.
	EndsWithoutEpoch int
}

// ToolCalls totals the tool calls recorded for the session identity.
func (s Session) ToolCalls() int {
	var n int
	for _, e := range s.Epochs {
		n += e.ToolCalls
	}
	return n
}

// Report is the structure of everything added so far.
type Report struct {
	// Sessions are ordered by where each identity first appears in the log.
	Sessions []Session
	// Unidentified counts records that carried no session identity. They
	// belong to no epoch: without an identity there is no context to place
	// them in.
	Unidentified int
}

// Epochs totals the epochs across every session identity.
func (r Report) Epochs() int {
	var n int
	for _, s := range r.Sessions {
		n += len(s.Epochs)
	}
	return n
}

// Timeline derives the structure of an event log.
type Timeline struct {
	// order holds session identities in the order they first appear, which is
	// the only ordering the log establishes between them.
	order        []string
	sessions     map[string]*sessionState
	unidentified int
}

// New returns an empty timeline.
func New() *Timeline {
	return &Timeline{sessions: make(map[string]*sessionState)}
}

// Add records one event. Events must arrive in append order, which is the order
// they were written and the only order the log guarantees.
func (t *Timeline) Add(ev event.Event) {
	if ev.SessionID == "" {
		t.unidentified++
		return
	}

	// A session identity is only recorded once something places it: a
	// boundary, an end, or work. A record this version cannot interpret would
	// otherwise add a session with no observed structure at all, which reads
	// as a session that did nothing rather than as a record Axiom could not
	// place.
	switch ev.Type {
	case event.TypeSessionStart:
		s := t.session(ev.SessionID)
		// Every session_start is a boundary, including the first one for an
		// identity, where there is nothing to close. Claude Code records one
		// on compaction, keeping the same identity, and one after /clear,
		// under a new identity. Axiom does not need to tell those apart to
		// place the boundary: it reports the source it was given.
		s.close(Closing{Kind: ClosingReset})
		s.begin(opening(detail(ev).Source), ev.Timestamp)

	case event.TypeSessionEnd:
		s := t.session(ev.SessionID)
		if s.current == nil {
			s.endsWithoutEpoch++
			return
		}
		s.current.mark(ev.Timestamp)
		s.close(Closing{Kind: ClosingEnded, Reason: detail(ev).Reason})

	case event.TypeToolCall:
		// A tool_call carrying no call is not work Axiom can describe, and it
		// does not open a context: structure follows observed work.
		if ev.Tool == nil {
			return
		}
		s := t.session(ev.SessionID)
		if s.current == nil {
			s.begin(Opening{Kind: OpeningAbsent}, ev.Timestamp)
		}
		s.observe(ev)
	}
}

// Report summarizes everything added so far. It does not consume the timeline:
// adding more events and reporting again is valid.
func (t *Timeline) Report() Report {
	r := Report{Unidentified: t.unidentified}
	for _, id := range t.order {
		s := t.sessions[id]
		r.Sessions = append(r.Sessions, Session{
			ID:               id,
			Epochs:           s.epochs(),
			EndsWithoutEpoch: s.endsWithoutEpoch,
		})
	}
	return r
}

func (t *Timeline) session(id string) *sessionState {
	s, ok := t.sessions[id]
	if !ok {
		s = &sessionState{}
		t.sessions[id] = s
		t.order = append(t.order, id)
	}
	return s
}

// detail returns the lifecycle detail of a session record. Nothing validates
// the field on the way into or out of the log, so a start or an end can arrive
// carrying none: that is a source or a reason the agent did not report, and it
// is never a reason to lose the boundary itself.
func detail(ev event.Event) event.Session {
	if ev.Session == nil {
		return event.Session{}
	}
	return *ev.Session
}

// opening describes the start of an epoch from the source recorded on it. A
// source Axiom has never seen is still a source the agent reported, so it is
// kept verbatim and reported as recorded.
func opening(source string) Opening {
	if source == "" {
		return Opening{Kind: OpeningUnspecified}
	}
	return Opening{Kind: OpeningRecorded, Source: source}
}

// sessionState accumulates the epochs of one session identity.
type sessionState struct {
	closed  []*epochState
	current *epochState
	// endsWithoutEpoch counts session_end records that closed nothing.
	endsWithoutEpoch int
}

func (s *sessionState) begin(o Opening, at time.Time) {
	s.current = &epochState{opening: o, turns: make(map[string]struct{})}
	s.current.mark(at)
}

// close ends the open epoch, if there is one. A boundary with nothing open is
// not an error: it is the ordinary first start of a session identity.
func (s *sessionState) close(c Closing) {
	if s.current == nil {
		return
	}
	s.current.closing = c
	s.closed = append(s.closed, s.current)
	s.current = nil
}

func (s *sessionState) observe(ev event.Event) {
	e := s.current
	e.mark(ev.Timestamp)
	e.toolCalls++
	if ev.SubagentID != "" {
		e.subagentCalls++
	}
	// A call whose turn was not recorded cannot be counted as a turn with
	// work: the turn it belonged to was never established.
	if ev.TurnID != "" {
		e.turns[ev.TurnID] = struct{}{}
	}
}

// epochs reports the epochs of the session, closed and open alike. An epoch
// still open is reported as open at the end of the log without being closed,
// so that adding more events later continues it rather than starting another.
//
// The ordinal is the position, so the two cannot drift apart: epochs are closed
// in the order they were opened, and the open one is always the last.
func (s *sessionState) epochs() []Epoch {
	out := make([]Epoch, 0, len(s.closed)+1)
	for _, e := range s.closed {
		out = append(out, e.epoch())
	}
	if s.current != nil {
		open := s.current.epoch()
		open.Closing = Closing{Kind: ClosingOpen}
		out = append(out, open)
	}
	for i := range out {
		out[i].Ordinal = i + 1
	}
	return out
}

type epochState struct {
	opening       Opening
	closing       Closing
	first         time.Time
	last          time.Time
	toolCalls     int
	subagentCalls int
	turns         map[string]struct{}
}

// mark widens the epoch's display window to include one record's time. A record
// that carries no time cannot widen it: the window reports times that were
// recorded, and it is never used to decide what belongs to the epoch.
func (e *epochState) mark(at time.Time) {
	if at.IsZero() {
		return
	}
	if e.first.IsZero() {
		e.first = at
	}
	e.last = at
}

func (e *epochState) epoch() Epoch {
	return Epoch{
		Opening:       e.opening,
		Closing:       e.closing,
		First:         e.first,
		Last:          e.last,
		ToolCalls:     e.toolCalls,
		SubagentCalls: e.subagentCalls,
		Turns:         len(e.turns),
	}
}
