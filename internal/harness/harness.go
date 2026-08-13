// Package harness reports the provenance recorded with the session starts in
// one log.
//
// It establishes one thing, and the wording is the whole of it:
//
//	When this session start was recorded, Axiom observed these paths, at the
//	project root it resolved for that session, in these states.
//
// That is a record of an observation and not a description of the agent. It is
// not the configuration the agent loaded, not a complete account of what
// shaped its behavior, and not an identity two sessions can be said to share.
// Axiom sees a fixed handful of project-local paths from a hook; an agent
// reads user, enterprise and command-line configuration, follows imports out
// of the files it does read, and is handed a model, a permission mode and
// tools from places no hook reports. None of that reaches a record, so none of
// it is claimed.
//
// # Nothing is derived from the filesystem here
//
// Every value in this report was written into the log by the hook that
// observed it. This package reads records. It never looks at a file, which is
// the only way a report about a session recorded on Monday can still describe
// Monday.
//
// # Scope
//
// Provenance belongs to a recorded session start, not to a session and not to
// a log. One session identity can record several starts — compaction and a
// resume were both observed keeping it — and the observations taken at two of
// them can differ, because the files can change in between. Merging them would
// invent one harness for a session that was observed under two.
//
// Consecutive starts whose observed components were identical are reported
// together, which says that the observations matched and nothing further.
package harness

import (
	"slices"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// Observation is what one or more consecutive session starts recorded.
type Observation struct {
	// Starts are the ordinals of the recorded session starts this
	// observation covers, in the order they were recorded. More than one
	// means consecutive starts recorded identical components: the same
	// paths, in the same states, with the same digests.
	//
	// Ordinals count every recorded start of the session identity,
	// including starts that recorded no provenance, so a gap in them is a
	// start this report has nothing to say about rather than a start that
	// did not happen.
	Starts []int

	// Components are the observations exactly as they were recorded, in the
	// order the collector wrote them.
	Components []event.HarnessComponent
}

// Session is the provenance recorded under one session identity.
type Session struct {
	ID string

	// Observations are what was recorded, in the order it was recorded. It
	// is empty where the session recorded starts and no provenance with any
	// of them.
	Observations []Observation

	// Starts counts every recorded session start for this identity, and
	// StartsWithoutProvenance the ones that carried no provenance.
	//
	// The second is not a count of sessions that ran unconfigured. It
	// counts starts recorded by an Axiom that did not observe provenance,
	// or starts where no project could be resolved.
	Starts                  int
	StartsWithoutProvenance int
}

// Report is the provenance one pass over a log found.
type Report struct {
	// Sessions holds every session identity that recorded a start, in the
	// order the log first records one, including those that recorded no
	// provenance. It is complete: showing part of it is a decision for a
	// report and not for the analysis.
	Sessions []Session
}

// Accumulator collects recorded provenance.
type Accumulator struct {
	order    []string
	sessions map[string]*Session
}

// New returns an accumulator with no observations.
func New() *Accumulator {
	return &Accumulator{sessions: make(map[string]*Session)}
}

// Add records one event.
//
// Only a session start carries provenance, and a start naming no session
// identity is dropped: an observation belongs to the session it was taken for,
// and there is nothing to attribute it to without one.
func (a *Accumulator) Add(ev event.Event) {
	if ev.Type != event.TypeSessionStart || ev.SessionID == "" {
		return
	}

	s, ok := a.sessions[ev.SessionID]
	if !ok {
		s = &Session{ID: ev.SessionID}
		a.sessions[ev.SessionID] = s
		a.order = append(a.order, ev.SessionID)
	}

	s.Starts++
	if ev.Session == nil || ev.Session.Harness == nil {
		s.StartsWithoutProvenance++
		return
	}
	s.record(ev.Session.Harness.Components)
}

// record adds one start's components, joining it to the observation before it
// where the two matched.
//
// They are joined only where the earlier one covers the immediately preceding
// start. A start that recorded nothing sits between them otherwise, and
// reporting the two together would close a gap the log actually has.
func (s *Session) record(components []event.HarnessComponent) {
	if n := len(s.Observations); n > 0 {
		last := &s.Observations[n-1]
		if last.Starts[len(last.Starts)-1] == s.Starts-1 &&
			slices.Equal(last.Components, components) {
			last.Starts = append(last.Starts, s.Starts)
			return
		}
	}
	s.Observations = append(s.Observations, Observation{
		Starts:     []int{s.Starts},
		Components: components,
	})
}

// Report returns what has been recorded so far. It does not consume the
// accumulator: adding more events and reporting again is valid.
func (a *Accumulator) Report() Report {
	out := Report{Sessions: make([]Session, 0, len(a.order))}
	for _, id := range a.order {
		out.Sessions = append(out.Sessions, *a.sessions[id])
	}
	return out
}

// Recorded reports whether any session start in the log carried provenance.
func (r Report) Recorded() bool {
	for _, s := range r.Sessions {
		if len(s.Observations) > 0 {
			return true
		}
	}
	return false
}
